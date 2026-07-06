// Package httpapi implements ClipBridge's HTTP/JSON API and WSS endpoints for
// both the self-signed device port and the plain-HTTP Web console port. The two
// ports share one Server (store, hub, settings) but expose different route sets:
// the device port carries pairing submission, device auth and the device WSS;
// the Web port carries admin/user authentication, management and the Web WSS.
package httpapi

import (
	"net/http"

	"github.com/mokeyjay/clipbridge/server/internal/blobstore"
	"github.com/mokeyjay/clipbridge/server/internal/store"
	"github.com/mokeyjay/clipbridge/server/internal/wshub"
	"github.com/mokeyjay/clipbridge/shared/protocol"
)

// apiPrefix is the version prefix all routes live under (prd/05-api-and-events.md).
const apiPrefix = "/api/v1"

// Web auth cookies. Admin and user sessions use separate HttpOnly session
// cookies so both can be held at once (the console switches between them without
// re-login). The CSRF cookie is shared and readable by the console JS for the
// double-submit check.
const (
	adminSessionCookie = "cb_session_admin"
	userSessionCookie  = "cb_session_user"
	csrfCookieName     = "cb_csrf"
	csrfHeaderName     = "X-CSRF-Token"
)

// sessionCookieForType maps a subject type to its session cookie name.
func sessionCookieForType(t protocol.SubjectType) string {
	if t == protocol.SubjectAdmin {
		return adminSessionCookie
	}
	return userSessionCookie
}

// sessionTTLSeconds is the Web session lifetime (24h).
const sessionTTLSeconds = 24 * 60 * 60

// Server bundles the dependencies every handler needs. It is constructed once
// and its DeviceHandler/WebHandler are mounted on the two listeners.
type Server struct {
	store *store.Store
	hub   *wshub.Hub
	blobs *blobstore.Store

	// serverFingerprint is the device-port certificate SHA-256, surfaced on the
	// pairing code response so users can verify it against the client.
	serverFingerprint string

	// secureCookies controls the Secure attribute on auth cookies. Production
	// sets true; tests over plain HTTP set false.
	secureCookies bool

	// pairingLimiter rate-limits pairing submissions per client IP.
	pairingLimiter *rateLimiter

	// webAssets serves the embedded React console as an SPA on the Web port. It
	// may be nil (e.g. in tests or when the console was not built in).
	webAssets http.Handler
}

// New builds a Server. secureCookies should be true in production. webAssets may
// be nil to serve API/WSS only.
func New(st *store.Store, hub *wshub.Hub, blobs *blobstore.Store, serverFingerprint string, secureCookies bool, webAssets http.Handler) *Server {
	return &Server{
		store:             st,
		hub:               hub,
		blobs:             blobs,
		serverFingerprint: serverFingerprint,
		secureCookies:     secureCookies,
		pairingLimiter:    newRateLimiter(pairingRateMaxAttempts, pairingRateWindow),
		webAssets:         webAssets,
	}
}

// DeviceHandler returns the router for the self-signed HTTPS device port.
func (s *Server) DeviceHandler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /healthz", health)

	// Device pairing (client pins this port's certificate before calling).
	mux.HandleFunc("POST "+apiPrefix+"/pairing-requests", s.handleSubmitPairingRequest)
	mux.HandleFunc("GET "+apiPrefix+"/pairing-requests/{id}", s.handlePollPairingRequest)

	// Device WSS with protocol-version negotiation and token auth.
	mux.HandleFunc("GET "+apiPrefix+"/ws/device", s.handleDeviceWS)

	// Online target discovery and ciphertext relay (device token auth).
	mux.HandleFunc("GET "+apiPrefix+"/device/profile", s.requireDevice(s.handleDeviceProfile))
	mux.HandleFunc("GET "+apiPrefix+"/device/settings", s.requireDevice(s.handleGetDeviceSettings))
	mux.HandleFunc("PATCH "+apiPrefix+"/device/settings", s.requireDevice(s.handleUpdateDeviceSettings))
	mux.HandleFunc("GET "+apiPrefix+"/device/effective-config", s.requireDevice(s.handleEffectiveConfig))
	mux.HandleFunc("GET "+apiPrefix+"/device/targets", s.requireDevice(s.handleDeviceTargets))
	mux.HandleFunc("GET "+apiPrefix+"/device/peers", s.requireDevice(s.handleDevicePeers))
	mux.HandleFunc("POST "+apiPrefix+"/clipboard/items", s.requireDevice(s.handleUploadItem))
	mux.HandleFunc("GET "+apiPrefix+"/clipboard/deliveries/pending", s.requireDevice(s.handlePendingDeliveries))
	mux.HandleFunc("GET "+apiPrefix+"/clipboard/deliveries/{id}", s.requireDevice(s.handleGetDelivery))
	mux.HandleFunc("GET "+apiPrefix+"/clipboard/deliveries/{id}/content", s.requireDevice(s.handleDownloadContent))
	mux.HandleFunc("POST "+apiPrefix+"/clipboard/deliveries/{id}/ack", s.requireDevice(s.handleAckDelivery))
	mux.HandleFunc("POST "+apiPrefix+"/clipboard/deliveries/{id}/reject", s.requireDevice(s.handleRejectDelivery))

	return s.baseChain(mux)
}

// WebHandler returns the router for the plain-HTTP Web console port.
func (s *Server) WebHandler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /healthz", health)

	// Authentication. /auth/me and /auth/logout resolve cookies themselves (both
	// roles may be present), so they are not gated by a single-session middleware.
	mux.HandleFunc("POST "+apiPrefix+"/auth/login", s.handleLogin)
	mux.HandleFunc("POST "+apiPrefix+"/auth/logout", s.handleLogout)
	mux.HandleFunc("GET "+apiPrefix+"/auth/me", s.handleMe)
	mux.HandleFunc("PATCH "+apiPrefix+"/auth/password", s.requireUser(s.handleChangePassword))

	// Admin management (admin session only).
	mux.HandleFunc("GET "+apiPrefix+"/admin/settings", s.requireAdmin(s.handleGetServerSettings))
	mux.HandleFunc("PATCH "+apiPrefix+"/admin/settings", s.requireAdmin(s.handleUpdateServerSettings))
	mux.HandleFunc("GET "+apiPrefix+"/admin/stats", s.requireAdmin(s.handleAdminStats))
	mux.HandleFunc("GET "+apiPrefix+"/admin/sync-logs", s.requireAdmin(s.handleListSyncLogs))
	mux.HandleFunc("GET "+apiPrefix+"/admin/audit-logs", s.requireAdmin(s.handleListAuditLogs))
	mux.HandleFunc("GET "+apiPrefix+"/admin/users", s.requireAdmin(s.handleListUsers))
	mux.HandleFunc("POST "+apiPrefix+"/admin/users", s.requireAdmin(s.handleCreateUser))
	mux.HandleFunc("PATCH "+apiPrefix+"/admin/users/{id}", s.requireAdmin(s.handleUpdateUser))
	mux.HandleFunc("POST "+apiPrefix+"/admin/users/{id}/reset-password", s.requireAdmin(s.handleResetUserPassword))
	mux.HandleFunc("GET "+apiPrefix+"/admin/users/{id}/devices", s.requireAdmin(s.handleAdminListUserDevices))
	mux.HandleFunc("PATCH "+apiPrefix+"/admin/devices/{id}", s.requireAdmin(s.handleAdminUpdateDevice))
	mux.HandleFunc("DELETE "+apiPrefix+"/admin/devices/{id}", s.requireAdmin(s.handleAdminDeleteDevice))
	mux.HandleFunc("PATCH "+apiPrefix+"/admin/profile", s.requireAdmin(s.handleUpdateAdminProfile))

	// User self-service (user session only).
	mux.HandleFunc("GET "+apiPrefix+"/user/settings", s.requireUser(s.handleGetUserSettings))
	mux.HandleFunc("PATCH "+apiPrefix+"/user/settings", s.requireUser(s.handleUpdateUserSettings))
	mux.HandleFunc("GET "+apiPrefix+"/user/devices", s.requireUser(s.handleListUserDevices))
	mux.HandleFunc("PATCH "+apiPrefix+"/user/devices/{id}", s.requireUser(s.handleUpdateUserDevice))
	mux.HandleFunc("DELETE "+apiPrefix+"/user/devices/{id}", s.requireUser(s.handleRevokeUserDevice))

	// Pairing-code management + request confirmation (user session only).
	mux.HandleFunc("POST "+apiPrefix+"/pairing-codes", s.requireUser(s.handleCreatePairingCode))
	mux.HandleFunc("GET "+apiPrefix+"/pairing-codes/current", s.requireUser(s.handleGetCurrentPairingCode))
	mux.HandleFunc("DELETE "+apiPrefix+"/pairing-codes/current", s.requireUser(s.handleCancelPairingCode))
	mux.HandleFunc("POST "+apiPrefix+"/pairing-requests/{id}/confirm", s.requireUser(s.handleConfirmPairingRequest))
	mux.HandleFunc("POST "+apiPrefix+"/pairing-requests/{id}/reject", s.requireUser(s.handleRejectPairingRequest))

	// Web WSS (session auth).
	mux.HandleFunc("GET "+apiPrefix+"/ws/web", s.handleWebWS)

	// Embedded React console as the catch-all (more specific routes above win).
	if s.webAssets != nil {
		mux.Handle("/", s.webAssets)
	}

	return s.baseChain(mux)
}

// health is the shared liveness probe.
func health(w http.ResponseWriter, _ *http.Request) {
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write([]byte("ok"))
}

package httpapi

import (
	"net/http"
	"strconv"

	"github.com/mokeyjay/clipbridge/server/internal/security"
	"github.com/mokeyjay/clipbridge/server/internal/store"
	"github.com/mokeyjay/clipbridge/server/internal/wshub"
	"github.com/mokeyjay/clipbridge/shared/protocol"
)

// handleDeviceWS authenticates a device by bearer token, negotiates the protocol
// version, then upgrades to WSS and registers the connection. Auth and version
// checks run before Upgrade so failures return normal JSON errors.
func (s *Server) handleDeviceWS(w http.ResponseWriter, r *http.Request) {
	token, ok := bearerScheme(r, "Bearer")
	if !ok {
		s.writeError(w, r, http.StatusUnauthorized, protocol.ErrAuthRequired, "缺少设备令牌")
		return
	}
	device, dt, err := s.store.AuthenticateDeviceToken(security.TokenHash(token))
	if err != nil {
		s.writeError(w, r, http.StatusUnauthorized, protocol.ErrAuthRequired, "设备令牌无效")
		return
	}
	if !s.deviceUsable(w, r, device) {
		return
	}

	// Protocol-version negotiation: the device declares its version via query
	// param; reject out-of-range versions before upgrading.
	if !negotiateProtocol(w, r, s) {
		return
	}

	// Record token usage (throttled is unnecessary at connect time).
	_ = s.store.TouchDeviceToken(dt.ID, s.store.Now().Unix())

	conn, err := wshub.Upgrader.Upgrade(w, r, nil)
	if err != nil {
		return // Upgrade already wrote the error
	}
	c := s.hub.RegisterDevice(device.UserID, device.ID)
	s.servePumps(conn, c, func() { s.hub.Heartbeat(c) })
}

// handleWebWS authenticates the user Web session and registers a console
// connection (used to push pairing/sync events to the user's browser).
func (s *Server) handleWebWS(w http.ResponseWriter, r *http.Request) {
	p, ok := s.authenticateRole(w, r, userSessionCookie, protocol.SubjectUser) // GET: no CSRF
	if !ok {
		return
	}
	conn, err := wshub.Upgrader.Upgrade(w, r, nil)
	if err != nil {
		return
	}
	c := s.hub.RegisterWeb(p.SubjectID)
	s.servePumps(conn, c, nil)
}

// deviceUsable verifies the device and its owner are not disabled/revoked,
// writing the precise error code otherwise.
func (s *Server) deviceUsable(w http.ResponseWriter, r *http.Request, device *store.Device) bool {
	switch device.Status {
	case protocol.DeviceDisabled:
		s.writeError(w, r, http.StatusForbidden, protocol.ErrDeviceDisabled, "设备已被禁用")
		return false
	case protocol.DeviceRevoked:
		s.writeError(w, r, http.StatusForbidden, protocol.ErrDeviceRevoked, "设备已被吊销")
		return false
	}
	user, err := s.store.GetUserByID(device.UserID)
	if err != nil {
		s.writeError(w, r, http.StatusUnauthorized, protocol.ErrAuthRequired, "设备所属用户无效")
		return false
	}
	if user.Status == protocol.UserDisabled {
		s.writeError(w, r, http.StatusForbidden, protocol.ErrUserDisabled, "用户已被禁用")
		return false
	}
	return true
}

// negotiateProtocol validates the device-declared protocol version against the
// server's supported range, writing PROTOCOL_VERSION_UNSUPPORTED on mismatch. A
// missing version defaults to the current ProtocolVersion for convenience.
func negotiateProtocol(w http.ResponseWriter, r *http.Request, s *Server) bool {
	raw := r.URL.Query().Get("protocol_version")
	if raw == "" {
		return true
	}
	v, err := strconv.Atoi(raw)
	if err != nil || v < protocol.MinSupportedProtocolVersion || v > protocol.MaxSupportedProtocolVersion {
		s.writeError(w, r, http.StatusBadRequest, protocol.ErrProtocolVersionUnsupported, "协议版本不受支持")
		return false
	}
	return true
}

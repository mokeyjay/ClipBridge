package httpapi

import (
	"errors"
	"net/http"
	"strings"

	"github.com/mokeyjay/clipbridge/server/internal/security"
	"github.com/mokeyjay/clipbridge/server/internal/store"
	"github.com/mokeyjay/clipbridge/shared/protocol"
)

// pairingCodeTTLSeconds is the 6-digit code lifetime (prd/04-data-model.md §5.3).
const pairingCodeTTLSeconds = 300

// pendingRequestView is a pending pairing request as shown to the confirming user.
type pendingRequestView struct {
	RequestID      string `json:"request_id"`
	DeviceName     string `json:"device_name"`
	Platform       string `json:"platform"`
	ClientVersion  string `json:"client_version"`
	KeyFingerprint string `json:"key_fingerprint"`
	CreatedAt      string `json:"created_at"`
}

// currentPairingCodeView is the GET /pairing-codes/current response. The
// plaintext code is never shown again (only its hash is stored).
type currentPairingCodeView struct {
	Active                  bool                 `json:"active"`
	ExpiresAt               string               `json:"expires_at,omitempty"`
	ServerName              string               `json:"server_name"`
	ServerFingerprintSHA256 string               `json:"server_fingerprint_sha256"`
	PendingRequests         []pendingRequestView `json:"pending_requests"`
}

// handleCreatePairingCode issues a fresh 6-digit code, replacing any active one.
func (s *Server) handleCreatePairingCode(w http.ResponseWriter, r *http.Request) {
	p := principalFrom(r.Context())
	code, err := security.RandomNumericCode(6)
	if err != nil {
		s.writeError(w, r, http.StatusInternalServerError, protocol.ErrorCode("INTERNAL"), "生成配对码失败")
		return
	}
	pc, err := s.store.CreatePairingCode(p.SubjectID, security.TokenHash(code), pairingCodeTTLSeconds)
	if err != nil {
		s.writeError(w, r, http.StatusInternalServerError, protocol.ErrorCode("INTERNAL"), "创建配对码失败")
		return
	}
	settings, _ := s.store.GetServerSettings()
	name := "ClipBridge"
	if settings != nil {
		name = settings.ServerName
	}
	// 只记录「生成了配对码」这一事实，配对码本身绝不入日志。
	s.audit(r, "pairing.code_create", "pairing", pc.ID, "", "")
	writeJSON(w, http.StatusCreated, protocol.PairingCodeResponse{
		Code: code, ExpiresAt: rfc3339(pc.ExpiresAt), ServerName: name, ServerFingerprintSHA256: s.serverFingerprint,
	})
}

// handleGetCurrentPairingCode returns the active code's metadata (not the code
// itself) and the user's pending requests awaiting confirmation.
func (s *Server) handleGetCurrentPairingCode(w http.ResponseWriter, r *http.Request) {
	p := principalFrom(r.Context())
	settings, _ := s.store.GetServerSettings()
	name := "ClipBridge"
	if settings != nil {
		name = settings.ServerName
	}
	view := currentPairingCodeView{ServerName: name, ServerFingerprintSHA256: s.serverFingerprint, PendingRequests: []pendingRequestView{}}

	if pc, err := s.store.GetActivePairingCodeByUser(p.SubjectID); err == nil {
		view.Active = true
		view.ExpiresAt = rfc3339(pc.ExpiresAt)
	}
	pending, err := s.store.ListPendingPairingRequestsByUser(p.SubjectID)
	if err != nil {
		s.writeError(w, r, http.StatusInternalServerError, protocol.ErrorCode("INTERNAL"), "读取配对请求失败")
		return
	}
	for _, pr := range pending {
		view.PendingRequests = append(view.PendingRequests, pendingRequestView{
			RequestID: pr.ID, DeviceName: pr.DeviceName, Platform: string(pr.Platform),
			ClientVersion: pr.ClientVersion, KeyFingerprint: protocol.KeyFingerprint(pr.HPKEPublicKey), CreatedAt: rfc3339(pr.CreatedAt),
		})
	}
	writeJSON(w, http.StatusOK, view)
}

// handleCancelPairingCode cancels the caller's active code.
func (s *Server) handleCancelPairingCode(w http.ResponseWriter, r *http.Request) {
	p := principalFrom(r.Context())
	if err := s.store.CancelActivePairingCode(p.SubjectID); err != nil {
		s.writeError(w, r, http.StatusInternalServerError, protocol.ErrorCode("INTERNAL"), "取消配对码失败")
		return
	}
	writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
}

// handleConfirmPairingRequest confirms a pending request, creating the device.
func (s *Server) handleConfirmPairingRequest(w http.ResponseWriter, r *http.Request) {
	p := principalFrom(r.Context())
	device, err := s.store.ConfirmPairingRequest(p.SubjectID, r.PathValue("id"))
	switch {
	case errors.Is(err, store.ErrNotFound):
		s.writeError(w, r, http.StatusNotFound, protocol.ErrorCode("NOT_FOUND"), "配对请求不存在")
		return
	case errors.Is(err, store.ErrPairingNotPending):
		s.writeError(w, r, http.StatusConflict, protocol.ErrorCode("PAIRING_NOT_PENDING"), "配对请求已处理或已过期")
		return
	case err != nil:
		s.writeError(w, r, http.StatusInternalServerError, protocol.ErrorCode("INTERNAL"), "确认配对失败")
		return
	}
	s.audit(r, "pairing.confirm", "device", device.ID, device.Name, "")
	writeJSON(w, http.StatusOK, toDeviceView(device))
}

// handleRejectPairingRequest rejects a pending request.
func (s *Server) handleRejectPairingRequest(w http.ResponseWriter, r *http.Request) {
	p := principalFrom(r.Context())
	err := s.store.RejectPairingRequest(p.SubjectID, r.PathValue("id"))
	switch {
	case errors.Is(err, store.ErrPairingNotPending):
		s.writeError(w, r, http.StatusConflict, protocol.ErrorCode("PAIRING_NOT_PENDING"), "配对请求已处理或已过期")
		return
	case err != nil:
		s.writeError(w, r, http.StatusInternalServerError, protocol.ErrorCode("INTERNAL"), "拒绝配对失败")
		return
	}
	s.audit(r, "pairing.reject", "pairing", r.PathValue("id"), "", "")
	writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
}

// --- device-port handlers (no Web session) ---

// handleSubmitPairingRequest is the client's POST /pairing-requests. It is rate
// limited per source IP and matches the submitted code against an active code.
func (s *Server) handleSubmitPairingRequest(w http.ResponseWriter, r *http.Request) {
	if !s.pairingLimiter.Allow(clientIP(r)) {
		s.writeError(w, r, http.StatusTooManyRequests, protocol.ErrPairingRateLimited, "配对请求过于频繁")
		return
	}
	var req protocol.SubmitPairingRequest
	if !s.decodeJSON(w, r, &req) {
		return
	}
	platform := protocol.Platform(req.Platform)
	if platform != protocol.PlatformDarwin && platform != protocol.PlatformWindows {
		s.writeError(w, r, http.StatusBadRequest, protocol.ErrorCode("BAD_REQUEST"), "平台不合法")
		return
	}
	if strings.TrimSpace(req.DeviceName) == "" || req.HPKEPublicKey == "" || len(req.Code) != 6 {
		s.writeError(w, r, http.StatusBadRequest, protocol.ErrorCode("BAD_REQUEST"), "配对请求参数不合法")
		return
	}

	code, err := s.store.GetActivePairingCodeByHash(security.TokenHash(req.Code))
	if errors.Is(err, store.ErrNotFound) {
		// No active, non-expired code matches; do not distinguish wrong vs expired.
		s.writeError(w, r, http.StatusBadRequest, protocol.ErrPairingCodeInvalid, "配对码无效或已过期")
		return
	}
	if err != nil {
		s.writeError(w, r, http.StatusInternalServerError, protocol.ErrorCode("INTERNAL"), "校验配对码失败")
		return
	}

	pollToken, err := security.RandomToken(32)
	if err != nil {
		s.writeError(w, r, http.StatusInternalServerError, protocol.ErrorCode("INTERNAL"), "生成令牌失败")
		return
	}
	pr, err := s.store.CreatePairingRequest(code, strings.TrimSpace(req.DeviceName), platform, req.ClientVersion, req.HPKEPublicKey, security.TokenHash(pollToken))
	if err != nil {
		s.writeError(w, r, http.StatusInternalServerError, protocol.ErrorCode("INTERNAL"), "创建配对请求失败")
		return
	}

	// Notify the owner's Web console that a device awaits confirmation.
	s.hub.NotifyUserWeb(code.UserID, protocol.Event{
		Event:      protocol.EventPairingRequested,
		OccurredAt: rfc3339(s.store.Now().Unix()),
		Data:       protocol.PairingRequestedData{RequestID: pr.ID, DeviceName: pr.DeviceName, Platform: string(pr.Platform), ClientVersion: pr.ClientVersion},
	})

	writeJSON(w, http.StatusCreated, protocol.SubmitPairingResponse{RequestID: pr.ID, PollToken: pollToken, ExpiresAt: rfc3339(pr.ExpiresAt)})
}

// handlePollPairingRequest is the client's GET /pairing-requests/{id}, authorized
// by the one-time poll token. On a confirmed request it claims and returns the
// device token exactly once.
func (s *Server) handlePollPairingRequest(w http.ResponseWriter, r *http.Request) {
	pollToken, ok := bearerScheme(r, "Pairing")
	if !ok {
		s.writeError(w, r, http.StatusUnauthorized, protocol.ErrAuthRequired, "缺少配对令牌")
		return
	}
	pr, err := s.store.GetPairingRequestByID(r.PathValue("id"))
	if errors.Is(err, store.ErrNotFound) {
		s.writeError(w, r, http.StatusNotFound, protocol.ErrorCode("NOT_FOUND"), "配对请求不存在")
		return
	}
	if err != nil {
		s.writeError(w, r, http.StatusInternalServerError, protocol.ErrorCode("INTERNAL"), "读取配对请求失败")
		return
	}
	if security.TokenHash(pollToken) != pr.PollTokenHash {
		s.writeError(w, r, http.StatusUnauthorized, protocol.ErrAuthRequired, "配对令牌无效")
		return
	}

	// Treat past-due pending/confirmed requests as expired to the caller.
	status := pr.Status
	if (status == protocol.PairingRequestPending || status == protocol.PairingRequestConfirmed) && pr.ExpiresAt <= s.store.Now().Unix() {
		status = protocol.PairingRequestExpired
	}

	switch status {
	case protocol.PairingRequestConfirmed:
		// First claim mints and returns the device token exactly once.
		deviceToken, err := security.RandomToken(32)
		if err != nil {
			s.writeError(w, r, http.StatusInternalServerError, protocol.ErrorCode("INTERNAL"), "生成设备令牌失败")
			return
		}
		device, err := s.store.ClaimDeviceToken(pr.ID, security.TokenHash(deviceToken))
		if errors.Is(err, store.ErrPairingNotConfirmed) {
			// Lost the race; report the now-current status without a token.
			s.respondPollStatus(w, pr.ID)
			return
		}
		if err != nil {
			s.writeError(w, r, http.StatusInternalServerError, protocol.ErrorCode("INTERNAL"), "领取设备令牌失败")
			return
		}
		settings, _ := s.store.GetServerSettings()
		serverID := ""
		if settings != nil {
			serverID = settings.ServerID
		}
		writeJSON(w, http.StatusOK, protocol.PairingResultResponse{
			Status:      protocol.PairingRequestConfirmed,
			Device:      &protocol.PairingResultDevice{ID: device.ID, UserID: device.UserID, ServerID: serverID},
			DeviceToken: deviceToken,
		})
	default:
		writeJSON(w, http.StatusOK, protocol.PairingResultResponse{Status: status})
	}
}

// respondPollStatus re-reads a request and returns its current status (no token).
func (s *Server) respondPollStatus(w http.ResponseWriter, requestID string) {
	pr, err := s.store.GetPairingRequestByID(requestID)
	if err != nil {
		writeJSON(w, http.StatusOK, protocol.PairingResultResponse{Status: protocol.PairingRequestClaimed})
		return
	}
	writeJSON(w, http.StatusOK, protocol.PairingResultResponse{Status: pr.Status})
}

// bearerScheme extracts a token from "Authorization: <scheme> <token>".
func bearerScheme(r *http.Request, scheme string) (string, bool) {
	h := r.Header.Get("Authorization")
	prefix := scheme + " "
	if len(h) <= len(prefix) || !strings.EqualFold(h[:len(prefix)], prefix) {
		return "", false
	}
	token := strings.TrimSpace(h[len(prefix):])
	return token, token != ""
}

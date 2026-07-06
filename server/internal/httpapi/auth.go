package httpapi

import (
	"net/http"
	"strings"

	"github.com/mokeyjay/clipbridge/server/internal/security"
	"github.com/mokeyjay/clipbridge/shared/protocol"
)

// issueSession creates a Web session for a subject and sets the session + CSRF
// cookies. The plaintext session token is stored only as a hash.
func (s *Server) issueSession(w http.ResponseWriter, subjectType protocol.SubjectType, subjectID string) error {
	token, err := security.RandomToken(32)
	if err != nil {
		return err
	}
	csrf, err := security.RandomToken(32)
	if err != nil {
		return err
	}
	if _, err := s.store.CreateWebSession(subjectType, subjectID, security.TokenHash(token), sessionTTLSeconds); err != nil {
		return err
	}
	http.SetCookie(w, &http.Cookie{
		Name: sessionCookieForType(subjectType), Value: token, Path: "/",
		HttpOnly: true, Secure: s.secureCookies, SameSite: http.SameSiteLaxMode, MaxAge: sessionTTLSeconds,
	})
	http.SetCookie(w, &http.Cookie{
		Name: csrfCookieName, Value: csrf, Path: "/",
		HttpOnly: false, Secure: s.secureCookies, SameSite: http.SameSiteLaxMode, MaxAge: sessionTTLSeconds,
	})
	return nil
}

// clearRoleCookie expires one role's session cookie (per-role logout). The shared
// CSRF cookie is left in place; it's a double-submit token, harmless on its own.
func (s *Server) clearRoleCookie(w http.ResponseWriter, t protocol.SubjectType) {
	http.SetCookie(w, &http.Cookie{Name: sessionCookieForType(t), Value: "", Path: "/", MaxAge: -1, HttpOnly: true, Secure: s.secureCookies})
}

// handleLogin authenticates an admin or user and starts a session. Admins and
// users share the endpoint; the admin record is checked first.
func (s *Server) handleLogin(w http.ResponseWriter, r *http.Request) {
	var req protocol.LoginRequest
	if !s.decodeJSON(w, r, &req) {
		return
	}

	// Admin path.
	if admin, err := s.store.GetAdminByUsername(req.Username); err == nil {
		if security.VerifyPassword(admin.PasswordHash, req.Password) {
			if err := s.issueSession(w, protocol.SubjectAdmin, admin.ID); err != nil {
				s.writeError(w, r, http.StatusInternalServerError, protocol.ErrorCode("INTERNAL"), "创建会话失败")
				return
			}
			writeJSON(w, http.StatusOK, protocol.MeResponse{SubjectType: protocol.SubjectAdmin, SubjectID: admin.ID, Username: admin.Username})
			return
		}
		s.invalidCredentials(w, r)
		return
	}

	// User path.
	user, err := s.store.GetUserByUsername(req.Username)
	if err != nil {
		s.invalidCredentials(w, r)
		return
	}
	if !security.VerifyPassword(user.PasswordHash, req.Password) {
		s.invalidCredentials(w, r)
		return
	}
	if user.Status == protocol.UserDisabled {
		s.writeError(w, r, http.StatusForbidden, protocol.ErrUserDisabled, "用户已被禁用")
		return
	}
	if err := s.issueSession(w, protocol.SubjectUser, user.ID); err != nil {
		s.writeError(w, r, http.StatusInternalServerError, protocol.ErrorCode("INTERNAL"), "创建会话失败")
		return
	}
	writeJSON(w, http.StatusOK, protocol.MeResponse{SubjectType: protocol.SubjectUser, SubjectID: user.ID, Username: user.Username})
}

// invalidCredentials returns a uniform 401 to avoid leaking which field is wrong.
func (s *Server) invalidCredentials(w http.ResponseWriter, r *http.Request) {
	s.writeError(w, r, http.StatusUnauthorized, protocol.ErrAuthRequired, "用户名或密码错误")
}

// handleLogout destroys one role's session (?role=admin|user) or both when no
// role is given, and clears the matching cookie(s). CSRF is checked since this
// mutates state. Always 200 so the console can clean up regardless.
func (s *Server) handleLogout(w http.ResponseWriter, r *http.Request) {
	if !s.checkCSRF(w, r) {
		return
	}
	roles := []protocol.SubjectType{protocol.SubjectAdmin, protocol.SubjectUser}
	switch r.URL.Query().Get("role") {
	case "admin":
		roles = []protocol.SubjectType{protocol.SubjectAdmin}
	case "user":
		roles = []protocol.SubjectType{protocol.SubjectUser}
	}
	for _, role := range roles {
		if cookie, err := r.Cookie(sessionCookieForType(role)); err == nil && cookie.Value != "" {
			_ = s.store.DeleteWebSessionByTokenHash(security.TokenHash(cookie.Value))
		}
		s.clearRoleCookie(w, role)
	}
	writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
}

// meResponse reports which principals are currently logged in (both may be).
type meResponse struct {
	Admin *protocol.MeResponse `json:"admin,omitempty"`
	User  *protocol.MeResponse `json:"user,omitempty"`
}

// handleMe returns whichever of the admin/user sessions are currently valid, so
// the console can offer one-click switching when both are present.
func (s *Server) handleMe(w http.ResponseWriter, r *http.Request) {
	out := meResponse{}
	if p := s.peekPrincipal(r, adminSessionCookie); p != nil && p.SubjectType == protocol.SubjectAdmin {
		out.Admin = &protocol.MeResponse{SubjectType: p.SubjectType, SubjectID: p.SubjectID, Username: p.Username}
	}
	if p := s.peekPrincipal(r, userSessionCookie); p != nil && p.SubjectType == protocol.SubjectUser {
		out.User = &protocol.MeResponse{SubjectType: p.SubjectType, SubjectID: p.SubjectID, Username: p.Username}
	}
	writeJSON(w, http.StatusOK, out)
}

// handleChangePassword updates the caller's own password, then rotates sessions
// so other devices are logged out and the current client gets a fresh session.
func (s *Server) handleChangePassword(w http.ResponseWriter, r *http.Request) {
	p := principalFrom(r.Context())
	var req protocol.ChangePasswordRequest
	if !s.decodeJSON(w, r, &req) {
		return
	}
	if len(req.NewPassword) < minPasswordLen {
		s.writeError(w, r, http.StatusBadRequest, protocol.ErrorCode("BAD_REQUEST"), "新密码过短")
		return
	}

	currentHash, ok := s.subjectPasswordHash(p)
	if !ok || !security.VerifyPassword(currentHash, req.CurrentPassword) {
		s.writeError(w, r, http.StatusUnauthorized, protocol.ErrAuthRequired, "当前密码错误")
		return
	}
	newHash, err := security.HashPassword(req.NewPassword)
	if err != nil {
		s.writeError(w, r, http.StatusInternalServerError, protocol.ErrorCode("INTERNAL"), "处理密码失败")
		return
	}
	if err := s.setSubjectPassword(p, newHash); err != nil {
		s.writeError(w, r, http.StatusInternalServerError, protocol.ErrorCode("INTERNAL"), "更新密码失败")
		return
	}
	// Invalidate all sessions for this subject, then re-issue for this client.
	_ = s.store.DeleteSessionsForSubject(p.SubjectType, p.SubjectID)
	if err := s.issueSession(w, p.SubjectType, p.SubjectID); err != nil {
		s.writeError(w, r, http.StatusInternalServerError, protocol.ErrorCode("INTERNAL"), "重建会话失败")
		return
	}
	// 只记录「本人修改了密码」这一事实，新旧密码绝不入日志。
	s.audit(r, "account.change_password", "account", p.SubjectID, p.Username, "")
	writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
}

// subjectPasswordHash returns the current password hash for a principal.
func (s *Server) subjectPasswordHash(p *principal) (string, bool) {
	if p.SubjectType == protocol.SubjectAdmin {
		admin, err := s.store.GetAdmin()
		if err != nil {
			return "", false
		}
		return admin.PasswordHash, true
	}
	user, err := s.store.GetUserByID(p.SubjectID)
	if err != nil {
		return "", false
	}
	return user.PasswordHash, true
}

// setSubjectPassword writes a new password hash for a principal.
func (s *Server) setSubjectPassword(p *principal, hash string) error {
	if p.SubjectType == protocol.SubjectAdmin {
		return s.store.UpdateAdminPassword(p.SubjectID, hash)
	}
	return s.store.UpdateUserPassword(p.SubjectID, hash)
}

// minPasswordLen is the minimum accepted password length for set/change/register.
const minPasswordLen = 8

// validUsername enforces a conservative username shape.
func validUsername(name string) bool {
	name = strings.TrimSpace(name)
	if len(name) < 3 || len(name) > 32 {
		return false
	}
	for _, r := range name {
		if !(r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' || r == '-' || r == '_' || r == '.') {
			return false
		}
	}
	return true
}

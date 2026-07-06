package httpapi

import (
	"context"
	"errors"
	"log/slog"
	"net/http"

	"github.com/mokeyjay/clipbridge/server/internal/security"
	"github.com/mokeyjay/clipbridge/server/internal/store"
	"github.com/mokeyjay/clipbridge/shared/protocol"
)

// deviceAuth is the authenticated device bound to a request by device-token auth.
type deviceAuth struct {
	device  *store.Device
	tokenID string
}

// baseChain wraps a mux with the always-on middleware: request id assignment and
// panic recovery. It applies to every route on both ports.
func (s *Server) baseChain(next http.Handler) http.Handler {
	return s.withRequestID(s.recoverPanic(next))
}

// withRequestID assigns a request id, exposes it on the response header and
// stores it in the context for the error envelope.
func (s *Server) withRequestID(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		id := newRequestID()
		w.Header().Set("X-Request-ID", id)
		next.ServeHTTP(w, r.WithContext(context.WithValue(r.Context(), ctxRequestID, id)))
	})
}

// recoverPanic converts a handler panic into a 500 without crashing the process.
func (s *Server) recoverPanic(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		defer func() {
			if rec := recover(); rec != nil {
				slog.Error("handler panic", "request_id", requestIDFrom(r.Context()), "panic", rec)
				s.writeError(w, r, http.StatusInternalServerError, protocol.ErrorCode("INTERNAL"), "服务器内部错误")
			}
		}()
		next.ServeHTTP(w, r)
	})
}

// isMutating reports whether a method changes server state and thus needs CSRF.
func isMutating(method string) bool {
	switch method {
	case http.MethodPost, http.MethodPatch, http.MethodPut, http.MethodDelete:
		return true
	}
	return false
}

// authenticate resolves the Web session cookie to a principal and enforces the
// double-submit CSRF check on mutating requests. On any failure it writes the
// appropriate error and returns false.
func (s *Server) checkCSRF(w http.ResponseWriter, r *http.Request) bool {
	if !isMutating(r.Method) {
		return true
	}
	csrfCookie, err := r.Cookie(csrfCookieName)
	header := r.Header.Get(csrfHeaderName)
	if err != nil || csrfCookie.Value == "" || header == "" || header != csrfCookie.Value {
		s.writeError(w, r, http.StatusForbidden, protocol.ErrForbidden, "CSRF 校验失败")
		return false
	}
	return true
}

// authenticateRole resolves a specific role's session cookie to a principal of
// the wanted type and enforces CSRF. On any failure it writes the error.
func (s *Server) authenticateRole(w http.ResponseWriter, r *http.Request, cookieName string, want protocol.SubjectType) (*principal, bool) {
	cookie, err := r.Cookie(cookieName)
	if err != nil || cookie.Value == "" {
		s.writeError(w, r, http.StatusUnauthorized, protocol.ErrAuthRequired, "需要登录")
		return nil, false
	}
	sess, err := s.store.GetWebSessionByTokenHash(security.TokenHash(cookie.Value))
	if err != nil || sess.SubjectType != want {
		s.writeError(w, r, http.StatusUnauthorized, protocol.ErrAuthRequired, "会话无效或已过期")
		return nil, false
	}
	if !s.checkCSRF(w, r) {
		return nil, false
	}
	p, ok := s.loadSubject(w, r, sess)
	if !ok {
		return nil, false
	}
	_ = s.store.TouchWebSession(sess.ID)
	return p, true
}

// peekPrincipal resolves a cookie to a live principal without writing errors or
// tearing anything down (used by /auth/me to report who is logged in). Returns
// nil for a missing, invalid or disabled session.
func (s *Server) peekPrincipal(r *http.Request, cookieName string) *principal {
	cookie, err := r.Cookie(cookieName)
	if err != nil || cookie.Value == "" {
		return nil
	}
	sess, err := s.store.GetWebSessionByTokenHash(security.TokenHash(cookie.Value))
	if err != nil {
		return nil
	}
	switch sess.SubjectType {
	case protocol.SubjectAdmin:
		if admin, err := s.store.GetAdmin(); err == nil && admin.ID == sess.SubjectID {
			return &principal{SubjectType: protocol.SubjectAdmin, SubjectID: admin.ID, Username: admin.Username, SessionID: sess.ID}
		}
	case protocol.SubjectUser:
		if u, err := s.store.GetUserByID(sess.SubjectID); err == nil && u.Status != protocol.UserDisabled {
			return &principal{SubjectType: protocol.SubjectUser, SubjectID: u.ID, Username: u.Username, SessionID: sess.ID}
		}
	}
	return nil
}

// loadSubject resolves a session to its live admin/user principal, rejecting
// disabled users (and tearing down their sessions and connections).
func (s *Server) loadSubject(w http.ResponseWriter, r *http.Request, sess *store.WebSession) (*principal, bool) {
	switch sess.SubjectType {
	case protocol.SubjectAdmin:
		admin, err := s.store.GetAdmin()
		if err != nil || admin.ID != sess.SubjectID {
			s.writeError(w, r, http.StatusUnauthorized, protocol.ErrAuthRequired, "会话无效")
			return nil, false
		}
		return &principal{SubjectType: protocol.SubjectAdmin, SubjectID: admin.ID, Username: admin.Username, SessionID: sess.ID}, true
	case protocol.SubjectUser:
		user, err := s.store.GetUserByID(sess.SubjectID)
		if errors.Is(err, store.ErrNotFound) {
			s.writeError(w, r, http.StatusUnauthorized, protocol.ErrAuthRequired, "会话无效")
			return nil, false
		}
		if err != nil {
			s.writeError(w, r, http.StatusInternalServerError, protocol.ErrorCode("INTERNAL"), "读取用户失败")
			return nil, false
		}
		if user.Status == protocol.UserDisabled {
			_ = s.store.DeleteSessionsForSubject(protocol.SubjectUser, user.ID)
			s.hub.CloseUser(user.ID)
			s.writeError(w, r, http.StatusForbidden, protocol.ErrUserDisabled, "用户已被禁用")
			return nil, false
		}
		return &principal{SubjectType: protocol.SubjectUser, SubjectID: user.ID, Username: user.Username, SessionID: sess.ID}, true
	default:
		s.writeError(w, r, http.StatusUnauthorized, protocol.ErrAuthRequired, "会话无效")
		return nil, false
	}
}

// requireAdmin gates a handler behind the admin session cookie.
func (s *Server) requireAdmin(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		p, ok := s.authenticateRole(w, r, adminSessionCookie, protocol.SubjectAdmin)
		if !ok {
			return
		}
		next(w, r.WithContext(context.WithValue(r.Context(), ctxPrincipal, p)))
	}
}

// requireUser gates a handler behind the user session cookie.
func (s *Server) requireUser(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		p, ok := s.authenticateRole(w, r, userSessionCookie, protocol.SubjectUser)
		if !ok {
			return
		}
		next(w, r.WithContext(context.WithValue(r.Context(), ctxPrincipal, p)))
	}
}

// requireDevice gates a handler behind a valid device bearer token, rejecting
// disabled/revoked devices and disabled owners. It binds the device to the
// request context for handlers to read.
func (s *Server) requireDevice(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
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
		_ = s.store.TouchDeviceToken(dt.ID, s.store.Now().Unix())
		ctx := context.WithValue(r.Context(), ctxDevice, &deviceAuth{device: device, tokenID: dt.ID})
		next(w, r.WithContext(ctx))
	}
}

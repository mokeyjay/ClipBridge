package httpapi

import (
	"context"
	"encoding/json"
	"io"
	"net/http"

	"github.com/google/uuid"
	"github.com/mokeyjay/clipbridge/shared/protocol"
)

// ctxKey is the unexported type for request-scoped context keys.
type ctxKey int

const (
	ctxRequestID ctxKey = iota
	ctxPrincipal
	ctxDevice
)

// principal is an authenticated Web session subject (admin or user).
type principal struct {
	SubjectType protocol.SubjectType
	SubjectID   string
	Username    string
	SessionID   string
}

// requestIDFrom returns the request id stored by the requestID middleware.
func requestIDFrom(ctx context.Context) string {
	if v, ok := ctx.Value(ctxRequestID).(string); ok {
		return v
	}
	return ""
}

// principalFrom returns the authenticated principal, if any.
func principalFrom(ctx context.Context) *principal {
	if p, ok := ctx.Value(ctxPrincipal).(*principal); ok {
		return p
	}
	return nil
}

// deviceFrom returns the authenticated device, if any.
func deviceFrom(ctx context.Context) *deviceAuth {
	if d, ok := ctx.Value(ctxDevice).(*deviceAuth); ok {
		return d
	}
	return nil
}

// maxJSONBody caps decoded request bodies to keep control-plane requests small.
const maxJSONBody = 1 << 20 // 1 MiB

// decodeJSON strictly decodes a JSON request body into dst, rejecting unknown
// fields and oversized bodies. It returns false (after writing a 400) on error.
func (s *Server) decodeJSON(w http.ResponseWriter, r *http.Request, dst any) bool {
	r.Body = http.MaxBytesReader(w, r.Body, maxJSONBody)
	dec := json.NewDecoder(r.Body)
	dec.DisallowUnknownFields()
	if err := dec.Decode(dst); err != nil && err != io.EOF {
		s.writeError(w, r, http.StatusBadRequest, protocol.ErrorCode("BAD_REQUEST"), "请求体无法解析")
		return false
	}
	return true
}

// writeJSON writes status and a JSON body.
func writeJSON(w http.ResponseWriter, status int, body any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	if body != nil {
		_ = json.NewEncoder(w).Encode(body)
	}
}

// writeError writes the standard error envelope with the request id and a stable
// machine-readable code.
func (s *Server) writeError(w http.ResponseWriter, r *http.Request, status int, code protocol.ErrorCode, message string) {
	writeJSON(w, status, protocol.ErrorResponse{
		RequestID: requestIDFrom(r.Context()),
		Error:     protocol.ErrorBody{Code: code, Message: message},
	})
}

// newRequestID returns a fresh request id.
func newRequestID() string { return uuid.NewString() }

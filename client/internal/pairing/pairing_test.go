package pairing

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"

	"github.com/mokeyjay/clipbridge/client/internal/credstore"
	"github.com/mokeyjay/clipbridge/shared/protocol"
)

// pairStub is a TLS stub of the pairing endpoints that confirms after one poll.
func pairStub(t *testing.T) (*httptest.Server, *int32) {
	t.Helper()
	var polls int32
	mux := http.NewServeMux()
	mux.HandleFunc("POST /api/v1/pairing-requests", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(protocol.SubmitPairingResponse{RequestID: "req-1", PollToken: "poll-1", ExpiresAt: ""})
	})
	mux.HandleFunc("GET /api/v1/pairing-requests/{id}", func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Pairing poll-1" {
			w.WriteHeader(401)
			return
		}
		n := atomic.AddInt32(&polls, 1)
		w.Header().Set("Content-Type", "application/json")
		// First poll: still pending; second: confirmed with a token (once).
		if n < 2 {
			_ = json.NewEncoder(w).Encode(protocol.PairingResultResponse{Status: protocol.PairingRequestPending})
			return
		}
		_ = json.NewEncoder(w).Encode(protocol.PairingResultResponse{
			Status:      protocol.PairingRequestConfirmed,
			Device:      &protocol.PairingResultDevice{ID: "dev-1", UserID: "user-1", ServerID: "srv-1"},
			DeviceToken: "device-token-xyz",
		})
	})
	srv := httptest.NewTLSServer(mux)
	t.Cleanup(srv.Close)
	return srv, &polls
}

// TestPairingFlowPersists drives submit→poll→confirm and checks persisted creds.
func TestPairingFlowPersists(t *testing.T) {
	srv, polls := pairStub(t)
	sum := sha256.Sum256(srv.Certificate().Raw)
	fp := hex.EncodeToString(sum[:])

	store, err := credstore.Open(filepath.Join(t.TempDir(), "cfg"))
	if err != nil {
		t.Fatalf("open store: %v", err)
	}

	id, err := run(context.Background(), store, Request{
		ServerURL: srv.URL, ServerFingerprint: fp, Code: "123456",
		DeviceName: "MacBook", Platform: protocol.PlatformDarwin, ClientVersion: "0.1.0",
	}, 10*time.Millisecond)
	if err != nil {
		t.Fatalf("pairing run: %v", err)
	}
	if id.DeviceID != "dev-1" || id.UserID != "user-1" || id.ServerID != "srv-1" {
		t.Errorf("identity = %+v", id)
	}
	if atomic.LoadInt32(polls) < 2 {
		t.Errorf("expected at least 2 polls, got %d", *polls)
	}

	// Credentials must be persisted and the store now considered paired.
	if !store.IsPaired() {
		t.Error("store not paired after successful run")
	}
	if tok, _ := store.LoadToken(); tok != "device-token-xyz" {
		t.Errorf("token = %q", tok)
	}
	if savedFp, _ := store.LoadServerFingerprint(); savedFp != fp {
		t.Errorf("fingerprint not pinned: %q", savedFp)
	}
	if key, _ := store.LoadPrivateKey(); len(key) == 0 {
		t.Error("private key not saved")
	}
}

// TestPairingRejected maps a rejected status to ErrRejected.
func TestPairingRejected(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("POST /api/v1/pairing-requests", func(w http.ResponseWriter, _ *http.Request) {
		_ = json.NewEncoder(w).Encode(protocol.SubmitPairingResponse{RequestID: "r", PollToken: "p"})
	})
	mux.HandleFunc("GET /api/v1/pairing-requests/{id}", func(w http.ResponseWriter, _ *http.Request) {
		_ = json.NewEncoder(w).Encode(protocol.PairingResultResponse{Status: protocol.PairingRequestRejected})
	})
	srv := httptest.NewTLSServer(mux)
	defer srv.Close()
	sum := sha256.Sum256(srv.Certificate().Raw)

	store, _ := credstore.Open(filepath.Join(t.TempDir(), "cfg"))
	_, err := run(context.Background(), store, Request{
		ServerURL: srv.URL, ServerFingerprint: hex.EncodeToString(sum[:]), Code: "123456",
		DeviceName: "Mac", Platform: protocol.PlatformDarwin,
	}, 10*time.Millisecond)
	if err != ErrRejected {
		t.Errorf("err = %v, want ErrRejected", err)
	}
}

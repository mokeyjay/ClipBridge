package httpapi

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/gorilla/websocket"
	"github.com/mokeyjay/clipbridge/server/internal/blobstore"
	"github.com/mokeyjay/clipbridge/server/internal/bootstrap"
	"github.com/mokeyjay/clipbridge/server/internal/security"
	"github.com/mokeyjay/clipbridge/server/internal/store"
	"github.com/mokeyjay/clipbridge/server/internal/wshub"
	"github.com/mokeyjay/clipbridge/shared/protocol"
)

// testEnv wires a Server to in-memory test servers for both ports.
type testEnv struct {
	st     *store.Store
	hub    *wshub.Hub
	web    *httptest.Server
	device *httptest.Server
	admin  *bootstrap.AdminCredentials
}

// newTestEnv builds a fully initialized server with a bootstrapped admin.
func newTestEnv(t *testing.T) *testEnv {
	t.Helper()
	db, err := store.Open(filepath.Join(t.TempDir(), "api.db"))
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	st := store.New(db)
	admin, err := bootstrap.InitializeIdentity(st)
	if err != nil {
		t.Fatalf("bootstrap: %v", err)
	}
	hub := wshub.New(st)
	blobDir := t.TempDir()
	for _, sub := range []string{"data/incoming", "data/ciphertext"} {
		if err := os.MkdirAll(filepath.Join(blobDir, sub), 0o700); err != nil {
			t.Fatalf("mkdir blob dir: %v", err)
		}
	}
	blobs := blobstore.New(blobDir)
	srv := New(st, hub, blobs, "AB:CD:EF", false /* secureCookies off for http tests */, nil)

	env := &testEnv{st: st, hub: hub, admin: admin}
	env.web = httptest.NewServer(srv.WebHandler())
	env.device = httptest.NewServer(srv.DeviceHandler())
	t.Cleanup(env.web.Close)
	t.Cleanup(env.device.Close)
	return env
}

// apiClient is a cookie-jar HTTP client that auto-attaches the CSRF header on
// mutating requests, mimicking the Web console.
type apiClient struct {
	t    *testing.T
	base string
	hc   *http.Client
}

// newClient builds a client against the given base URL.
func (e *testEnv) newClient(t *testing.T, base string) *apiClient {
	jar, _ := cookiejar.New(nil)
	return &apiClient{t: t, base: base, hc: &http.Client{Jar: jar}}
}

// do issues a request, marshaling body to JSON and echoing the CSRF cookie.
func (c *apiClient) do(method, path string, body any) *http.Response {
	c.t.Helper()
	var reader io.Reader
	if body != nil {
		raw, _ := json.Marshal(body)
		reader = bytes.NewReader(raw)
	}
	req, err := http.NewRequest(method, c.base+path, reader)
	if err != nil {
		c.t.Fatalf("new request: %v", err)
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	// Double-submit CSRF: copy cb_csrf cookie value into the header on mutations.
	if isMutating(method) {
		u, _ := url.Parse(c.base)
		for _, ck := range c.hc.Jar.Cookies(u) {
			if ck.Name == csrfCookieName {
				req.Header.Set(csrfHeaderName, ck.Value)
			}
		}
	}
	resp, err := c.hc.Do(req)
	if err != nil {
		c.t.Fatalf("do %s %s: %v", method, path, err)
	}
	return resp
}

// decode reads and unmarshals a JSON response body into dst.
func decodeBody(t *testing.T, resp *http.Response, dst any) {
	t.Helper()
	defer resp.Body.Close()
	if dst == nil {
		_, _ = io.Copy(io.Discard, resp.Body)
		return
	}
	if err := json.NewDecoder(resp.Body).Decode(dst); err != nil {
		t.Fatalf("decode body: %v", err)
	}
}

// TestAdminLoginAndCSRF covers login, me, wrong password and CSRF enforcement.
func TestAdminLoginAndCSRF(t *testing.T) {
	env := newTestEnv(t)
	c := env.newClient(t, env.web.URL)

	// Wrong password.
	resp := c.do(http.MethodPost, apiPrefix+"/auth/login", protocol.LoginRequest{Username: env.admin.Username, Password: "wrong"})
	if resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("wrong password status = %d, want 401", resp.StatusCode)
	}
	resp.Body.Close()

	// Correct login.
	resp = c.do(http.MethodPost, apiPrefix+"/auth/login", protocol.LoginRequest{Username: env.admin.Username, Password: env.admin.Password})
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("login status = %d, want 200", resp.StatusCode)
	}
	var me protocol.MeResponse
	decodeBody(t, resp, &me)
	if me.SubjectType != protocol.SubjectAdmin {
		t.Errorf("subject type = %q, want admin", me.SubjectType)
	}

	// /auth/me reflects the session.
	resp = c.do(http.MethodGet, apiPrefix+"/auth/me", nil)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("me status = %d", resp.StatusCode)
	}
	resp.Body.Close()

	// CSRF: a mutating request without the header must be rejected.
	rawReq, _ := http.NewRequest(http.MethodPatch, env.web.URL+apiPrefix+"/admin/profile", strings.NewReader(`{"username":"admin-new"}`))
	rawReq.Header.Set("Content-Type", "application/json")
	u, _ := url.Parse(env.web.URL)
	for _, ck := range c.hc.Jar.Cookies(u) {
		rawReq.AddCookie(ck) // include session + csrf cookies but NOT the header
	}
	rawResp, err := c.hc.Do(rawReq)
	if err != nil {
		t.Fatalf("raw patch: %v", err)
	}
	if rawResp.StatusCode != http.StatusForbidden {
		t.Errorf("CSRF-less mutation status = %d, want 403", rawResp.StatusCode)
	}
	rawResp.Body.Close()
}

// loginAdmin logs the client in as the bootstrapped admin.
func (e *testEnv) loginAdmin(t *testing.T, c *apiClient) {
	resp := c.do(http.MethodPost, apiPrefix+"/auth/login", protocol.LoginRequest{Username: e.admin.Username, Password: e.admin.Password})
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("admin login failed: %d", resp.StatusCode)
	}
	resp.Body.Close()
}

// TestUserManagementAndDisable covers admin-created users, user login and the
// effect of disabling a user on its session.
func TestUserManagementAndDisable(t *testing.T) {
	env := newTestEnv(t)
	adminC := env.newClient(t, env.web.URL)
	env.loginAdmin(t, adminC)

	// Create a user.
	resp := adminC.do(http.MethodPost, apiPrefix+"/admin/users", protocol.LoginRequest{Username: "alice", Password: "password123"})
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("create user status = %d, want 201", resp.StatusCode)
	}
	var created userView
	decodeBody(t, resp, &created)

	// User can log in.
	userC := env.newClient(t, env.web.URL)
	resp = userC.do(http.MethodPost, apiPrefix+"/auth/login", protocol.LoginRequest{Username: "alice", Password: "password123"})
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("user login status = %d, want 200", resp.StatusCode)
	}
	resp.Body.Close()

	// Admin disables the user.
	resp = adminC.do(http.MethodPatch, apiPrefix+"/admin/users/"+created.ID, map[string]string{"status": "disabled"})
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("disable status = %d", resp.StatusCode)
	}
	resp.Body.Close()

	// The disabled user's existing session is invalidated immediately (the
	// session row is deleted on disable), so further requests are rejected.
	resp = userC.do(http.MethodGet, apiPrefix+"/user/settings", nil)
	if resp.StatusCode != http.StatusUnauthorized && resp.StatusCode != http.StatusForbidden {
		t.Errorf("disabled user settings status = %d, want 401 or 403", resp.StatusCode)
	}
	resp.Body.Close()

	// And a fresh login is refused.
	resp = userC.do(http.MethodPost, apiPrefix+"/auth/login", protocol.LoginRequest{Username: "alice", Password: "password123"})
	if resp.StatusCode != http.StatusForbidden {
		t.Errorf("disabled user login status = %d, want 403", resp.StatusCode)
	}
	resp.Body.Close()
}

// TestPairingEndToEnd covers code creation, device submission across the device
// port, web confirmation, and one-time device-token claim.
func TestPairingEndToEnd(t *testing.T) {
	env := newTestEnv(t)
	adminC := env.newClient(t, env.web.URL)
	env.loginAdmin(t, adminC)
	resp := adminC.do(http.MethodPost, apiPrefix+"/admin/users", protocol.LoginRequest{Username: "alice", Password: "password123"})
	resp.Body.Close()

	userC := env.newClient(t, env.web.URL)
	resp = userC.do(http.MethodPost, apiPrefix+"/auth/login", protocol.LoginRequest{Username: "alice", Password: "password123"})
	resp.Body.Close()

	// User creates a pairing code.
	resp = userC.do(http.MethodPost, apiPrefix+"/pairing-codes", nil)
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("create code status = %d", resp.StatusCode)
	}
	var code protocol.PairingCodeResponse
	decodeBody(t, resp, &code)
	if len(code.Code) != 6 || code.ServerFingerprintSHA256 != "AB:CD:EF" {
		t.Fatalf("bad code response: %+v", code)
	}

	// Device submits a pairing request on the device port (no session).
	deviceC := env.newClient(t, env.device.URL)
	resp = deviceC.do(http.MethodPost, apiPrefix+"/pairing-requests", protocol.SubmitPairingRequest{
		Code: code.Code, DeviceName: "MacBook", Platform: "darwin", ClientVersion: "0.1.0", HPKEPublicKey: "cHVibGljLWtleQ",
	})
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("submit status = %d", resp.StatusCode)
	}
	var submit protocol.SubmitPairingResponse
	decodeBody(t, resp, &submit)

	// User sees the pending request.
	resp = userC.do(http.MethodGet, apiPrefix+"/pairing-codes/current", nil)
	var current currentPairingCodeView
	decodeBody(t, resp, &current)
	if len(current.PendingRequests) != 1 || current.PendingRequests[0].RequestID != submit.RequestID {
		t.Fatalf("pending requests = %+v", current.PendingRequests)
	}

	// Poll before confirmation: still pending, no token.
	pollResp := pollPairing(t, env, submit.RequestID, submit.PollToken)
	if pollResp.Status != protocol.PairingRequestPending || pollResp.DeviceToken != "" {
		t.Fatalf("pre-confirm poll = %+v", pollResp)
	}

	// User confirms.
	resp = userC.do(http.MethodPost, apiPrefix+"/pairing-requests/"+submit.RequestID+"/confirm", nil)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("confirm status = %d", resp.StatusCode)
	}
	resp.Body.Close()

	// First poll after confirm: token issued.
	pollResp = pollPairing(t, env, submit.RequestID, submit.PollToken)
	if pollResp.Status != protocol.PairingRequestConfirmed || pollResp.DeviceToken == "" || pollResp.Device == nil {
		t.Fatalf("confirm poll = %+v", pollResp)
	}
	deviceToken := pollResp.DeviceToken
	deviceID := pollResp.Device.ID

	// Second poll: claimed, no token re-issued.
	pollResp = pollPairing(t, env, submit.RequestID, submit.PollToken)
	if pollResp.DeviceToken != "" {
		t.Errorf("token re-issued on second poll: %+v", pollResp)
	}

	// The issued device token authenticates a WSS connection.
	assertDeviceWSOnline(t, env, deviceToken, deviceID)
}

// pollPairing issues an authorized poll and returns the parsed result.
func pollPairing(t *testing.T, env *testEnv, requestID, pollToken string) protocol.PairingResultResponse {
	t.Helper()
	req, _ := http.NewRequest(http.MethodGet, env.device.URL+apiPrefix+"/pairing-requests/"+requestID, nil)
	req.Header.Set("Authorization", "Pairing "+pollToken)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("poll: %v", err)
	}
	var out protocol.PairingResultResponse
	decodeBody(t, resp, &out)
	return out
}

// assertDeviceWSOnline dials the device WSS with the token and asserts the hub
// marks the device online; then disabling the device closes the connection.
func assertDeviceWSOnline(t *testing.T, env *testEnv, deviceToken, deviceID string) {
	t.Helper()
	wsURL := "ws" + strings.TrimPrefix(env.device.URL, "http") + apiPrefix + "/ws/device?protocol_version=1"
	header := http.Header{"Authorization": {"Bearer " + deviceToken}}
	conn, resp, err := websocket.DefaultDialer.Dial(wsURL, header)
	if err != nil {
		t.Fatalf("device ws dial: %v (status %v)", err, statusOf(resp))
	}
	defer conn.Close()

	// Online status should appear quickly after registration.
	if !waitFor(func() bool { return env.hub.IsDeviceOnline(deviceID) }, time.Second) {
		t.Fatal("device not marked online after WSS connect")
	}

	// Revoking via the hub (as a disable would) closes the socket.
	env.hub.CloseDevice(deviceID)
	_ = conn.SetReadDeadline(time.Now().Add(time.Second))
	if _, _, err := conn.ReadMessage(); err == nil {
		// A close frame or error is expected once the hub drops us.
		if _, _, err2 := conn.ReadMessage(); err2 == nil {
			t.Error("connection stayed open after CloseDevice")
		}
	}
}

// TestProtocolVersionNegotiation rejects an unsupported version pre-upgrade.
func TestProtocolVersionNegotiation(t *testing.T) {
	env := newTestEnv(t)
	// Mint a valid device with a known-plaintext token directly via the store.
	u, _ := env.st.CreateUser("bob", "h")
	code, _ := env.st.CreatePairingCode(u.ID, "ch", 300)
	req, _ := env.st.CreatePairingRequest(code, "Mac", protocol.PlatformDarwin, "0.1.0", "pk", "ph")
	device, _ := env.st.ConfirmPairingRequest(u.ID, req.ID)
	tokenPlain := "plain-device-token"
	if _, err := env.st.CreateDeviceToken(device.ID, security.TokenHash(tokenPlain)); err != nil {
		t.Fatalf("mint token: %v", err)
	}

	wsURL := "ws" + strings.TrimPrefix(env.device.URL, "http") + apiPrefix + "/ws/device?protocol_version=999"
	header := http.Header{"Authorization": {"Bearer " + tokenPlain}}
	_, resp, err := websocket.DefaultDialer.Dial(wsURL, header)
	if err == nil {
		t.Fatal("dial with unsupported protocol version unexpectedly succeeded")
	}
	if resp == nil || resp.StatusCode != http.StatusBadRequest {
		t.Errorf("unsupported version status = %v, want 400", statusOf(resp))
	}
}

// TestPairingRateLimit verifies the per-IP submission cap.
func TestPairingRateLimit(t *testing.T) {
	env := newTestEnv(t)
	deviceC := env.newClient(t, env.device.URL)
	body := protocol.SubmitPairingRequest{Code: "000000", DeviceName: "Mac", Platform: "darwin", HPKEPublicKey: "pk"}

	var got429 bool
	for i := 0; i < pairingRateMaxAttempts+1; i++ {
		resp := deviceC.do(http.MethodPost, apiPrefix+"/pairing-requests", body)
		if resp.StatusCode == http.StatusTooManyRequests {
			got429 = true
		}
		resp.Body.Close()
	}
	if !got429 {
		t.Errorf("expected a 429 within %d attempts", pairingRateMaxAttempts+1)
	}
}

// --- small test helpers ---

func statusOf(resp *http.Response) any {
	if resp == nil {
		return "<nil>"
	}
	return resp.StatusCode
}

func waitFor(cond func() bool, d time.Duration) bool {
	deadline := time.Now().Add(d)
	for time.Now().Before(deadline) {
		if cond() {
			return true
		}
		time.Sleep(5 * time.Millisecond)
	}
	return cond()
}

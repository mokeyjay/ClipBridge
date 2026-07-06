package httpapi

import (
	"bytes"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"io"
	"mime/multipart"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/gorilla/websocket"
	"github.com/mokeyjay/clipbridge/server/internal/security"
	"github.com/mokeyjay/clipbridge/shared/protocol"
)

// mintDevice creates an active device for userID and returns its id and a usable
// plaintext bearer token.
func (e *testEnv) mintDevice(t *testing.T, userID, name string) (deviceID, token string) {
	t.Helper()
	code, _ := e.st.CreatePairingCode(userID, "ch-"+name, 300)
	req, _ := e.st.CreatePairingRequest(code, name, protocol.PlatformDarwin, "0.1.0", "pk-"+name, "ph-"+name)
	device, err := e.st.ConfirmPairingRequest(userID, req.ID)
	if err != nil {
		t.Fatalf("confirm device %s: %v", name, err)
	}
	token = "token-" + name
	if _, err := e.st.CreateDeviceToken(device.ID, security.TokenHash(token)); err != nil {
		t.Fatalf("mint token %s: %v", name, err)
	}
	return device.ID, token
}

// dialDevice opens a device WSS connection and returns it plus a channel of the
// decoded events its read pump receives. The reader keeps the socket healthy.
func (e *testEnv) dialDevice(t *testing.T, token string) (*websocket.Conn, chan protocol.Event) {
	t.Helper()
	wsURL := "ws" + strings.TrimPrefix(e.device.URL, "http") + apiPrefix + "/ws/device?protocol_version=1"
	conn, _, err := websocket.DefaultDialer.Dial(wsURL, http.Header{"Authorization": {"Bearer " + token}})
	if err != nil {
		t.Fatalf("device ws dial: %v", err)
	}
	events := make(chan protocol.Event, 8)
	go func() {
		for {
			_, msg, err := conn.ReadMessage()
			if err != nil {
				close(events)
				return
			}
			var ev protocol.Event
			if json.Unmarshal(msg, &ev) == nil {
				events <- ev
			}
		}
	}()
	return conn, events
}

// buildUpload assembles the multipart body (manifest + ciphertext) for an upload.
func buildUpload(t *testing.T, manifest protocol.UploadManifest, ciphertext []byte) (string, *bytes.Buffer) {
	t.Helper()
	var buf bytes.Buffer
	mw := multipart.NewWriter(&buf)
	mf, _ := mw.CreateFormField("manifest")
	if err := json.NewEncoder(mf).Encode(manifest); err != nil {
		t.Fatalf("encode manifest: %v", err)
	}
	cf, _ := mw.CreateFormField("ciphertext")
	if _, err := cf.Write(ciphertext); err != nil {
		t.Fatalf("write ciphertext: %v", err)
	}
	_ = mw.Close()
	return mw.FormDataContentType(), &buf
}

// deviceUpload posts a multipart upload as a device and returns the response.
func (e *testEnv) deviceUpload(t *testing.T, token, contentType string, body *bytes.Buffer) *http.Response {
	t.Helper()
	req, _ := http.NewRequest(http.MethodPost, e.device.URL+apiPrefix+"/clipboard/items", body)
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Content-Type", contentType)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("upload: %v", err)
	}
	return resp
}

// deviceGET issues an authorized GET on the device port.
func (e *testEnv) deviceGET(t *testing.T, token, path string) *http.Response {
	t.Helper()
	req, _ := http.NewRequest(http.MethodGet, e.device.URL+path, nil)
	req.Header.Set("Authorization", "Bearer "+token)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("GET %s: %v", path, err)
	}
	return resp
}

// devicePOST issues an authorized POST (optional JSON body) on the device port.
func (e *testEnv) devicePOST(t *testing.T, token, path string, body any) *http.Response {
	t.Helper()
	var r io.Reader
	if body != nil {
		raw, _ := json.Marshal(body)
		r = bytes.NewReader(raw)
	}
	req, _ := http.NewRequest(http.MethodPost, e.device.URL+path, r)
	req.Header.Set("Authorization", "Bearer "+token)
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("POST %s: %v", path, err)
	}
	return resp
}

// devicePATCH issues an authorized PATCH with a JSON body on the device port.
func (e *testEnv) devicePATCH(t *testing.T, token, path string, body any) *http.Response {
	t.Helper()
	raw, _ := json.Marshal(body)
	req, _ := http.NewRequest(http.MethodPatch, e.device.URL+path, bytes.NewReader(raw))
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Content-Type", "application/json")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("PATCH %s: %v", path, err)
	}
	return resp
}

// TestClipboardRelayEndToEnd covers targets discovery, multipart upload, the WSS
// delivery notification, download with integrity, ack, completion and cleanup.
func TestClipboardRelayEndToEnd(t *testing.T) {
	env := newTestEnv(t)
	u, _ := env.st.CreateUser("alice", "h")
	srcID, srcTok := env.mintDevice(t, u.ID, "src")
	dstID, dstTok := env.mintDevice(t, u.ID, "dst")

	// Both devices connect so they count as online.
	srcConn, _ := env.dialDevice(t, srcTok)
	defer srcConn.Close()
	dstConn, dstEvents := env.dialDevice(t, dstTok)
	defer dstConn.Close()
	if !waitFor(func() bool { return env.hub.IsDeviceOnline(srcID) && env.hub.IsDeviceOnline(dstID) }, 2*time.Second) {
		t.Fatal("devices did not come online")
	}

	// Source discovers the target via /device/targets.
	resp := env.deviceGET(t, srcTok, apiPrefix+"/device/targets")
	var targets protocol.TargetsResponse
	decodeBody(t, resp, &targets)
	if len(targets.Targets) != 1 || targets.Targets[0].DeviceID != dstID {
		t.Fatalf("targets = %+v, want [%s]", targets.Targets, dstID)
	}

	// Build an opaque ciphertext (the relay is content-agnostic) and upload it.
	ciphertext := make([]byte, 4096)
	_, _ = rand.Read(ciphertext)
	sum := sha256.Sum256(ciphertext)
	itemID := uuid.NewString()
	manifest := protocol.UploadManifest{
		ProtocolVersion: protocol.ProtocolVersion, ItemID: itemID, ContentType: protocol.ContentText,
		CiphertextSizeBytes: int64(len(ciphertext)), CiphertextSHA256: hex.EncodeToString(sum[:]),
		ChunkSizeBytes: 65536, TotalChunks: 1,
		Deliveries: []protocol.DeliveryTarget{{TargetDeviceID: dstID, WrappedDEK: "d3JhcHBlZA"}},
	}
	ct, body := buildUpload(t, manifest, ciphertext)
	resp = env.deviceUpload(t, srcTok, ct, body)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("upload status = %d", resp.StatusCode)
	}
	var up protocol.UploadResponse
	decodeBody(t, resp, &up)
	if len(up.AcceptedTargetDeviceIDs) != 1 || up.AcceptedTargetDeviceIDs[0] != dstID {
		t.Fatalf("accepted = %+v", up.AcceptedTargetDeviceIDs)
	}

	// Target receives a delivery.created event over WSS.
	var deliveryID string
	select {
	case ev := <-dstEvents:
		if ev.Event != protocol.EventDeliveryCreated {
			t.Fatalf("event = %q, want delivery.created", ev.Event)
		}
		// Data is a generic map after JSON round-trip.
		if m, ok := ev.Data.(map[string]any); ok {
			deliveryID, _ = m["delivery_id"].(string)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("no delivery.created event received")
	}
	if deliveryID == "" {
		t.Fatal("event carried no delivery_id")
	}

	// Pending list shows the delivery with the target's wrapped DEK.
	resp = env.deviceGET(t, dstTok, apiPrefix+"/clipboard/deliveries/pending")
	var pending protocol.PendingDeliveriesResponse
	decodeBody(t, resp, &pending)
	if len(pending.Deliveries) != 1 || pending.Deliveries[0].DeliveryID != deliveryID {
		t.Fatalf("pending = %+v", pending.Deliveries)
	}
	if pending.Deliveries[0].WrappedDEK != "d3JhcHBlZA" || pending.Deliveries[0].SourceDeviceID != srcID {
		t.Errorf("delivery manifest mismatch: %+v", pending.Deliveries[0])
	}

	// Download the ciphertext and verify it byte-for-byte.
	resp = env.deviceGET(t, dstTok, apiPrefix+"/clipboard/deliveries/"+deliveryID+"/content")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("content status = %d", resp.StatusCode)
	}
	got, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if !bytes.Equal(got, ciphertext) {
		t.Error("downloaded ciphertext mismatch")
	}

	// Ack completes the item and triggers ciphertext deletion.
	resp = env.devicePOST(t, dstTok, apiPrefix+"/clipboard/deliveries/"+deliveryID+"/ack", nil)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("ack status = %d", resp.StatusCode)
	}
	resp.Body.Close()

	// Pending is now empty and the content is gone.
	resp = env.deviceGET(t, dstTok, apiPrefix+"/clipboard/deliveries/pending")
	var pending2 protocol.PendingDeliveriesResponse
	decodeBody(t, resp, &pending2)
	if len(pending2.Deliveries) != 0 {
		t.Errorf("pending after ack = %+v, want empty", pending2.Deliveries)
	}
	resp = env.deviceGET(t, dstTok, apiPrefix+"/clipboard/deliveries/"+deliveryID+"/content")
	if resp.StatusCode == http.StatusOK {
		t.Error("content still downloadable after ack")
	}
	resp.Body.Close()
}

// TestUploadNoOnlineTargets rejects an upload when no eligible target is online.
func TestUploadNoOnlineTargets(t *testing.T) {
	env := newTestEnv(t)
	u, _ := env.st.CreateUser("alice", "h")
	srcID, srcTok := env.mintDevice(t, u.ID, "src")
	dstID, _ := env.mintDevice(t, u.ID, "dst") // never connects → offline

	srcConn, _ := env.dialDevice(t, srcTok)
	defer srcConn.Close()
	if !waitFor(func() bool { return env.hub.IsDeviceOnline(srcID) }, 2*time.Second) {
		t.Fatal("source not online")
	}

	manifest := protocol.UploadManifest{
		ProtocolVersion: protocol.ProtocolVersion, ItemID: uuid.NewString(), ContentType: protocol.ContentText,
		CiphertextSizeBytes: 3, CiphertextSHA256: "00", ChunkSizeBytes: 65536, TotalChunks: 1,
		Deliveries: []protocol.DeliveryTarget{{TargetDeviceID: dstID, WrappedDEK: "x"}},
	}
	ct, body := buildUpload(t, manifest, []byte("abc"))
	resp := env.deviceUpload(t, srcTok, ct, body)
	if resp.StatusCode != http.StatusConflict {
		t.Fatalf("status = %d, want 409 NO_ONLINE_TARGETS", resp.StatusCode)
	}
	var errResp protocol.ErrorResponse
	decodeBody(t, resp, &errResp)
	if errResp.Error.Code != protocol.ErrNoOnlineTargets {
		t.Errorf("code = %q, want NO_ONLINE_TARGETS", errResp.Error.Code)
	}
}

// TestUploadIntegrityFailure rejects a ciphertext whose hash/size disagree with
// the manifest.
func TestUploadIntegrityFailure(t *testing.T) {
	env := newTestEnv(t)
	u, _ := env.st.CreateUser("alice", "h")
	srcID, srcTok := env.mintDevice(t, u.ID, "src")
	dstID, dstTok := env.mintDevice(t, u.ID, "dst")

	srcConn, _ := env.dialDevice(t, srcTok)
	defer srcConn.Close()
	dstConn, _ := env.dialDevice(t, dstTok)
	defer dstConn.Close()
	if !waitFor(func() bool { return env.hub.IsDeviceOnline(srcID) && env.hub.IsDeviceOnline(dstID) }, 2*time.Second) {
		t.Fatal("devices not online")
	}

	manifest := protocol.UploadManifest{
		ProtocolVersion: protocol.ProtocolVersion, ItemID: uuid.NewString(), ContentType: protocol.ContentText,
		CiphertextSizeBytes: 3, CiphertextSHA256: "deadbeef", ChunkSizeBytes: 65536, TotalChunks: 1,
		Deliveries: []protocol.DeliveryTarget{{TargetDeviceID: dstID, WrappedDEK: "x"}},
	}
	ct, body := buildUpload(t, manifest, []byte("abc")) // real sha != deadbeef
	resp := env.deviceUpload(t, srcTok, ct, body)
	var errResp protocol.ErrorResponse
	decodeBody(t, resp, &errResp)
	if errResp.Error.Code != protocol.ErrCiphertextIntegrityFailed {
		t.Errorf("code = %q, want CIPHERTEXT_INTEGRITY_FAILED", errResp.Error.Code)
	}
}

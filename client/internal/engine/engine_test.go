package engine

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"io"
	"mime"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"

	"github.com/mokeyjay/clipbridge/client/internal/apiclient"
	"github.com/mokeyjay/clipbridge/client/internal/e2ee"
	"github.com/mokeyjay/clipbridge/shared/protocol"
)

// fakeClipboard is an in-memory Clipboard for tests.
type fakeClipboard struct {
	mu         sync.Mutex
	text       string
	ok         bool
	image      []byte
	richFormat string
	rich       []byte
	richPlain  string
	filePath   string
}

func (c *fakeClipboard) ReadText() (string, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.text, c.ok
}
func (c *fakeClipboard) WriteText(t string) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.text, c.ok = t, true
	return nil
}
func (c *fakeClipboard) WriteImage(png []byte) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.image = append([]byte(nil), png...)
	return nil
}
func (c *fakeClipboard) WriteRichText(format string, rich []byte, plain string) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.richFormat, c.rich, c.richPlain = format, append([]byte(nil), rich...), plain
	return nil
}
func (c *fakeClipboard) WriteFile(path string) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.filePath = path
	return nil
}
func (c *fakeClipboard) readImage() []byte {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.image
}
func (c *fakeClipboard) readRich() (string, []byte, string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.richFormat, c.rich, c.richPlain
}
func (c *fakeClipboard) readFilePath() string {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.filePath
}

// captured holds what the fake server received from an upload.
type captured struct {
	manifest   protocol.UploadManifest
	ciphertext []byte
}

// fakeServer implements the subset of the device API the engine uses, over TLS.
type fakeServer struct {
	srv         *httptest.Server
	mu          sync.Mutex
	targets     []protocol.TargetDevice
	uploads     []captured
	pending     []protocol.DeliveryManifest
	contents    map[string][]byte // deliveryID -> ciphertext
	acked       []string
	rejected    map[string]protocol.RejectReason
	uploadCount int
	eff         *protocol.EffectiveConfig // effective config served to the engine
}

// newFakeServer starts a TLS device-API stub.
func newFakeServer(t *testing.T) *fakeServer {
	t.Helper()
	f := &fakeServer{contents: map[string][]byte{}, rejected: map[string]protocol.RejectReason{}}
	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/v1/device/targets", func(w http.ResponseWriter, _ *http.Request) {
		f.mu.Lock()
		defer f.mu.Unlock()
		writeJSON(w, protocol.TargetsResponse{Targets: f.targets})
	})
	mux.HandleFunc("GET /api/v1/device/effective-config", func(w http.ResponseWriter, _ *http.Request) {
		f.mu.Lock()
		eff := f.eff
		f.mu.Unlock()
		if eff == nil {
			// Permissive default: all types, generous limits.
			eff = &protocol.EffectiveConfig{
				MaxSyncSizeBytes:         100 << 20,
				AllowedTypes:             []protocol.ContentType{protocol.ContentText, protocol.ContentImage, protocol.ContentFile, protocol.ContentRichText},
				MaxAutoUploadSizeBytes:   100 << 20,
				MaxAutoDownloadSizeBytes: 100 << 20,
			}
		}
		writeJSON(w, *eff)
	})
	mux.HandleFunc("POST /api/v1/clipboard/items", func(w http.ResponseWriter, r *http.Request) {
		mediaType, params, _ := mime.ParseMediaType(r.Header.Get("Content-Type"))
		if mediaType != "multipart/form-data" {
			w.WriteHeader(400)
			return
		}
		mr := multipart.NewReader(r.Body, params["boundary"])
		var cap captured
		for {
			part, err := mr.NextPart()
			if err != nil {
				break
			}
			if part.FormName() == "manifest" {
				_ = json.NewDecoder(part).Decode(&cap.manifest)
			} else if part.FormName() == "ciphertext" {
				cap.ciphertext, _ = io.ReadAll(part)
			}
		}
		f.mu.Lock()
		f.uploads = append(f.uploads, cap)
		f.uploadCount++
		ids := make([]string, len(cap.manifest.Deliveries))
		for i, d := range cap.manifest.Deliveries {
			ids[i] = d.TargetDeviceID
		}
		f.mu.Unlock()
		writeJSON(w, protocol.UploadResponse{ItemID: cap.manifest.ItemID, ExpiresAt: "", AcceptedTargetDeviceIDs: ids})
	})
	mux.HandleFunc("GET /api/v1/clipboard/deliveries/pending", func(w http.ResponseWriter, _ *http.Request) {
		f.mu.Lock()
		defer f.mu.Unlock()
		writeJSON(w, protocol.PendingDeliveriesResponse{Deliveries: f.pending})
	})
	mux.HandleFunc("GET /api/v1/clipboard/deliveries/{id}", func(w http.ResponseWriter, r *http.Request) {
		id := r.PathValue("id")
		f.mu.Lock()
		defer f.mu.Unlock()
		for _, d := range f.pending {
			if d.DeliveryID == id {
				writeJSON(w, d)
				return
			}
		}
		w.WriteHeader(404)
	})
	mux.HandleFunc("GET /api/v1/clipboard/deliveries/{id}/content", func(w http.ResponseWriter, r *http.Request) {
		f.mu.Lock()
		ct := f.contents[r.PathValue("id")]
		f.mu.Unlock()
		w.Header().Set("Content-Type", "application/octet-stream")
		_, _ = w.Write(ct)
	})
	mux.HandleFunc("POST /api/v1/clipboard/deliveries/{id}/ack", func(w http.ResponseWriter, r *http.Request) {
		f.mu.Lock()
		f.acked = append(f.acked, r.PathValue("id"))
		f.mu.Unlock()
		writeJSON(w, map[string]bool{"ok": true})
	})
	mux.HandleFunc("POST /api/v1/clipboard/deliveries/{id}/reject", func(w http.ResponseWriter, r *http.Request) {
		var req protocol.RejectRequest
		_ = json.NewDecoder(r.Body).Decode(&req)
		f.mu.Lock()
		f.rejected[r.PathValue("id")] = req.Reason
		f.mu.Unlock()
		writeJSON(w, map[string]bool{"ok": true})
	})

	f.srv = httptest.NewTLSServer(mux)
	t.Cleanup(f.srv.Close)
	return f
}

// fingerprint returns the server cert SHA-256 as plain hex (apiclient normalizes).
func (f *fakeServer) fingerprint() string {
	sum := sha256.Sum256(f.srv.Certificate().Raw)
	return hex.EncodeToString(sum[:])
}

// client builds a pinned apiclient for this server with the given token.
func (f *fakeServer) client(token string) *apiclient.Client {
	c := apiclient.New(f.srv.URL, f.fingerprint())
	c.SetToken(token)
	return c
}

func writeJSON(w http.ResponseWriter, v any) {
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(v)
}

// TestPublishProducesDecryptableEnvelope verifies the engine uploads a single
// ciphertext + per-target wrapped DEK that the target can actually decrypt.
func TestPublishProducesDecryptableEnvelope(t *testing.T) {
	f := newFakeServer(t)
	// The one online target with its real keypair.
	tgtPriv, tgtPub, err := e2ee.GenerateDeviceKey()
	if err != nil {
		t.Fatalf("gen target key: %v", err)
	}
	f.targets = []protocol.TargetDevice{{DeviceID: "target", HPKEPublicKey: e2ee.PublicKeyBase64(tgtPub)}}

	srcPriv, _, _ := e2ee.GenerateDeviceKey()
	eng := New(Identity{DeviceID: "src", UserID: "u", PrivateKey: srcPriv}, f.client("tok"), &fakeClipboard{})

	const msg = "hello clipbridge 你好"
	if err := eng.PublishText(context.Background(), msg); err != nil {
		t.Fatalf("publish: %v", err)
	}

	if len(f.uploads) != 1 {
		t.Fatalf("uploads = %d, want 1", len(f.uploads))
	}
	up := f.uploads[0]
	if len(up.manifest.Deliveries) != 1 || up.manifest.Deliveries[0].TargetDeviceID != "target" {
		t.Fatalf("deliveries = %+v", up.manifest.Deliveries)
	}

	// Act as the target: unwrap the DEK and decrypt the body.
	wrapped, err := base64.StdEncoding.DecodeString(up.manifest.Deliveries[0].WrappedDEK)
	if err != nil {
		t.Fatalf("decode wrapped: %v", err)
	}
	hdr := e2ee.Header{ProtocolVersion: up.manifest.ProtocolVersion, ItemID: up.manifest.ItemID, SourceDeviceID: "src", ContentType: protocol.ContentText}
	dek, err := e2ee.UnwrapDEK(tgtPriv, wrapped, hdr, "target")
	if err != nil {
		t.Fatalf("target unwrap: %v", err)
	}
	var out bytes.Buffer
	if err := e2ee.DecryptStream(&out, bytes.NewReader(up.ciphertext), dek, hdr, up.manifest.ChunkSizeBytes, up.manifest.TotalChunks); err != nil {
		t.Fatalf("target decrypt: %v", err)
	}
	if out.String() != msg {
		t.Errorf("decrypted = %q, want %q", out.String(), msg)
	}
}

// TestHandleDeliveryAndLoopback verifies inbound decryption + write-back, then
// that the written text is suppressed from re-upload (loop-back guard).
func TestHandleDeliveryAndLoopback(t *testing.T) {
	f := newFakeServer(t)
	myPriv, myPub, _ := e2ee.GenerateDeviceKey()
	clip := &fakeClipboard{}
	eng := New(Identity{DeviceID: "me", UserID: "u", PrivateKey: myPriv}, f.client("tok"), clip)

	// A remote source encrypts a message addressed to "me".
	const msg = "from remote"
	dek, _ := e2ee.NewDEK()
	hdr := e2ee.Header{ProtocolVersion: protocol.ProtocolVersion, ItemID: "item-1", SourceDeviceID: "remote", ContentType: protocol.ContentText}
	var ct bytes.Buffer
	res, _ := e2ee.EncryptStream(&ct, bytes.NewReader([]byte(msg)), dek, hdr, e2ee.DefaultChunkSize)
	wrapped, _ := e2ee.WrapDEK(myPub, dek, hdr, "me")

	f.pending = []protocol.DeliveryManifest{{
		DeliveryID: "del-1", ItemID: "item-1", SourceDeviceID: "remote", ContentType: protocol.ContentText,
		CiphertextSizeBytes: res.CiphertextSizeBytes, CiphertextSHA256: res.CiphertextSHA256,
		ChunkSizeBytes: res.ChunkSizeBytes, TotalChunks: res.TotalChunks,
		WrappedDEK: base64.StdEncoding.EncodeToString(wrapped),
	}}
	f.contents["del-1"] = ct.Bytes()

	if err := eng.SyncPending(context.Background()); err != nil {
		t.Fatalf("sync pending: %v", err)
	}
	if got, _ := clip.ReadText(); got != msg {
		t.Errorf("clipboard = %q, want %q", got, msg)
	}
	if len(f.acked) != 1 || f.acked[0] != "del-1" {
		t.Errorf("acked = %v, want [del-1]", f.acked)
	}

	// The clipboard now holds the just-written text; a monitor tick must NOT
	// re-upload it (loop-back suppression).
	f.targets = []protocol.TargetDevice{} // even if asked, no targets
	before := f.uploadCount
	if err := eng.OnClipboardChanged(context.Background(), msg); err != nil {
		t.Fatalf("onchange: %v", err)
	}
	if f.uploadCount != before {
		t.Error("loop-back text was re-uploaded")
	}
}

// TestTOFUBlocksChangedKey verifies a target whose key fingerprint changes after
// first trust is blocked from receiving further content.
func TestTOFUBlocksChangedKey(t *testing.T) {
	f := newFakeServer(t)
	_, pub1, _ := e2ee.GenerateDeviceKey()
	f.targets = []protocol.TargetDevice{{DeviceID: "target", HPKEPublicKey: e2ee.PublicKeyBase64(pub1)}}

	srcPriv, _, _ := e2ee.GenerateDeviceKey()
	eng := New(Identity{DeviceID: "src", PrivateKey: srcPriv}, f.client("tok"), &fakeClipboard{})

	// First publish trusts the key (TOFU) and uploads.
	if err := eng.PublishText(context.Background(), "one"); err != nil {
		t.Fatalf("publish 1: %v", err)
	}
	if f.uploadCount != 1 {
		t.Fatalf("expected 1 upload, got %d", f.uploadCount)
	}

	// The server now returns a DIFFERENT key for the same device id.
	_, pub2, _ := e2ee.GenerateDeviceKey()
	f.mu.Lock()
	f.targets = []protocol.TargetDevice{{DeviceID: "target", HPKEPublicKey: e2ee.PublicKeyBase64(pub2)}}
	f.mu.Unlock()

	// Second publish must block the mismatched target → no upload.
	if err := eng.PublishText(context.Background(), "two"); err != nil {
		t.Fatalf("publish 2: %v", err)
	}
	if f.uploadCount != 1 {
		t.Errorf("upload happened despite key mismatch (count=%d)", f.uploadCount)
	}
}

// TestTOFUPersistAndTrustPeer 覆盖 TOFU 持久化与手动信任：首次信任触发持久化
// 回调；换钥后登记失配并阻断；TrustPeer 采纳新指纹、清除告警并再次持久化，
// 之后同步恢复；SeedTOFU 预置的缓存同样能触发失配阻断。
func TestTOFUPersistAndTrustPeer(t *testing.T) {
	f := newFakeServer(t)
	_, pub1, _ := e2ee.GenerateDeviceKey()
	f.targets = []protocol.TargetDevice{{DeviceID: "target", DeviceName: "MacBook", HPKEPublicKey: e2ee.PublicKeyBase64(pub1)}}

	srcPriv, _, _ := e2ee.GenerateDeviceKey()
	eng := New(Identity{DeviceID: "src", PrivateKey: srcPriv}, f.client("tok"), &fakeClipboard{})

	// 记录每次持久化回调的快照。
	var persisted []map[string]string
	eng.SetTOFUPersist(func(m map[string]string) { persisted = append(persisted, m) })

	// 首次发布：TOFU 信任 + 一次持久化。
	if err := eng.PublishText(context.Background(), "one"); err != nil {
		t.Fatalf("publish 1: %v", err)
	}
	if len(persisted) != 1 || persisted[0]["target"] == "" {
		t.Fatalf("首次信任未持久化: %+v", persisted)
	}

	// 服务端换钥：阻断并登记失配（带设备名与新旧指纹）。
	_, pub2, _ := e2ee.GenerateDeviceKey()
	f.mu.Lock()
	f.targets = []protocol.TargetDevice{{DeviceID: "target", DeviceName: "MacBook", HPKEPublicKey: e2ee.PublicKeyBase64(pub2)}}
	f.mu.Unlock()
	if err := eng.PublishText(context.Background(), "two"); err != nil {
		t.Fatalf("publish 2: %v", err)
	}
	if f.uploadCount != 1 {
		t.Fatalf("换钥后仍上传 (count=%d)", f.uploadCount)
	}
	mms := eng.PeerMismatches()
	if len(mms) != 1 || mms[0].DeviceID != "target" || mms[0].DeviceName != "MacBook" ||
		mms[0].TrustedFP == "" || mms[0].NewFP == "" || mms[0].TrustedFP == mms[0].NewFP {
		t.Fatalf("失配记录错误: %+v", mms)
	}

	// 未登记失配的设备不可信任。
	if eng.TrustPeer("nobody") {
		t.Error("TrustPeer 对未知设备不应成功")
	}

	// 用户互验后信任新指纹：告警清除、缓存更新并再次持久化，同步恢复。
	if !eng.TrustPeer("target") {
		t.Fatal("TrustPeer 失败")
	}
	if len(eng.PeerMismatches()) != 0 {
		t.Error("信任后告警未清除")
	}
	if got := persisted[len(persisted)-1]["target"]; got != mms[0].NewFP {
		t.Errorf("持久化的新指纹 = %q, want %q", got, mms[0].NewFP)
	}
	if err := eng.PublishText(context.Background(), "three"); err != nil {
		t.Fatalf("publish 3: %v", err)
	}
	if f.uploadCount != 2 {
		t.Errorf("信任新指纹后同步未恢复 (count=%d)", f.uploadCount)
	}

	// SeedTOFU：新引擎预置旧指纹缓存后，遇到不同的当前密钥同样阻断。
	eng2 := New(Identity{DeviceID: "src2", PrivateKey: srcPriv}, f.client("tok"), &fakeClipboard{})
	eng2.SeedTOFU(map[string]string{"target": mms[0].TrustedFP}) // 预置旧指纹
	if err := eng2.PublishText(context.Background(), "four"); err != nil {
		t.Fatalf("publish 4: %v", err)
	}
	if f.uploadCount != 2 {
		t.Errorf("预置缓存未生效，失配仍上传 (count=%d)", f.uploadCount)
	}
	if len(eng2.PeerMismatches()) != 1 {
		t.Errorf("预置缓存失配未登记告警")
	}
}

// TestPinningRejectsWrongFingerprint verifies a mismatched pin blocks the call.
func TestPinningRejectsWrongFingerprint(t *testing.T) {
	f := newFakeServer(t)
	bad := apiclient.New(f.srv.URL, "00:00:00:00")
	bad.SetToken("tok")
	if _, err := bad.GetTargets(context.Background()); err == nil {
		t.Error("call with wrong pinned fingerprint unexpectedly succeeded")
	}
}

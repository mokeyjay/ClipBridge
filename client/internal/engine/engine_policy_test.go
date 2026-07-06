package engine

import (
	"bytes"
	"context"
	"encoding/base64"
	"testing"

	"github.com/mokeyjay/clipbridge/client/internal/e2ee"
	"github.com/mokeyjay/clipbridge/shared/protocol"
)

// makeDelivery builds a pending delivery on the fake server addressed to myPub,
// returning the delivery id.
func makeDelivery(t *testing.T, f *fakeServer, myPub []byte, id, text string, size int64) string {
	t.Helper()
	dek, _ := e2ee.NewDEK()
	hdr := e2ee.Header{ProtocolVersion: protocol.ProtocolVersion, ItemID: "item-" + id, SourceDeviceID: "remote", ContentType: protocol.ContentText}
	var ct bytes.Buffer
	res, _ := e2ee.EncryptStream(&ct, bytes.NewReader([]byte(text)), dek, hdr, e2ee.DefaultChunkSize)
	wrapped, _ := e2ee.WrapDEK(myPub, dek, hdr, "me")
	if size == 0 {
		size = res.CiphertextSizeBytes
	}
	f.mu.Lock()
	f.pending = []protocol.DeliveryManifest{{
		DeliveryID: id, ItemID: "item-" + id, SourceDeviceID: "remote", ContentType: protocol.ContentText,
		CiphertextSizeBytes: size, CiphertextSHA256: res.CiphertextSHA256,
		ChunkSizeBytes: res.ChunkSizeBytes, TotalChunks: res.TotalChunks,
		WrappedDEK: base64.StdEncoding.EncodeToString(wrapped),
	}}
	f.contents[id] = ct.Bytes()
	f.mu.Unlock()
	return id
}

// TestDirectionDownloadOnlySkipsUpload verifies upload-side direction control.
func TestDirectionDownloadOnlySkipsUpload(t *testing.T) {
	f := newFakeServer(t)
	_, pub, _ := e2ee.GenerateDeviceKey()
	f.targets = []protocol.TargetDevice{{DeviceID: "target", HPKEPublicKey: e2ee.PublicKeyBase64(pub)}}
	srcPriv, _, _ := e2ee.GenerateDeviceKey()
	eng := New(Identity{DeviceID: "src", PrivateKey: srcPriv}, f.client("tok"), &fakeClipboard{})
	eng.SetDirection(protocol.DirectionDownloadOnly)

	if err := eng.PublishText(context.Background(), "hi"); err != nil {
		t.Fatalf("publish: %v", err)
	}
	if f.uploadCount != 0 {
		t.Errorf("download-only mode uploaded (count=%d)", f.uploadCount)
	}
}

// TestDirectionUploadOnlyRejectsDelivery verifies download-side direction control.
func TestDirectionUploadOnlyRejectsDelivery(t *testing.T) {
	f := newFakeServer(t)
	myPriv, myPub, _ := e2ee.GenerateDeviceKey()
	clip := &fakeClipboard{}
	eng := New(Identity{DeviceID: "me", PrivateKey: myPriv}, f.client("tok"), clip)
	eng.SetDirection(protocol.DirectionUploadOnly)

	makeDelivery(t, f, myPub, "del-1", "from remote", 0)
	if err := eng.SyncPending(context.Background()); err != nil {
		t.Fatalf("sync: %v", err)
	}
	if _, ok := clip.ReadText(); ok {
		t.Error("upload-only mode wrote inbound content to clipboard")
	}
	if f.rejected["del-1"] != protocol.RejectPolicyBlocked {
		t.Errorf("delivery not rejected with POLICY_BLOCKED: %v", f.rejected["del-1"])
	}
}

// TestRefreshConfigTypeGate verifies a refreshed policy that disallows text blocks
// the upload before contacting the server for targets.
func TestRefreshConfigTypeGate(t *testing.T) {
	f := newFakeServer(t)
	f.eff = &protocol.EffectiveConfig{
		MaxSyncSizeBytes:         100 << 20,
		AllowedTypes:             []protocol.ContentType{protocol.ContentImage}, // text not allowed
		MaxAutoUploadSizeBytes:   100 << 20,
		MaxAutoDownloadSizeBytes: 100 << 20,
	}
	srcPriv, _, _ := e2ee.GenerateDeviceKey()
	eng := New(Identity{DeviceID: "src", PrivateKey: srcPriv}, f.client("tok"), &fakeClipboard{})
	if err := eng.RefreshConfig(context.Background()); err != nil {
		t.Fatalf("refresh: %v", err)
	}
	if err := eng.PublishText(context.Background(), "hi"); err != nil {
		t.Fatalf("publish: %v", err)
	}
	if f.uploadCount != 0 {
		t.Errorf("upload happened despite text being disallowed (count=%d)", f.uploadCount)
	}
}

// TestAutoUploadThresholdSkips verifies content over the auto-upload threshold is
// not auto-sent (awaits confirmation).
func TestAutoUploadThresholdSkips(t *testing.T) {
	f := newFakeServer(t)
	_, pub, _ := e2ee.GenerateDeviceKey()
	f.targets = []protocol.TargetDevice{{DeviceID: "target", HPKEPublicKey: e2ee.PublicKeyBase64(pub)}}
	f.eff = &protocol.EffectiveConfig{
		MaxSyncSizeBytes:         100 << 20,
		AllowedTypes:             []protocol.ContentType{protocol.ContentText},
		MaxAutoUploadSizeBytes:   8, // tiny threshold
		MaxAutoDownloadSizeBytes: 100 << 20,
	}
	srcPriv, _, _ := e2ee.GenerateDeviceKey()
	eng := New(Identity{DeviceID: "src", PrivateKey: srcPriv}, f.client("tok"), &fakeClipboard{})
	_ = eng.RefreshConfig(context.Background())

	if err := eng.PublishText(context.Background(), "this text is well over eight bytes"); err != nil {
		t.Fatalf("publish: %v", err)
	}
	if f.uploadCount != 0 {
		t.Errorf("over-threshold content was auto-uploaded (count=%d)", f.uploadCount)
	}
}

// TestAutoDownloadThresholdDefers verifies oversized inbound content is left
// pending (not acked, not rejected) awaiting confirmation.
func TestAutoDownloadThresholdDefers(t *testing.T) {
	f := newFakeServer(t)
	myPriv, myPub, _ := e2ee.GenerateDeviceKey()
	clip := &fakeClipboard{}
	eng := New(Identity{DeviceID: "me", PrivateKey: myPriv}, f.client("tok"), clip)
	f.eff = &protocol.EffectiveConfig{
		MaxSyncSizeBytes:         100 << 20,
		AllowedTypes:             []protocol.ContentType{protocol.ContentText},
		MaxAutoUploadSizeBytes:   100 << 20,
		MaxAutoDownloadSizeBytes: 4, // tiny threshold
	}
	_ = eng.RefreshConfig(context.Background())

	makeDelivery(t, f, myPub, "del-big", "way more than four bytes", 0)
	if err := eng.SyncPending(context.Background()); err != nil {
		t.Fatalf("sync: %v", err)
	}
	if _, ok := clip.ReadText(); ok {
		t.Error("over-threshold inbound content was auto-written")
	}
	if len(f.acked) != 0 || len(f.rejected) != 0 {
		t.Errorf("deferred delivery should be neither acked nor rejected: acked=%v rejected=%v", f.acked, f.rejected)
	}
}

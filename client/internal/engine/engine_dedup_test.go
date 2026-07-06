package engine

import (
	"bytes"
	"context"
	"encoding/base64"
	"image"
	"image/color"
	"image/png"
	"testing"

	"github.com/mokeyjay/clipbridge/client/internal/e2ee"
	"github.com/mokeyjay/clipbridge/shared/protocol"
)

// drawTestImage builds a small deterministic RGBA image for round-trip tests.
func drawTestImage() *image.RGBA {
	img := image.NewRGBA(image.Rect(0, 0, 32, 24))
	for y := 0; y < 24; y++ {
		for x := 0; x < 32; x++ {
			img.SetRGBA(x, y, color.RGBA{R: uint8(x * 7), G: uint8(y * 11), B: uint8(x + y), A: 255})
		}
	}
	return img
}

// TestImageLoopbackSurvivesReencode is the regression for the image dedup bug: a
// received image, once written back, must not be re-uploaded even though the OS
// hands the clipboard monitor re-encoded bytes (same pixels, different PNG bytes).
func TestImageLoopbackSurvivesReencode(t *testing.T) {
	f := newFakeServer(t)
	myPriv, myPub, _ := e2ee.GenerateDeviceKey()
	clip := &fakeClipboard{}
	eng := New(Identity{DeviceID: "me", PrivateKey: myPriv}, f.client("tok"), clip)

	src := drawTestImage()
	var aBuf, bBuf bytes.Buffer
	if err := (&png.Encoder{CompressionLevel: png.NoCompression}).Encode(&aBuf, src); err != nil {
		t.Fatalf("encode A: %v", err)
	}
	if err := (&png.Encoder{CompressionLevel: png.BestCompression}).Encode(&bBuf, src); err != nil {
		t.Fatalf("encode B: %v", err)
	}
	pngA, pngB := aBuf.Bytes(), bBuf.Bytes()
	if bytes.Equal(pngA, pngB) {
		t.Fatal("test setup: two encodings unexpectedly identical")
	}

	// Deliver pngA inbound so write-back remembers its pixel fingerprint.
	dek, _ := e2ee.NewDEK()
	hdr := e2ee.Header{ProtocolVersion: protocol.ProtocolVersion, ItemID: "i", SourceDeviceID: "remote", ContentType: protocol.ContentImage}
	var ct bytes.Buffer
	res, _ := e2ee.EncryptStream(&ct, bytes.NewReader(pngA), dek, hdr, e2ee.DefaultChunkSize)
	wrapped, _ := e2ee.WrapDEK(myPub, dek, hdr, "me")
	f.pending = []protocol.DeliveryManifest{{
		DeliveryID: "d", ItemID: "i", SourceDeviceID: "remote", ContentType: protocol.ContentImage,
		CiphertextSizeBytes: res.CiphertextSizeBytes, CiphertextSHA256: res.CiphertextSHA256,
		ChunkSizeBytes: res.ChunkSizeBytes, TotalChunks: res.TotalChunks,
		WrappedDEK: base64.StdEncoding.EncodeToString(wrapped),
	}}
	f.contents["d"] = ct.Bytes()
	if err := eng.SyncPending(context.Background()); err != nil {
		t.Fatalf("sync: %v", err)
	}

	before := f.uploadCount
	// The clipboard monitor sees the re-encoded bytes; loop-back must still suppress.
	if err := eng.OnImageChanged(context.Background(), pngB); err != nil {
		t.Fatalf("onimage: %v", err)
	}
	if f.uploadCount != before {
		t.Error("re-encoded write-back image was re-uploaded (pixel loop-back failed)")
	}
}

// TestConfirmUploadSendsDeferred verifies an over-threshold upload is held and only
// sent once confirmed.
func TestConfirmUploadSendsDeferred(t *testing.T) {
	f := newFakeServer(t)
	_, pub, _ := e2ee.GenerateDeviceKey()
	f.targets = []protocol.TargetDevice{{DeviceID: "target", HPKEPublicKey: e2ee.PublicKeyBase64(pub)}}
	f.eff = &protocol.EffectiveConfig{
		MaxSyncSizeBytes: 100 << 20, AllowedTypes: []protocol.ContentType{protocol.ContentText},
		MaxAutoUploadSizeBytes: 8, MaxAutoDownloadSizeBytes: 100 << 20,
	}
	srcPriv, _, _ := e2ee.GenerateDeviceKey()
	eng := New(Identity{DeviceID: "src", PrivateKey: srcPriv}, f.client("tok"), &fakeClipboard{})
	_ = eng.RefreshConfig(context.Background())

	var req ConfirmRequest
	eng.SetConfirmHook(func(c ConfirmRequest) { req = c })
	if err := eng.PublishText(context.Background(), "this text is well over eight bytes"); err != nil {
		t.Fatalf("publish: %v", err)
	}
	if f.uploadCount != 0 {
		t.Fatalf("over-threshold content was auto-uploaded (count=%d)", f.uploadCount)
	}
	if req.ID == "" || req.Kind != "upload" {
		t.Fatalf("confirm hook not called correctly: %+v", req)
	}
	if err := eng.ConfirmUpload(context.Background(), req.ID); err != nil {
		t.Fatalf("confirm: %v", err)
	}
	if f.uploadCount != 1 {
		t.Errorf("confirmed upload not sent (count=%d)", f.uploadCount)
	}
}

// TestConfirmDownloadPullsDeferred verifies a deferred over-threshold delivery is
// written back only after confirmation.
func TestConfirmDownloadPullsDeferred(t *testing.T) {
	f := newFakeServer(t)
	myPriv, myPub, _ := e2ee.GenerateDeviceKey()
	clip := &fakeClipboard{}
	eng := New(Identity{DeviceID: "me", PrivateKey: myPriv}, f.client("tok"), clip)
	f.eff = &protocol.EffectiveConfig{
		MaxSyncSizeBytes: 100 << 20, AllowedTypes: []protocol.ContentType{protocol.ContentText},
		MaxAutoUploadSizeBytes: 100 << 20, MaxAutoDownloadSizeBytes: 4,
	}
	_ = eng.RefreshConfig(context.Background())

	var req ConfirmRequest
	eng.SetConfirmHook(func(c ConfirmRequest) { req = c })
	makeDelivery(t, f, myPub, "del-big", "way more than four bytes", 0)
	if err := eng.SyncPending(context.Background()); err != nil {
		t.Fatalf("sync: %v", err)
	}
	if _, ok := clip.ReadText(); ok {
		t.Error("over-threshold content auto-written before confirmation")
	}
	if req.ID != "del-big" || req.Kind != "download" {
		t.Fatalf("confirm hook not called correctly: %+v", req)
	}
	if err := eng.ConfirmDownload(context.Background(), "del-big"); err != nil {
		t.Fatalf("confirm download: %v", err)
	}
	if txt, ok := clip.ReadText(); !ok || txt != "way more than four bytes" {
		t.Errorf("confirmed download not written back: %q", txt)
	}
}

// TestConfirmUploadUpdatesPendingRow is the issue #7 regression: confirming a
// deferred over-threshold upload updates the single "待确认" history row in place
// rather than appending a second row.
func TestConfirmUploadUpdatesPendingRow(t *testing.T) {
	f := newFakeServer(t)
	_, pub, _ := e2ee.GenerateDeviceKey()
	f.targets = []protocol.TargetDevice{{DeviceID: "target", HPKEPublicKey: e2ee.PublicKeyBase64(pub)}}
	f.eff = &protocol.EffectiveConfig{
		MaxSyncSizeBytes: 100 << 20, AllowedTypes: []protocol.ContentType{protocol.ContentText},
		MaxAutoUploadSizeBytes: 8, MaxAutoDownloadSizeBytes: 100 << 20,
	}
	srcPriv, _, _ := e2ee.GenerateDeviceKey()
	eng := New(Identity{DeviceID: "src", PrivateKey: srcPriv}, f.client("tok"), &fakeClipboard{})
	_ = eng.RefreshConfig(context.Background())

	var req ConfirmRequest
	eng.SetConfirmHook(func(c ConfirmRequest) { req = c })
	if err := eng.PublishText(context.Background(), "this text is well over eight bytes"); err != nil {
		t.Fatalf("publish: %v", err)
	}
	if n := len(eng.History()); n != 1 {
		t.Fatalf("after defer: history len = %d, want 1 (one 待确认 row)", n)
	}
	if err := eng.ConfirmUpload(context.Background(), req.ID); err != nil {
		t.Fatalf("confirm: %v", err)
	}
	h := eng.History()
	if len(h) != 1 {
		t.Fatalf("after confirm: history len = %d, want 1 (pending row updated in place, not a second row)", len(h))
	}
	if h[0].Status != StatusOK {
		t.Errorf("pending row status = %q, want %q", h[0].Status, StatusOK)
	}
}

// TestDiscardDownloadUpdatesPendingRow is the issue #7 regression on the download
// side: declining a deferred over-threshold delivery updates the pending row to
// ignored (keeping its content type) rather than adding a second row.
func TestDiscardDownloadUpdatesPendingRow(t *testing.T) {
	f := newFakeServer(t)
	myPriv, myPub, _ := e2ee.GenerateDeviceKey()
	eng := New(Identity{DeviceID: "me", PrivateKey: myPriv}, f.client("tok"), &fakeClipboard{})
	f.eff = &protocol.EffectiveConfig{
		MaxSyncSizeBytes: 100 << 20, AllowedTypes: []protocol.ContentType{protocol.ContentText},
		MaxAutoUploadSizeBytes: 100 << 20, MaxAutoDownloadSizeBytes: 4,
	}
	_ = eng.RefreshConfig(context.Background())
	makeDelivery(t, f, myPub, "del-big", "way more than four bytes", 0)
	if err := eng.SyncPending(context.Background()); err != nil {
		t.Fatalf("sync: %v", err)
	}
	if n := len(eng.History()); n != 1 {
		t.Fatalf("after defer: history len = %d, want 1", n)
	}
	if err := eng.DiscardDownload(context.Background(), "del-big"); err != nil {
		t.Fatalf("discard: %v", err)
	}
	h := eng.History()
	if len(h) != 1 {
		t.Fatalf("after discard: history len = %d, want 1 (pending row updated in place)", len(h))
	}
	if h[0].Status != StatusIgnored {
		t.Errorf("row status = %q, want %q", h[0].Status, StatusIgnored)
	}
	if h[0].ContentType != protocol.ContentText {
		t.Errorf("discard should preserve the pending row's content type, got %q", h[0].ContentType)
	}
}

// TestDuplicateClipboardSuppressed is the regression for the duplicate-record bug:
// a spurious clipboard re-fire of identical content (the OS bumping the change
// counter without the content changing) must not create a second sync record,
// while genuinely new content still records.
func TestDuplicateClipboardSuppressed(t *testing.T) {
	f := newFakeServer(t)
	srcPriv, _, _ := e2ee.GenerateDeviceKey()
	eng := New(Identity{DeviceID: "src", PrivateKey: srcPriv}, f.client("tok"), &fakeClipboard{})
	ctx := context.Background()

	if err := eng.OnClipboardChanged(ctx, "hello world"); err != nil {
		t.Fatalf("first: %v", err)
	}
	if err := eng.OnClipboardChanged(ctx, "hello world"); err != nil {
		t.Fatalf("second (dup): %v", err)
	}
	if n := len(eng.History()); n != 1 {
		t.Fatalf("identical clipboard re-fire created %d records, want 1", n)
	}
	// Genuinely new content publishes again.
	if err := eng.OnClipboardChanged(ctx, "different text"); err != nil {
		t.Fatalf("third: %v", err)
	}
	if n := len(eng.History()); n != 2 {
		t.Fatalf("new content not recorded; got %d records, want 2", n)
	}
}

// TestIgnoredStatusForNoTargets verifies a skip (no online target) is recorded as
// ignored, not failed.
func TestIgnoredStatusForNoTargets(t *testing.T) {
	f := newFakeServer(t)
	srcPriv, _, _ := e2ee.GenerateDeviceKey()
	eng := New(Identity{DeviceID: "src", PrivateKey: srcPriv}, f.client("tok"), &fakeClipboard{})
	if err := eng.PublishText(context.Background(), "hello"); err != nil {
		t.Fatalf("publish: %v", err)
	}
	h := eng.History()
	if len(h) == 0 {
		t.Fatal("no history recorded")
	}
	last := h[len(h)-1]
	if last.Status != StatusIgnored {
		t.Errorf("status = %q, want %q", last.Status, StatusIgnored)
	}
	if last.OK {
		t.Error("OK should be false for an ignored sync")
	}
}

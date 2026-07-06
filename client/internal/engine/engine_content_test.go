package engine

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"image"
	"image/png"
	"io"
	"os"
	"path/filepath"
	"sync"
	"testing"

	"github.com/mokeyjay/clipbridge/client/internal/e2ee"
	"github.com/mokeyjay/clipbridge/shared/protocol"
)

// fakeFileSink captures received files in memory for assertions.
type fakeFileSink struct {
	mu    sync.Mutex
	saved map[string][]byte
}

func newFakeFileSink() *fakeFileSink { return &fakeFileSink{saved: map[string][]byte{}} }

func (f *fakeFileSink) Save(name string, r io.Reader) (string, error) {
	data, err := io.ReadAll(r)
	if err != nil {
		return "", err
	}
	f.mu.Lock()
	f.saved[name] = data
	f.mu.Unlock()
	return "/tmp/" + name, nil
}

// TestPublishImageEnvelopeAndMetadata verifies an image is encrypted with sealed
// metadata that a target can open, and the body decrypts to the original bytes.
func TestPublishImageEnvelopeAndMetadata(t *testing.T) {
	f := newFakeServer(t)
	tgtPriv, tgtPub, _ := e2ee.GenerateDeviceKey()
	f.targets = []protocol.TargetDevice{{DeviceID: "target", HPKEPublicKey: e2ee.PublicKeyBase64(tgtPub)}}

	srcPriv, _, _ := e2ee.GenerateDeviceKey()
	eng := New(Identity{DeviceID: "src", PrivateKey: srcPriv}, f.client("tok"), &fakeClipboard{})

	img := make([]byte, 9000)
	_, _ = rand.Read(img)
	meta := Metadata{Filename: "shot.png", Width: 800, Height: 600}
	if err := eng.PublishContent(context.Background(), protocol.ContentImage, meta, bytes.NewReader(img), int64(len(img)), summarizeImage(meta.Width, meta.Height)); err != nil {
		t.Fatalf("publish image: %v", err)
	}
	if len(f.uploads) != 1 {
		t.Fatalf("uploads = %d", len(f.uploads))
	}
	up := f.uploads[0]
	if up.manifest.ContentType != protocol.ContentImage || up.manifest.EncryptedMetadata == "" {
		t.Fatalf("manifest = %+v", up.manifest)
	}

	// Target unwraps DEK, opens metadata, decrypts body.
	hdr := e2ee.Header{ProtocolVersion: up.manifest.ProtocolVersion, ItemID: up.manifest.ItemID, SourceDeviceID: "src", ContentType: protocol.ContentImage}
	wrapped, _ := base64.StdEncoding.DecodeString(up.manifest.Deliveries[0].WrappedDEK)
	dek, err := e2ee.UnwrapDEK(tgtPriv, wrapped, hdr, "target")
	if err != nil {
		t.Fatalf("unwrap: %v", err)
	}
	sealed, _ := base64.StdEncoding.DecodeString(up.manifest.EncryptedMetadata)
	rawMeta, err := e2ee.OpenMetadata(dek, hdr, sealed)
	if err != nil {
		t.Fatalf("open metadata: %v", err)
	}
	if !bytes.Contains(rawMeta, []byte("shot.png")) {
		t.Errorf("metadata missing filename: %s", rawMeta)
	}
	var out bytes.Buffer
	if err := e2ee.DecryptStream(&out, bytes.NewReader(up.ciphertext), dek, hdr, up.manifest.ChunkSizeBytes, up.manifest.TotalChunks); err != nil {
		t.Fatalf("decrypt body: %v", err)
	}
	if !bytes.Equal(out.Bytes(), img) {
		t.Error("image body round-trip mismatch")
	}
}

// TestReceiveImageWritesClipboard verifies an inbound image is decrypted and
// written to the clipboard (and loop-back suppresses re-upload).
func TestReceiveImageWritesClipboard(t *testing.T) {
	f := newFakeServer(t)
	myPriv, myPub, _ := e2ee.GenerateDeviceKey()
	clip := &fakeClipboard{}
	eng := New(Identity{DeviceID: "me", PrivateKey: myPriv}, f.client("tok"), clip)

	img := bytes.Repeat([]byte{0x89, 0x50}, 2000)
	dek, _ := e2ee.NewDEK()
	hdr := e2ee.Header{ProtocolVersion: protocol.ProtocolVersion, ItemID: "i-img", SourceDeviceID: "remote", ContentType: protocol.ContentImage}
	var ct bytes.Buffer
	res, _ := e2ee.EncryptStream(&ct, bytes.NewReader(img), dek, hdr, e2ee.DefaultChunkSize)
	wrapped, _ := e2ee.WrapDEK(myPub, dek, hdr, "me")
	sealed, _ := e2ee.SealMetadata(dek, hdr, []byte(`{"filename":"x.png"}`))

	f.pending = []protocol.DeliveryManifest{{
		DeliveryID: "d-img", ItemID: "i-img", SourceDeviceID: "remote", ContentType: protocol.ContentImage,
		CiphertextSizeBytes: res.CiphertextSizeBytes, CiphertextSHA256: res.CiphertextSHA256,
		ChunkSizeBytes: res.ChunkSizeBytes, TotalChunks: res.TotalChunks,
		WrappedDEK: base64.StdEncoding.EncodeToString(wrapped), EncryptedMetadata: base64.StdEncoding.EncodeToString(sealed),
	}}
	f.contents["d-img"] = ct.Bytes()

	if err := eng.SyncPending(context.Background()); err != nil {
		t.Fatalf("sync: %v", err)
	}
	if !bytes.Equal(clip.readImage(), img) {
		t.Error("clipboard image mismatch")
	}
	if len(f.acked) != 1 {
		t.Errorf("acked = %v", f.acked)
	}
	// Loop-back: the image monitor seeing the just-written image must not re-upload.
	before := f.uploadCount
	if err := eng.OnImageChanged(context.Background(), img); err != nil {
		t.Fatalf("onimage: %v", err)
	}
	if f.uploadCount != before {
		t.Error("written-back image was re-uploaded (loop-back not suppressed)")
	}
}

// TestReceiveFileStreamsToSink verifies an inbound file is decrypted to the file
// sink under its metadata filename.
func TestReceiveFileStreamsToSink(t *testing.T) {
	f := newFakeServer(t)
	myPriv, myPub, _ := e2ee.GenerateDeviceKey()
	sink := newFakeFileSink()
	clip := &fakeClipboard{}
	eng := New(Identity{DeviceID: "me", PrivateKey: myPriv}, f.client("tok"), clip)
	eng.SetFileSink(sink)

	content := bytes.Repeat([]byte("FILEDATA"), 5000) // ~40KB, multi-chunk
	dek, _ := e2ee.NewDEK()
	hdr := e2ee.Header{ProtocolVersion: protocol.ProtocolVersion, ItemID: "i-f", SourceDeviceID: "remote", ContentType: protocol.ContentFile}
	var ct bytes.Buffer
	res, _ := e2ee.EncryptStream(&ct, bytes.NewReader(content), dek, hdr, e2ee.DefaultChunkSize)
	wrapped, _ := e2ee.WrapDEK(myPub, dek, hdr, "me")
	sealed, _ := e2ee.SealMetadata(dek, hdr, []byte(`{"filename":"report.pdf"}`))

	f.pending = []protocol.DeliveryManifest{{
		DeliveryID: "d-f", ItemID: "i-f", SourceDeviceID: "remote", ContentType: protocol.ContentFile,
		CiphertextSizeBytes: res.CiphertextSizeBytes, CiphertextSHA256: res.CiphertextSHA256,
		ChunkSizeBytes: res.ChunkSizeBytes, TotalChunks: res.TotalChunks,
		WrappedDEK: base64.StdEncoding.EncodeToString(wrapped), EncryptedMetadata: base64.StdEncoding.EncodeToString(sealed),
	}}
	f.contents["d-f"] = ct.Bytes()

	if err := eng.SyncPending(context.Background()); err != nil {
		t.Fatalf("sync: %v", err)
	}
	sink.mu.Lock()
	got := sink.saved["report.pdf"]
	sink.mu.Unlock()
	if !bytes.Equal(got, content) {
		t.Errorf("saved file mismatch: %d bytes", len(got))
	}
	// The saved file's path must also be placed on the clipboard (pasteable).
	if clip.readFilePath() != "/tmp/report.pdf" {
		t.Errorf("file path not written to clipboard: %q", clip.readFilePath())
	}
	if got := lastSummary(eng.History()); got != "report.pdf" {
		t.Errorf("download history summary = %q, want report.pdf", got)
	}
}

// TestFileRejectedWithoutSink verifies a file delivery is rejected when no sink.
func TestFileRejectedWithoutSink(t *testing.T) {
	f := newFakeServer(t)
	myPriv, myPub, _ := e2ee.GenerateDeviceKey()
	eng := New(Identity{DeviceID: "me", PrivateKey: myPriv}, f.client("tok"), &fakeClipboard{})
	// no SetFileSink

	dek, _ := e2ee.NewDEK()
	hdr := e2ee.Header{ProtocolVersion: protocol.ProtocolVersion, ItemID: "i-f2", SourceDeviceID: "remote", ContentType: protocol.ContentFile}
	var ct bytes.Buffer
	res, _ := e2ee.EncryptStream(&ct, bytes.NewReader([]byte("data")), dek, hdr, e2ee.DefaultChunkSize)
	wrapped, _ := e2ee.WrapDEK(myPub, dek, hdr, "me")
	f.pending = []protocol.DeliveryManifest{{
		DeliveryID: "d-f2", ItemID: "i-f2", SourceDeviceID: "remote", ContentType: protocol.ContentFile,
		CiphertextSizeBytes: res.CiphertextSizeBytes, CiphertextSHA256: res.CiphertextSHA256,
		ChunkSizeBytes: res.ChunkSizeBytes, TotalChunks: res.TotalChunks,
		WrappedDEK: base64.StdEncoding.EncodeToString(wrapped),
	}}
	f.contents["d-f2"] = ct.Bytes()

	_ = eng.SyncPending(context.Background())
	if f.rejected["d-f2"] != protocol.RejectPolicyBlocked {
		t.Errorf("file without sink should be POLICY_BLOCKED, got %v", f.rejected["d-f2"])
	}
}

// TestImageDimensionsDecoded verifies width/height are read from PNG bytes.
func TestImageDimensionsDecoded(t *testing.T) {
	var buf bytes.Buffer
	if err := png.Encode(&buf, image.NewRGBA(image.Rect(0, 0, 24, 17))); err != nil {
		t.Fatalf("encode: %v", err)
	}
	w, h, ok := imageDimensions(buf.Bytes())
	if !ok || w != 24 || h != 17 {
		t.Fatalf("dims = %dx%d ok=%v, want 24x17", w, h, ok)
	}
	// Non-image bytes must fail gracefully (no panic, ok=false).
	if _, _, ok := imageDimensions([]byte("not a png")); ok {
		t.Error("garbage bytes reported as a decodable image")
	}
}

// TestOnImageChangedSealsDimensions verifies a copied image's dimensions are
// extracted and sealed into the encrypted metadata block on publish.
func TestOnImageChangedSealsDimensions(t *testing.T) {
	f := newFakeServer(t)
	tgtPriv, tgtPub, _ := e2ee.GenerateDeviceKey()
	f.targets = []protocol.TargetDevice{{DeviceID: "target", HPKEPublicKey: e2ee.PublicKeyBase64(tgtPub)}}

	srcPriv, _, _ := e2ee.GenerateDeviceKey()
	eng := New(Identity{DeviceID: "src", PrivateKey: srcPriv}, f.client("tok"), &fakeClipboard{})

	var buf bytes.Buffer
	if err := png.Encode(&buf, image.NewRGBA(image.Rect(0, 0, 40, 30))); err != nil {
		t.Fatalf("encode: %v", err)
	}
	if err := eng.OnImageChanged(context.Background(), buf.Bytes()); err != nil {
		t.Fatalf("onimage: %v", err)
	}
	if len(f.uploads) != 1 {
		t.Fatalf("uploads = %d", len(f.uploads))
	}
	up := f.uploads[0]
	if up.manifest.EncryptedMetadata == "" {
		t.Fatal("image upload carried no encrypted metadata")
	}

	// Target unwraps the DEK and opens the metadata to read the dimensions.
	hdr := e2ee.Header{ProtocolVersion: up.manifest.ProtocolVersion, ItemID: up.manifest.ItemID, SourceDeviceID: "src", ContentType: protocol.ContentImage}
	wrapped, _ := base64.StdEncoding.DecodeString(up.manifest.Deliveries[0].WrappedDEK)
	dek, err := e2ee.UnwrapDEK(tgtPriv, wrapped, hdr, "target")
	if err != nil {
		t.Fatalf("unwrap: %v", err)
	}
	sealed, _ := base64.StdEncoding.DecodeString(up.manifest.EncryptedMetadata)
	raw, err := e2ee.OpenMetadata(dek, hdr, sealed)
	if err != nil {
		t.Fatalf("open metadata: %v", err)
	}
	var meta Metadata
	if err := json.Unmarshal(raw, &meta); err != nil {
		t.Fatalf("unmarshal meta: %v", err)
	}
	if meta.Width != 40 || meta.Height != 30 {
		t.Errorf("sealed dims = %dx%d, want 40x30", meta.Width, meta.Height)
	}
}

// TestOnRichTextChangedPublishes verifies rich text is uploaded as the body with
// its format and plain-text fallback sealed in the metadata.
func TestOnRichTextChangedPublishes(t *testing.T) {
	f := newFakeServer(t)
	tgtPriv, tgtPub, _ := e2ee.GenerateDeviceKey()
	f.targets = []protocol.TargetDevice{{DeviceID: "target", HPKEPublicKey: e2ee.PublicKeyBase64(tgtPub)}}

	srcPriv, _, _ := e2ee.GenerateDeviceKey()
	eng := New(Identity{DeviceID: "src", PrivateKey: srcPriv}, f.client("tok"), &fakeClipboard{})

	rtf := []byte(`{\rtf1\ansi this is \b bold\b0 text}`)
	if err := eng.OnRichTextChanged(context.Background(), "rtf", rtf, "this is bold text"); err != nil {
		t.Fatalf("onrich: %v", err)
	}
	if len(f.uploads) != 1 {
		t.Fatalf("uploads = %d", len(f.uploads))
	}
	up := f.uploads[0]
	if up.manifest.ContentType != protocol.ContentRichText || up.manifest.EncryptedMetadata == "" {
		t.Fatalf("manifest = %+v", up.manifest)
	}

	hdr := e2ee.Header{ProtocolVersion: up.manifest.ProtocolVersion, ItemID: up.manifest.ItemID, SourceDeviceID: "src", ContentType: protocol.ContentRichText}
	wrapped, _ := base64.StdEncoding.DecodeString(up.manifest.Deliveries[0].WrappedDEK)
	dek, err := e2ee.UnwrapDEK(tgtPriv, wrapped, hdr, "target")
	if err != nil {
		t.Fatalf("unwrap: %v", err)
	}
	sealed, _ := base64.StdEncoding.DecodeString(up.manifest.EncryptedMetadata)
	raw, err := e2ee.OpenMetadata(dek, hdr, sealed)
	if err != nil {
		t.Fatalf("open metadata: %v", err)
	}
	var meta Metadata
	if err := json.Unmarshal(raw, &meta); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if meta.RichFormat != "rtf" || meta.PlainText != "this is bold text" {
		t.Errorf("meta = %+v, want rtf + plain fallback", meta)
	}
	var out bytes.Buffer
	if err := e2ee.DecryptStream(&out, bytes.NewReader(up.ciphertext), dek, hdr, up.manifest.ChunkSizeBytes, up.manifest.TotalChunks); err != nil {
		t.Fatalf("decrypt body: %v", err)
	}
	if !bytes.Equal(out.Bytes(), rtf) {
		t.Error("rich body round-trip mismatch")
	}
}

// TestReceiveRichTextWritesClipboard verifies an inbound rich-text delivery is
// written to the clipboard with format + plain fallback, and loop-back is
// suppressed for both the rich and the plain forms.
func TestReceiveRichTextWritesClipboard(t *testing.T) {
	f := newFakeServer(t)
	myPriv, myPub, _ := e2ee.GenerateDeviceKey()
	clip := &fakeClipboard{}
	eng := New(Identity{DeviceID: "me", PrivateKey: myPriv}, f.client("tok"), clip)

	rtf := []byte(`{\rtf1\ansi hello \b world\b0}`)
	plainFallback := "hello world"
	dek, _ := e2ee.NewDEK()
	hdr := e2ee.Header{ProtocolVersion: protocol.ProtocolVersion, ItemID: "i-r", SourceDeviceID: "remote", ContentType: protocol.ContentRichText}
	var ct bytes.Buffer
	res, _ := e2ee.EncryptStream(&ct, bytes.NewReader(rtf), dek, hdr, e2ee.DefaultChunkSize)
	wrapped, _ := e2ee.WrapDEK(myPub, dek, hdr, "me")
	metaJSON, _ := json.Marshal(Metadata{RichFormat: "rtf", PlainText: plainFallback})
	sealed, _ := e2ee.SealMetadata(dek, hdr, metaJSON)

	f.pending = []protocol.DeliveryManifest{{
		DeliveryID: "d-r", ItemID: "i-r", SourceDeviceID: "remote", ContentType: protocol.ContentRichText,
		CiphertextSizeBytes: res.CiphertextSizeBytes, CiphertextSHA256: res.CiphertextSHA256,
		ChunkSizeBytes: res.ChunkSizeBytes, TotalChunks: res.TotalChunks,
		WrappedDEK: base64.StdEncoding.EncodeToString(wrapped), EncryptedMetadata: base64.StdEncoding.EncodeToString(sealed),
	}}
	f.contents["d-r"] = ct.Bytes()

	if err := eng.SyncPending(context.Background()); err != nil {
		t.Fatalf("sync: %v", err)
	}
	gotFmt, gotRich, gotPlain := clip.readRich()
	if gotFmt != "rtf" || !bytes.Equal(gotRich, rtf) || gotPlain != plainFallback {
		t.Errorf("clipboard rich = (%q, %q, %q)", gotFmt, gotRich, gotPlain)
	}
	if len(f.acked) != 1 {
		t.Errorf("acked = %v", f.acked)
	}
	// Loop-back: re-seeing the just-written rich bytes must not re-upload.
	before := f.uploadCount
	if err := eng.OnRichTextChanged(context.Background(), "rtf", rtf, plainFallback); err != nil {
		t.Fatalf("onrich: %v", err)
	}
	if f.uploadCount != before {
		t.Error("written-back rich text was re-uploaded (loop-back not suppressed)")
	}
}

// TestOnFileChangedStreams verifies a copied file is streamed from disk and
// uploaded as a file item with its name sealed in the metadata.
func TestOnFileChangedStreams(t *testing.T) {
	f := newFakeServer(t)
	tgtPriv, tgtPub, _ := e2ee.GenerateDeviceKey()
	f.targets = []protocol.TargetDevice{{DeviceID: "target", HPKEPublicKey: e2ee.PublicKeyBase64(tgtPub)}}

	srcPriv, _, _ := e2ee.GenerateDeviceKey()
	eng := New(Identity{DeviceID: "src", PrivateKey: srcPriv}, f.client("tok"), &fakeClipboard{})

	// Write a real temp file so OnFileChanged opens and streams it from disk.
	content := bytes.Repeat([]byte("DOC"), 7000) // ~21KB, multi-chunk
	dir := t.TempDir()
	path := filepath.Join(dir, "notes.txt")
	if err := os.WriteFile(path, content, 0o600); err != nil {
		t.Fatalf("write temp: %v", err)
	}

	if err := eng.OnFileChanged(context.Background(), path); err != nil {
		t.Fatalf("onfile: %v", err)
	}
	if len(f.uploads) != 1 {
		t.Fatalf("uploads = %d", len(f.uploads))
	}
	up := f.uploads[0]
	if up.manifest.ContentType != protocol.ContentFile {
		t.Fatalf("content type = %v", up.manifest.ContentType)
	}

	hdr := e2ee.Header{ProtocolVersion: up.manifest.ProtocolVersion, ItemID: up.manifest.ItemID, SourceDeviceID: "src", ContentType: protocol.ContentFile}
	wrapped, _ := base64.StdEncoding.DecodeString(up.manifest.Deliveries[0].WrappedDEK)
	dek, err := e2ee.UnwrapDEK(tgtPriv, wrapped, hdr, "target")
	if err != nil {
		t.Fatalf("unwrap: %v", err)
	}
	sealed, _ := base64.StdEncoding.DecodeString(up.manifest.EncryptedMetadata)
	raw, err := e2ee.OpenMetadata(dek, hdr, sealed)
	if err != nil {
		t.Fatalf("open metadata: %v", err)
	}
	var meta Metadata
	_ = json.Unmarshal(raw, &meta)
	if meta.Filename != "notes.txt" {
		t.Errorf("filename = %q, want notes.txt", meta.Filename)
	}
	var out bytes.Buffer
	if err := e2ee.DecryptStream(&out, bytes.NewReader(up.ciphertext), dek, hdr, up.manifest.ChunkSizeBytes, up.manifest.TotalChunks); err != nil {
		t.Fatalf("decrypt: %v", err)
	}
	if !bytes.Equal(out.Bytes(), content) {
		t.Error("file body round-trip mismatch")
	}
	if h := eng.History(); len(h) == 0 || h[len(h)-1].Summary != "notes.txt" {
		t.Errorf("upload history summary = %q, want notes.txt", lastSummary(h))
	}
}

// lastSummary returns the summary of the most recent history event (or "").
func lastSummary(h []Event) string {
	if len(h) == 0 {
		return ""
	}
	return h[len(h)-1].Summary
}

// TestSummaryHelpers covers the local-only history preview formatting.
func TestSummaryHelpers(t *testing.T) {
	for _, c := range []struct{ in, want string }{
		{"abcdefghijkl", "abcde…hijkl"},
		{"abcdefghij", "abcdefghij"},
		{"abc", "abc"},
		{"  a b  c d ef g h ", "a b c…f g h"},
	} {
		if got := summarizeText(c.in); got != c.want {
			t.Errorf("summarizeText(%q) = %q, want %q", c.in, got, c.want)
		}
	}
	if got := summarizeFilename("report.pdf"); got != "report.pdf" {
		t.Errorf("short filename = %q", got)
	}
	if got := summarizeFilename("quarterly_report_final.pdf"); got != "quart…final.pdf" {
		t.Errorf("long filename = %q", got)
	}
	if got := summarizeImage(800, 600); got != "图片 800×600" {
		t.Errorf("image summary = %q", got)
	}
	if got := summarizeImage(0, 0); got != "图片" {
		t.Errorf("no-dims image = %q", got)
	}
}

// TestHistorySummaryUpload verifies an uploaded item records a content preview.
func TestHistorySummaryUpload(t *testing.T) {
	f := newFakeServer(t)
	_, tgtPub, _ := e2ee.GenerateDeviceKey()
	f.targets = []protocol.TargetDevice{{DeviceID: "target", HPKEPublicKey: e2ee.PublicKeyBase64(tgtPub)}}
	srcPriv, _, _ := e2ee.GenerateDeviceKey()
	eng := New(Identity{DeviceID: "src", PrivateKey: srcPriv}, f.client("tok"), &fakeClipboard{})

	if err := eng.PublishText(context.Background(), "abcdefghijklmno"); err != nil {
		t.Fatalf("publish: %v", err)
	}
	if got := lastSummary(eng.History()); got != "abcde…klmno" {
		t.Errorf("upload summary = %q, want abcde…klmno", got)
	}
}

// Package engine is the device-side sync orchestration: it encrypts clipboard
// content and uploads it to online targets, and decrypts inbound deliveries and
// writes them back. It is platform-agnostic — the OS clipboard is injected via
// the Clipboard interface so the core logic is unit-testable without a desktop.
// Loop-back is prevented by fingerprinting content written from remote deliveries.
package engine

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"image"
	"image/draw"
	_ "image/png" // register PNG decoder for clipboard image dimension extraction
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/mokeyjay/clipbridge/client/internal/apiclient"
	"github.com/mokeyjay/clipbridge/client/internal/e2ee"
	"github.com/mokeyjay/clipbridge/shared/protocol"
)

// Clipboard is the platform clipboard adapter. The real macOS implementation
// (golang.design/x/clipboard for text/image, native NSPasteboard for rich text
// and files) is wired in clipboardadapter.
type Clipboard interface {
	// ReadText returns the current clipboard text and whether text is present.
	ReadText() (string, bool)
	// WriteText replaces the clipboard text.
	WriteText(text string) error
	// WriteImage places PNG image bytes on the clipboard.
	WriteImage(png []byte) error
	// WriteRichText places rich content (format "rtf" | "html") on the clipboard,
	// with plain as the plain-text fallback for apps that can't take rich text.
	WriteRichText(format string, rich []byte, plain string) error
	// WriteFile places a file reference (its path) on the clipboard so the user
	// can paste the file (e.g. in Finder).
	WriteFile(path string) error
}

// FileSink stores a received file (e.g. a temp directory), returning its path.
type FileSink interface {
	Save(name string, r io.Reader) (path string, err error)
}

// Metadata is the DEK-sealed, content-specific metadata carried alongside the
// body (filename for files, dimensions for images, rich-text format and plain
// fallback for rich text, etc.). It never travels in the clear. Empty for plain
// text.
type Metadata struct {
	Filename string `json:"filename,omitempty"`
	Width    int    `json:"width,omitempty"`
	Height   int    `json:"height,omitempty"`
	// RichFormat is "rtf" | "html" for rich-text items; empty otherwise.
	RichFormat string `json:"rich_format,omitempty"`
	// PlainText is the sender's plain-text fallback for rich-text items, written
	// alongside the rich payload so apps that can't take rich text still paste.
	PlainText string `json:"plain_text,omitempty"`
}

// Identity is this device's stable identity and HPKE key material.
type Identity struct {
	DeviceID   string
	UserID     string
	ServerID   string
	PrivateKey []byte // serialized HPKE private key
}

// Sync-history event status. A sync is "ok" only when it actually moved content;
// "ignored" covers intentional skips (policy/direction blocked, no online target,
// over the auto threshold awaiting confirmation); "failed" is a genuine error.
const (
	StatusOK      = "ok"
	StatusIgnored = "ignored"
	StatusFailed  = "failed"
)

// Sync-history detail codes. The engine emits stable codes (not localized text) so
// the UI renders them in the user's language; the notification layer maps them to
// Chinese separately. Empty detail (a plain success) needs no code.
const (
	DetailDownloadOnlySkip   = "download_only_skip"
	DetailTypeNotAllowed     = "type_not_allowed"
	DetailOverMaxSize        = "over_max_size"
	DetailOverAutoUpload     = "over_auto_upload"
	DetailTargetsQueryFailed = "targets_query_failed"
	DetailTargetFpMismatch   = "target_fp_mismatch"
	DetailNoTrustedTarget    = "no_trusted_target"
	DetailUploadFailed       = "upload_failed"
	DetailConfirmTimeout     = "confirm_timeout"
	DetailIgnoredUnconfirmed = "ignored_unconfirmed"
	DetailUploadOnlyReject   = "upload_only_reject"
	DetailUnknownType        = "unknown_type"
	DetailNoRecvDir          = "no_recv_dir"
	DetailOverAutoDownload   = "over_auto_download"
	DetailUnwrapFailed       = "unwrap_failed"
	DetailDecryptFailed      = "decrypt_failed"
	DetailSaveFailed         = "save_failed"
)

// confirmWindow bounds how long a deferred over-threshold item waits for the
// user's confirmation before it is skipped (prd §4: 大文件人工确认最长等待 5 分钟).
const confirmWindow = 5 * time.Minute

// Event is one in-memory sync-history entry for the current session.
type Event struct {
	At          time.Time
	Direction   string // "upload" | "download"
	ContentType protocol.ContentType
	SizeBytes   int64
	// OK is true only for a fully-completed sync (Status == StatusOK); kept for the
	// session success counters.
	OK bool
	// Status is the three-state outcome: StatusOK | StatusIgnored | StatusFailed.
	Status string
	Detail string
	// Summary is a local-only content preview (text head/tail, filename, or image
	// dimensions) shown in the UI. It is never uploaded to the server.
	Summary string
	// id keys a confirmable item (pending-upload id / delivery id) so a later
	// confirm / ignore / timeout updates this same entry in place rather than
	// appending a second row. Empty for one-shot events. Local-only, never uploaded.
	id string
}

// ConfirmRequest describes an over-threshold item awaiting the user's decision,
// surfaced to the host (which raises an actionable notification). Kind is
// "upload" | "download"; ID is the pending-upload item id or the delivery id.
type ConfirmRequest struct {
	ID          string
	Kind        string
	ContentType protocol.ContentType
	SizeBytes   int64
	Summary     string
}

// preparedUpload is an encrypted-but-not-yet-uploaded item held while it awaits
// the user's confirmation (it exceeded the auto-upload threshold). The DEK is kept
// so targets can be wrapped at confirm time, when the online set is current.
type preparedUpload struct {
	itemID    string
	ct        protocol.ContentType
	dek       []byte
	hdr       e2ee.Header
	sealedB64 string
	sizeBytes int64
	sha256    string
	chunkSize int
	chunks    int
	tmpPath   string
	summary   string
	createdAt time.Time
}

// Engine coordinates encryption, upload, download and loop-back suppression.
type Engine struct {
	id    Identity
	api   *apiclient.Client
	clip  Clipboard
	files FileSink
	now   func() time.Time

	// loopback suppresses re-upload of content this device just wrote back from a
	// remote delivery. It is self-synchronized (its own lock), independent of mu.
	loopback *loopbackGuard

	mu   sync.Mutex
	tofu map[string]string // target deviceID -> trusted key fingerprint
	// mismatches 记录当前处于「公钥指纹与缓存不一致、同步已阻断」状态的对端，
	// 供 UI 持续告警;用户重新互验后可 TrustPeer 手动信任新指纹(prd/03 §5.4)。
	mismatches map[string]*PeerMismatch
	// onTOFUChange(若设置)在 TOFU 缓存变化后带副本回调(off-lock)，宿主用它持久化。
	onTOFUChange func(map[string]string)
	deferred     map[string]bool // delivery IDs deferred pending confirmation
	direction    protocol.SyncDirection
	eff          *protocol.EffectiveConfig // cached effective policy (nil until fetched)
	history      []Event
	// lastClipFP is the fingerprint of the last clipboard content this engine
	// processed. macOS/Windows bump the clipboard change counter even when the
	// content didn't actually change (e.g. an app re-asserting a promised type),
	// which would otherwise re-publish identical content and spawn duplicate
	// records; we skip a change whose fingerprint matches this.
	lastClipFP string

	// pendingUploads holds over-threshold uploads awaiting user confirmation,
	// keyed by item id.
	pendingUploads map[string]*preparedUpload
	// onEvent (if set) is called off-lock for every recorded history event so the
	// host can drive notifications and live UI refresh.
	onEvent func(Event)
	// onConfirm (if set) is called when an item exceeds the auto threshold and
	// needs the user's confirmation (delivered as an actionable notification).
	onConfirm func(ConfirmRequest)
}

// New builds an engine for the given identity, API client and clipboard adapter.
func New(id Identity, api *apiclient.Client, clip Clipboard) *Engine {
	return &Engine{
		id:             id,
		api:            api,
		clip:           clip,
		now:            time.Now,
		loopback:       newLoopbackGuard(time.Now),
		tofu:           make(map[string]string),
		mismatches:     make(map[string]*PeerMismatch),
		deferred:       make(map[string]bool),
		direction:      protocol.DirectionBidirectional,
		pendingUploads: make(map[string]*preparedUpload),
	}
}

// SetEventHook registers a callback invoked (off-lock) for each history event.
func (e *Engine) SetEventHook(fn func(Event)) {
	e.mu.Lock()
	e.onEvent = fn
	e.mu.Unlock()
}

// SetConfirmHook registers a callback invoked when an over-threshold item needs
// the user's confirmation.
func (e *Engine) SetConfirmHook(fn func(ConfirmRequest)) {
	e.mu.Lock()
	e.onConfirm = fn
	e.mu.Unlock()
}

// SetDirection sets the local sync direction policy.
func (e *Engine) SetDirection(d protocol.SyncDirection) {
	if d == "" {
		d = protocol.DirectionBidirectional
	}
	e.mu.Lock()
	e.direction = d
	e.mu.Unlock()
}

// SetFileSink sets the destination for received files (e.g. a temp directory).
func (e *Engine) SetFileSink(fs FileSink) {
	e.mu.Lock()
	e.files = fs
	e.mu.Unlock()
}

// RefreshConfig fetches and caches the device's effective config from the server.
// Called on connect, on WSS reconnect and after a config.changed notification.
func (e *Engine) RefreshConfig(ctx context.Context) error {
	cfg, err := e.api.GetEffectiveConfig(ctx)
	if err != nil {
		return err
	}
	e.mu.Lock()
	e.eff = cfg
	e.mu.Unlock()
	return nil
}

// policy snapshots the direction and effective config under the lock.
func (e *Engine) policy() (protocol.SyncDirection, *protocol.EffectiveConfig) {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.direction, e.eff
}

// EffectiveConfig returns a copy of the cached server-resolved policy, or nil if
// no effective config has been fetched yet. Used by the GUI to show the values a
// device inherits from the account default.
func (e *Engine) EffectiveConfig() *protocol.EffectiveConfig {
	e.mu.Lock()
	defer e.mu.Unlock()
	if e.eff == nil {
		return nil
	}
	c := *e.eff
	return &c
}

// EffectiveFileTTLDays returns the server-resolved account-level file retention
// (days) and whether an effective config has been fetched yet.
func (e *Engine) EffectiveFileTTLDays() (int64, bool) {
	e.mu.Lock()
	defer e.mu.Unlock()
	if e.eff == nil {
		return 0, false
	}
	return e.eff.FileTTLDays, true
}

// fingerprint returns a content fingerprint used for loop-back detection.
func fingerprint(text string) string {
	sum := sha256.Sum256([]byte(text))
	return hex.EncodeToString(sum[:])
}

// fingerprintBytes is fingerprint for binary content (images, files).
func fingerprintBytes(b []byte) string {
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:])
}

// loopbackTTL bounds how long a written-back fingerprint stays eligible to
// suppress the echoing clipboard event. The monitor fires within ~1s (its poll
// interval); a generous window absorbs OS coalescing while still expiring
// entries whose event never arrives, so the guard cannot grow without bound.
const loopbackTTL = 30 * time.Second

// loopbackGuard remembers fingerprints of content this device just wrote to the
// clipboard from a remote delivery, so the clipboard monitor can suppress the
// resulting change event instead of re-uploading it. Entries are one-shot
// (consumed on the first matching event) and expire after loopbackTTL, which
// caps the map at the fingerprints remembered within that window.
type loopbackGuard struct {
	mu   sync.Mutex
	seen map[string]time.Time
	now  func() time.Time
}

// newLoopbackGuard builds an empty guard using the given clock.
func newLoopbackGuard(now func() time.Time) *loopbackGuard {
	return &loopbackGuard{seen: make(map[string]time.Time), now: now}
}

// remember records a fingerprint just written locally, first evicting any
// entries older than loopbackTTL so a never-echoed write-back can't accumulate.
func (g *loopbackGuard) remember(fp string) {
	g.mu.Lock()
	defer g.mu.Unlock()
	now := g.now()
	for k, t := range g.seen {
		if now.Sub(t) > loopbackTTL {
			delete(g.seen, k)
		}
	}
	g.seen[fp] = now
}

// suppress reports whether fp matches a recent local write-back, consuming the
// entry so it suppresses exactly one clipboard event. A stale match is ignored.
func (g *loopbackGuard) suppress(fp string) bool {
	g.mu.Lock()
	defer g.mu.Unlock()
	t, ok := g.seen[fp]
	if !ok {
		return false
	}
	delete(g.seen, fp)
	return g.now().Sub(t) <= loopbackTTL
}

// header builds the AAD-binding envelope header for an item.
func (e *Engine) header(itemID, sourceDeviceID string, ct protocol.ContentType) e2ee.Header {
	return e2ee.Header{ProtocolVersion: protocol.ProtocolVersion, ItemID: itemID, SourceDeviceID: sourceDeviceID, ContentType: ct}
}

// clipUnchanged reports whether fp matches the last clipboard content this engine
// processed, recording fp as the new last when it differs. It lets the change
// handlers ignore spurious clipboard re-fires (the OS bumping the change counter
// without the content actually changing), which would otherwise re-publish
// identical content and create duplicate sync records.
func (e *Engine) clipUnchanged(fp string) bool {
	e.mu.Lock()
	defer e.mu.Unlock()
	if e.lastClipFP == fp {
		return true
	}
	e.lastClipFP = fp
	return false
}

// OnClipboardChanged is invoked by the clipboard monitor when local text changes.
// It ignores spurious re-fires of unchanged content and content we just wrote from
// a remote delivery (loop-back), then publishes everything else.
func (e *Engine) OnClipboardChanged(ctx context.Context, text string) error {
	if text == "" {
		return nil
	}
	fp := fingerprint(text)
	if e.clipUnchanged(fp) {
		return nil
	}
	if e.loopback.suppress(fp) {
		return nil
	}
	return e.PublishText(ctx, text)
}

// OnImageChanged is invoked by the clipboard monitor when a local image is
// copied; it suppresses images we just wrote from a delivery, else publishes.
func (e *Engine) OnImageChanged(ctx context.Context, png []byte) error {
	if len(png) == 0 {
		return nil
	}
	fp := imageFingerprint(png)
	if e.clipUnchanged(fp) {
		return nil
	}
	if e.loopback.suppress(fp) {
		return nil
	}
	// Seal the image dimensions into the metadata so a receiver can show them
	// without the server ever seeing the size (best-effort; zero if undecodable).
	meta := Metadata{}
	if w, h, ok := imageDimensions(png); ok {
		meta.Width, meta.Height = w, h
	}
	return e.PublishContent(ctx, protocol.ContentImage, meta, bytes.NewReader(png), int64(len(png)), summarizeImage(meta.Width, meta.Height))
}

// imageDimensions best-effort decodes width/height from image bytes (PNG from
// the macOS clipboard) for the encrypted metadata block. ok is false on failure.
func imageDimensions(b []byte) (w, h int, ok bool) {
	cfg, _, err := image.DecodeConfig(bytes.NewReader(b))
	if err != nil {
		return 0, 0, false
	}
	return cfg.Width, cfg.Height, true
}

// imageFingerprint returns a fingerprint over an image's decoded pixels (and
// dimensions) rather than its encoded bytes, so loop-back detection survives the
// re-encoding a clipboard PNG round-trip causes: the OS may store the picture as
// TIFF and hand back PNG bytes that differ byte-for-byte while the pixels are
// identical. Falls back to a raw byte fingerprint when the image can't be decoded.
func imageFingerprint(b []byte) string {
	img, _, err := image.Decode(bytes.NewReader(b))
	if err != nil {
		return fingerprintBytes(b)
	}
	bounds := img.Bounds()
	rgba := image.NewRGBA(bounds)
	draw.Draw(rgba, bounds, img, bounds.Min, draw.Src)
	h := sha256.New()
	fmt.Fprintf(h, "%dx%d:", bounds.Dx(), bounds.Dy())
	h.Write(rgba.Pix)
	return hex.EncodeToString(h.Sum(nil))
}

// summarizeText collapses whitespace then keeps the first and last 5 runes with
// an ellipsis between (≤10 runes shown in full) for a one-line history preview.
func summarizeText(s string) string {
	f := strings.Join(strings.Fields(s), " ")
	r := []rune(f)
	if len(r) <= 10 {
		return f
	}
	return string(r[:5]) + "…" + string(r[len(r)-5:])
}

// summarizeFilename abbreviates a filename's stem to the first and last 5 runes
// while keeping the extension (short stems shown in full).
func summarizeFilename(name string) string {
	ext := filepath.Ext(name)
	stem := name[:len(name)-len(ext)]
	r := []rune(stem)
	if len(r) <= 10 {
		return name
	}
	return string(r[:5]) + "…" + string(r[len(r)-5:]) + ext
}

// summarizeImage renders an image preview as "图片 宽×高" from the (metadata)
// dimensions, or just "图片" when they are unknown.
func summarizeImage(w, h int) string {
	if w > 0 && h > 0 {
		return fmt.Sprintf("图片 %d×%d", w, h)
	}
	return "图片"
}

// OnRichTextChanged is invoked when local rich text (RTF/HTML) is copied. It
// suppresses content we just wrote from a delivery (loop-back), then publishes
// the rich bytes as the body with the format and plain-text fallback sealed in
// the metadata. plain is the sender's plain-text version for the fallback.
func (e *Engine) OnRichTextChanged(ctx context.Context, format string, rich []byte, plain string) error {
	if len(rich) == 0 {
		return nil
	}
	fp := fingerprintBytes(rich)
	if e.clipUnchanged(fp) {
		return nil
	}
	if e.loopback.suppress(fp) {
		return nil
	}
	meta := Metadata{RichFormat: format, PlainText: plain}
	return e.PublishContent(ctx, protocol.ContentRichText, meta, bytes.NewReader(rich), int64(len(rich)), summarizeText(plain))
}

// OnFileChanged is invoked when a local file is copied (its path arrives from
// the clipboard watcher). It suppresses files we just wrote from a delivery,
// then streams the file contents as an encrypted body with the name in metadata.
func (e *Engine) OnFileChanged(ctx context.Context, path string) error {
	if path == "" {
		return nil
	}
	fp := fingerprint(path)
	if e.clipUnchanged(fp) {
		return nil
	}
	if e.loopback.suppress(fp) {
		return nil
	}
	// Open and stat the file so it streams from disk (never fully buffered).
	f, err := os.Open(path)
	if err != nil {
		return err
	}
	defer f.Close()
	info, err := f.Stat()
	if err != nil {
		return err
	}
	meta := Metadata{Filename: filepath.Base(path)}
	return e.PublishContent(ctx, protocol.ContentFile, meta, f, info.Size(), summarizeFilename(meta.Filename))
}

// PublishText is a convenience wrapper that publishes clipboard text.
func (e *Engine) PublishText(ctx context.Context, text string) error {
	return e.PublishContent(ctx, protocol.ContentText, Metadata{}, bytes.NewReader([]byte(text)), int64(len(text)), summarizeText(text))
}

// PublishContent encrypts content of any type once (streaming from src so the
// plaintext is never fully buffered), seals its metadata, wraps the DEK per
// online TOFU-trusted target, and uploads a single ciphertext. Local policy
// (direction, allowed type, max size, auto threshold) is enforced first. sizeHint
// is the plaintext size if known (0 if unknown). summary is a local-only preview
// recorded on success. No eligible target is not an error.
func (e *Engine) PublishContent(ctx context.Context, ct protocol.ContentType, meta Metadata, src io.Reader, sizeHint int64, summary string) error {
	dir, eff := e.policy()
	if dir == protocol.DirectionDownloadOnly {
		e.record("upload", ct, 0, StatusIgnored, DetailDownloadOnlySkip, summary)
		return nil
	}
	if eff != nil && !allowsType(eff.AllowedTypes, ct) {
		e.record("upload", ct, 0, StatusIgnored, DetailTypeNotAllowed, summary)
		return nil
	}

	dek, err := e2ee.NewDEK()
	if err != nil {
		return err
	}
	itemID := uuid.NewString()
	hdr := e.header(itemID, e.id.DeviceID, ct)

	// Encrypt to a temporary ciphertext file so neither plaintext nor ciphertext
	// is held whole in memory (streaming both sides).
	tmp, err := os.CreateTemp("", "cb-out-*.bin")
	if err != nil {
		return err
	}
	tmpPath := tmp.Name()
	res, err := e2ee.EncryptStream(tmp, src, dek, hdr, e2ee.DefaultChunkSize)
	if cerr := tmp.Close(); err == nil {
		err = cerr
	}
	if err != nil {
		_ = os.Remove(tmpPath)
		return err
	}

	if eff != nil && res.CiphertextSizeBytes > eff.MaxSyncSizeBytes {
		_ = os.Remove(tmpPath)
		e.record("upload", ct, res.CiphertextSizeBytes, StatusIgnored, DetailOverMaxSize, summary)
		return nil
	}

	// Seal the content metadata with the DEK (empty for plain text without meta).
	sealedB64 := ""
	if meta != (Metadata{}) {
		raw, _ := json.Marshal(meta)
		sealed, serr := e2ee.SealMetadata(dek, hdr, raw)
		if serr != nil {
			_ = os.Remove(tmpPath)
			return serr
		}
		sealedB64 = base64.StdEncoding.EncodeToString(sealed)
	}

	p := &preparedUpload{
		itemID: itemID, ct: ct, dek: dek, hdr: hdr, sealedB64: sealedB64,
		sizeBytes: res.CiphertextSizeBytes, sha256: res.CiphertextSHA256,
		chunkSize: res.ChunkSizeBytes, chunks: res.TotalChunks,
		tmpPath: tmpPath, summary: summary, createdAt: e.now(),
	}

	// Over the auto-upload threshold: hold the prepared ciphertext and ask the user
	// to confirm via an actionable notification rather than auto-sending it.
	if eff != nil && res.CiphertextSizeBytes > eff.MaxAutoUploadSizeBytes {
		e.mu.Lock()
		e.pendingUploads[itemID] = p
		hook := e.onConfirm
		e.mu.Unlock()
		e.recordKeyed(itemID, "upload", ct, res.CiphertextSizeBytes, StatusIgnored, DetailOverAutoUpload, summary)
		if hook != nil {
			hook(ConfirmRequest{ID: itemID, Kind: "upload", ContentType: ct, SizeBytes: res.CiphertextSizeBytes, Summary: summary})
		}
		return nil
	}

	return e.uploadPrepared(ctx, p)
}

// uploadPrepared wraps the DEK for each online TOFU-trusted target and streams the
// prepared ciphertext to the server, then removes the temp file. Mismatched target
// keys are blocked; no eligible target is not an error.
func (e *Engine) uploadPrepared(ctx context.Context, p *preparedUpload) error {
	defer func() { _ = os.Remove(p.tmpPath) }()

	targetsResp, err := e.api.GetTargets(ctx)
	if err != nil {
		e.recordKeyed(p.itemID, "upload", p.ct, p.sizeBytes, StatusFailed, DetailTargetsQueryFailed, p.summary)
		return err
	}
	var deliveries []protocol.DeliveryTarget
	for _, tgt := range targetsResp.Targets {
		if !e.trustTarget(tgt.DeviceID, tgt.DeviceName, tgt.HPKEPublicKey) {
			e.record("upload", p.ct, p.sizeBytes, StatusFailed, DetailTargetFpMismatch)
			continue
		}
		pub, derr := base64.StdEncoding.DecodeString(tgt.HPKEPublicKey)
		if derr != nil {
			continue
		}
		wrapped, werr := e2ee.WrapDEK(pub, p.dek, p.hdr, tgt.DeviceID)
		if werr != nil {
			continue
		}
		deliveries = append(deliveries, protocol.DeliveryTarget{TargetDeviceID: tgt.DeviceID, WrappedDEK: base64.StdEncoding.EncodeToString(wrapped)})
	}
	if len(deliveries) == 0 {
		e.recordKeyed(p.itemID, "upload", p.ct, p.sizeBytes, StatusIgnored, DetailNoTrustedTarget, p.summary)
		return nil
	}

	// Stream the temp ciphertext file to the server.
	f, err := os.Open(p.tmpPath)
	if err != nil {
		return err
	}
	defer f.Close()
	manifest := protocol.UploadManifest{
		ProtocolVersion: protocol.ProtocolVersion, ItemID: p.itemID, ContentType: p.ct,
		CiphertextSizeBytes: p.sizeBytes, CiphertextSHA256: p.sha256,
		ChunkSizeBytes: p.chunkSize, TotalChunks: p.chunks,
		EncryptedMetadata: p.sealedB64, Deliveries: deliveries,
	}
	if _, err := e.api.UploadItem(ctx, manifest, f); err != nil {
		e.recordKeyed(p.itemID, "upload", p.ct, p.sizeBytes, StatusFailed, DetailUploadFailed, p.summary)
		return err
	}
	e.recordKeyed(p.itemID, "upload", p.ct, p.sizeBytes, StatusOK, "", p.summary)
	return nil
}

// ConfirmUpload sends a previously-deferred over-threshold upload. The item is
// skipped if it was never deferred or has waited past the confirmation window.
func (e *Engine) ConfirmUpload(ctx context.Context, id string) error {
	e.mu.Lock()
	p := e.pendingUploads[id]
	delete(e.pendingUploads, id)
	e.mu.Unlock()
	if p == nil {
		return nil
	}
	if e.now().Sub(p.createdAt) > confirmWindow {
		_ = os.Remove(p.tmpPath)
		e.recordKeyed(p.itemID, "upload", p.ct, p.sizeBytes, StatusIgnored, DetailConfirmTimeout, p.summary)
		return nil
	}
	return e.uploadPrepared(ctx, p)
}

// DiscardUpload drops a deferred upload (the user declined) and removes its temp file.
func (e *Engine) DiscardUpload(id string) {
	e.mu.Lock()
	p := e.pendingUploads[id]
	delete(e.pendingUploads, id)
	e.mu.Unlock()
	if p == nil {
		return
	}
	_ = os.Remove(p.tmpPath)
	e.recordKeyed(p.itemID, "upload", p.ct, p.sizeBytes, StatusIgnored, DetailIgnoredUnconfirmed, p.summary)
}

// SweepPendingUploads drops deferred uploads older than the confirmation window,
// removing their temp files. Returns the number swept.
func (e *Engine) SweepPendingUploads(now time.Time) int {
	e.mu.Lock()
	var expired []*preparedUpload
	for id, p := range e.pendingUploads {
		if now.Sub(p.createdAt) > confirmWindow {
			expired = append(expired, p)
			delete(e.pendingUploads, id)
		}
	}
	e.mu.Unlock()
	for _, p := range expired {
		_ = os.Remove(p.tmpPath)
		e.recordKeyed(p.itemID, "upload", p.ct, p.sizeBytes, StatusIgnored, DetailConfirmTimeout, p.summary)
	}
	return len(expired)
}

// HandleDelivery downloads, decrypts and writes back a single delivery, then acks
// it. Local direction policy and the auto-download threshold are enforced first.
// Decryption failures reject the delivery with DECRYPT_FAILED.
func (e *Engine) HandleDelivery(ctx context.Context, deliveryID string) error {
	return e.handleDelivery(ctx, deliveryID, false)
}

// ConfirmDownload pulls a previously-deferred over-threshold delivery, bypassing
// the auto-download threshold (the user confirmed it via a notification).
func (e *Engine) ConfirmDownload(ctx context.Context, deliveryID string) error {
	e.mu.Lock()
	delete(e.deferred, deliveryID)
	e.mu.Unlock()
	return e.handleDelivery(ctx, deliveryID, true)
}

// DiscardDownload rejects a deferred delivery (the user declined to receive it).
func (e *Engine) DiscardDownload(ctx context.Context, deliveryID string) error {
	e.mu.Lock()
	delete(e.deferred, deliveryID)
	e.mu.Unlock()
	if err := e.api.Reject(ctx, deliveryID, protocol.RejectUserDeclined); err != nil {
		return err
	}
	// Empty ct / 0 size keep the pending "待确认" row's real type & size on update.
	e.recordKeyed(deliveryID, "download", "", 0, StatusIgnored, DetailIgnoredUnconfirmed)
	return nil
}

// handleDelivery is HandleDelivery's core; force bypasses the auto-download
// threshold (used by ConfirmDownload).
func (e *Engine) handleDelivery(ctx context.Context, deliveryID string, force bool) error {
	dir, eff := e.policy()
	if dir == protocol.DirectionUploadOnly {
		// We don't accept inbound content in upload-only mode.
		_ = e.api.Reject(ctx, deliveryID, protocol.RejectPolicyBlocked)
		e.recordKeyed(deliveryID, "download", protocol.ContentText, 0, StatusIgnored, DetailUploadOnlyReject)
		return nil
	}

	m, err := e.api.GetDelivery(ctx, deliveryID)
	if err != nil {
		return err
	}
	if !knownContentType(m.ContentType) {
		_ = e.api.Reject(ctx, deliveryID, protocol.RejectPolicyBlocked)
		e.recordKeyed(deliveryID, "download", m.ContentType, m.CiphertextSizeBytes, StatusIgnored, DetailUnknownType)
		return nil
	}
	// File delivery needs a destination; reject if none configured.
	if m.ContentType == protocol.ContentFile && e.files == nil {
		_ = e.api.Reject(ctx, deliveryID, protocol.RejectPolicyBlocked)
		e.recordKeyed(deliveryID, "download", m.ContentType, m.CiphertextSizeBytes, StatusIgnored, DetailNoRecvDir)
		return nil
	}
	// Over the auto-download threshold needs user confirmation; defer it (leave
	// pending) and raise an actionable notification rather than auto-pulling.
	if !force && eff != nil && m.CiphertextSizeBytes > eff.MaxAutoDownloadSizeBytes {
		e.mu.Lock()
		seen := e.deferred[deliveryID]
		e.deferred[deliveryID] = true
		hook := e.onConfirm
		e.mu.Unlock()
		if !seen {
			e.recordKeyed(deliveryID, "download", m.ContentType, m.CiphertextSizeBytes, StatusIgnored, DetailOverAutoDownload)
			if hook != nil {
				hook(ConfirmRequest{ID: deliveryID, Kind: "download", ContentType: m.ContentType, SizeBytes: m.CiphertextSizeBytes})
			}
		}
		return nil
	}

	wrapped, err := base64.StdEncoding.DecodeString(m.WrappedDEK)
	if err != nil {
		_ = e.api.Reject(ctx, deliveryID, protocol.RejectDecryptFailed)
		return err
	}
	hdr := e.header(m.ItemID, m.SourceDeviceID, m.ContentType)
	dek, err := e2ee.UnwrapDEK(e.id.PrivateKey, wrapped, hdr, e.id.DeviceID)
	if err != nil {
		_ = e.api.Reject(ctx, deliveryID, protocol.RejectDecryptFailed)
		e.recordKeyed(deliveryID, "download", m.ContentType, m.CiphertextSizeBytes, StatusFailed, DetailUnwrapFailed)
		return err
	}

	// Decrypt the sealed metadata (filename, dimensions), if present.
	var meta Metadata
	if m.EncryptedMetadata != "" {
		if sealed, derr := base64.StdEncoding.DecodeString(m.EncryptedMetadata); derr == nil {
			if raw, oerr := e2ee.OpenMetadata(dek, hdr, sealed); oerr == nil {
				_ = json.Unmarshal(raw, &meta)
			}
		}
	}

	body, err := e.api.DownloadContent(ctx, deliveryID)
	if err != nil {
		return err
	}
	defer body.Close()

	summary, err := e.writeBack(ctx, deliveryID, m, dek, hdr, meta, body)
	if err != nil {
		return err
	}
	if err := e.api.Ack(ctx, deliveryID); err != nil {
		return err
	}
	e.recordKeyed(deliveryID, "download", m.ContentType, m.CiphertextSizeBytes, StatusOK, "", summary)
	return nil
}

// writeBack decrypts the body stream and delivers it to the right destination by
// content type, returning a local-only history summary. Files stream straight to
// disk; text/image buffer (modest size).
func (e *Engine) writeBack(ctx context.Context, deliveryID string, m *protocol.DeliveryManifest, dek []byte, hdr e2ee.Header, meta Metadata, body io.Reader) (string, error) {
	reject := func() { _ = e.api.Reject(ctx, deliveryID, protocol.RejectDecryptFailed) }

	if m.ContentType == protocol.ContentFile {
		// Stream decrypt → temp file (never buffers the whole file in memory).
		pr, pw := io.Pipe()
		go func() {
			err := e2ee.DecryptStream(pw, body, dek, hdr, m.ChunkSizeBytes, m.TotalChunks)
			_ = pw.CloseWithError(err)
		}()
		name := meta.Filename
		if name == "" {
			name = "received.bin"
		}
		path, err := e.files.Save(name, pr)
		if err != nil {
			reject()
			e.recordKeyed(deliveryID, "download", m.ContentType, m.CiphertextSizeBytes, StatusFailed, DetailSaveFailed)
			return "", err
		}
		// Record the saved path so the file watcher won't re-upload it, then place
		// the file on the clipboard so the user can paste it (best-effort).
		e.loopback.remember(fingerprint(path))
		_ = e.clip.WriteFile(path)
		return summarizeFilename(name), nil
	}

	// Text, rich text and image: decrypt into a buffer, then route.
	var plain bytes.Buffer
	if err := e2ee.DecryptStream(&plain, body, dek, hdr, m.ChunkSizeBytes, m.TotalChunks); err != nil {
		reject()
		e.recordKeyed(deliveryID, "download", m.ContentType, m.CiphertextSizeBytes, StatusFailed, DetailDecryptFailed)
		return "", err
	}
	switch m.ContentType {
	case protocol.ContentImage:
		// Record fingerprint before writing so the image monitor won't re-upload it.
		// Pixel-based so it matches the re-encoded bytes the OS hands back on re-read.
		e.loopback.remember(imageFingerprint(plain.Bytes()))
		if err := e.clip.WriteImage(plain.Bytes()); err != nil {
			reject()
			return "", err
		}
		return summarizeImage(meta.Width, meta.Height), nil
	case protocol.ContentRichText:
		// The decrypted body is the rich payload (RTF/HTML); meta carries the
		// format and the plain-text fallback. Suppress both forms from re-upload.
		rich := plain.Bytes()
		e.loopback.remember(fingerprintBytes(rich))
		if meta.PlainText != "" {
			e.loopback.remember(fingerprint(meta.PlainText))
		}
		if err := e.clip.WriteRichText(meta.RichFormat, rich, meta.PlainText); err != nil {
			reject()
			return "", err
		}
		if s := summarizeText(meta.PlainText); s != "" {
			return s, nil
		}
		return "(富文本)", nil
	case protocol.ContentText:
		text := plain.String()
		// Record the fingerprint BEFORE writing so the monitor won't re-upload it.
		e.loopback.remember(fingerprint(text))
		if err := e.clip.WriteText(text); err != nil {
			reject()
			return "", err
		}
		return summarizeText(text), nil
	}
	return "", nil
}

// knownContentType reports whether ct is a content type this client understands.
func knownContentType(ct protocol.ContentType) bool {
	switch ct {
	case protocol.ContentText, protocol.ContentImage, protocol.ContentFile, protocol.ContentRichText:
		return true
	}
	return false
}

// SyncPending fetches and processes all pending deliveries (called on connect and
// after a delivery.created notification). Per-delivery errors are recorded but do
// not abort the batch.
func (e *Engine) SyncPending(ctx context.Context) error {
	pending, err := e.api.GetPending(ctx)
	if err != nil {
		return err
	}
	var firstErr error
	for _, d := range pending.Deliveries {
		if err := e.HandleDelivery(ctx, d.DeliveryID); err != nil && firstErr == nil {
			firstErr = err
		}
	}
	return firstErr
}

// allowsType reports whether a content type is in the allowed list.
func allowsType(types []protocol.ContentType, ct protocol.ContentType) bool {
	for _, t := range types {
		if t == ct {
			return true
		}
	}
	return false
}

// PeerMismatch 描述一台公钥指纹与 TOFU 缓存不一致、同步被阻断的对端设备。
// 「对端可能已重新配对(合法换钥)」与「服务端替换公钥(攻击)」都表现为此状态，
// 必须由用户跨设备重新互验后手动决定是否信任(prd/03 §5.4)。
type PeerMismatch struct {
	DeviceID   string
	DeviceName string
	TrustedFP  string // 本机缓存过的指纹
	NewFP      string // 服务端当前下发公钥的指纹
}

// trustTarget applies TOFU: first sighting of a target key is trusted and cached;
// a later mismatch is blocked (returns false) until the user re-verifies.
// deviceName 仅用于失配告警展示。
func (e *Engine) trustTarget(deviceID, deviceName, publicKeyB64 string) bool {
	fp := protocol.KeyFingerprint(publicKeyB64)
	e.mu.Lock()
	known, ok := e.tofu[deviceID]
	if !ok {
		// 首次见到该设备:信任并缓存(TOFU),同步持久化。
		e.tofu[deviceID] = fp
		snapshot, hook := e.tofuSnapshotLocked()
		e.mu.Unlock()
		e.persistTOFU(snapshot, hook)
		return true
	}
	if known == fp {
		// 指纹恢复一致(如用户在别处已处理),清除历史告警。
		delete(e.mismatches, deviceID)
		e.mu.Unlock()
		return true
	}
	// 失配:登记/更新持续告警条目并阻断。
	e.mismatches[deviceID] = &PeerMismatch{DeviceID: deviceID, DeviceName: deviceName, TrustedFP: known, NewFP: fp}
	e.mu.Unlock()
	return false
}

// SeedTOFU 用持久化的缓存预置 TOFU 表(启动时由宿主调用,覆盖同名条目)。
func (e *Engine) SeedTOFU(peers map[string]string) {
	e.mu.Lock()
	for id, fp := range peers {
		e.tofu[id] = fp
	}
	e.mu.Unlock()
}

// SetTOFUPersist 注册 TOFU 缓存变化时的持久化回调(off-lock、携带副本)。
func (e *Engine) SetTOFUPersist(fn func(map[string]string)) {
	e.mu.Lock()
	e.onTOFUChange = fn
	e.mu.Unlock()
}

// tofuSnapshotLocked 在持锁状态下复制 TOFU 表并取出回调。
func (e *Engine) tofuSnapshotLocked() (map[string]string, func(map[string]string)) {
	snapshot := make(map[string]string, len(e.tofu))
	for k, v := range e.tofu {
		snapshot[k] = v
	}
	return snapshot, e.onTOFUChange
}

// persistTOFU 调用持久化回调(可为 nil)。
func (e *Engine) persistTOFU(snapshot map[string]string, hook func(map[string]string)) {
	if hook != nil {
		hook(snapshot)
	}
}

// PeerMismatches 返回当前全部被阻断的对端指纹失配(供 UI 持续告警)。
func (e *Engine) PeerMismatches() []PeerMismatch {
	e.mu.Lock()
	defer e.mu.Unlock()
	out := make([]PeerMismatch, 0, len(e.mismatches))
	for _, m := range e.mismatches {
		out = append(out, *m)
	}
	return out
}

// TrustPeer 在用户跨设备重新互验后,手动信任某对端当前的新指纹并解除阻断。
// 只能信任「已登记失配」的设备,防止误调用扩大信任面。
func (e *Engine) TrustPeer(deviceID string) bool {
	e.mu.Lock()
	m, ok := e.mismatches[deviceID]
	if !ok {
		e.mu.Unlock()
		return false
	}
	e.tofu[deviceID] = m.NewFP
	delete(e.mismatches, deviceID)
	snapshot, hook := e.tofuSnapshotLocked()
	e.mu.Unlock()
	e.persistTOFU(snapshot, hook)
	return true
}

// TrustedFingerprint returns the cached fingerprint for a device (for the UI).
func (e *Engine) TrustedFingerprint(deviceID string) (string, bool) {
	e.mu.Lock()
	defer e.mu.Unlock()
	fp, ok := e.tofu[deviceID]
	return fp, ok
}

// record appends a one-shot session history event. See recordKeyed.
func (e *Engine) record(direction string, ct protocol.ContentType, size int64, status, detail string, summary ...string) {
	e.recordKeyed("", direction, ct, size, status, detail, summary...)
}

// recordKeyed records a history event (capped to a recent window) and invokes the
// event hook off-lock. When id is non-empty and an earlier entry already carries it
// (the over-threshold "待确认" row), that entry is updated in place so its final
// outcome replaces the pending row instead of spawning a second one. On update,
// empty ct / zero size / empty summary keep the pending row's original values (e.g.
// discard has no manifest). status is StatusOK | StatusIgnored | StatusFailed; the
// optional summary is a local-only content preview (never uploaded).
func (e *Engine) recordKeyed(id, direction string, ct protocol.ContentType, size int64, status, detail string, summary ...string) {
	sum := ""
	if len(summary) > 0 {
		sum = summary[0]
	}
	e.mu.Lock()
	var ev Event
	if idx := e.findEventIndex(id); idx >= 0 {
		ev = e.history[idx]
		ev.At, ev.Direction, ev.OK, ev.Status, ev.Detail = e.now(), direction, status == StatusOK, status, detail
		if ct != "" {
			ev.ContentType = ct
		}
		if size > 0 {
			ev.SizeBytes = size
		}
		if sum != "" {
			ev.Summary = sum
		}
		e.history[idx] = ev
	} else {
		ev = Event{At: e.now(), Direction: direction, ContentType: ct, SizeBytes: size, OK: status == StatusOK, Status: status, Detail: detail, Summary: sum, id: id}
		e.history = append(e.history, ev)
		if len(e.history) > 200 {
			e.history = e.history[len(e.history)-200:]
		}
	}
	hook := e.onEvent
	e.mu.Unlock()
	if hook != nil {
		hook(ev)
	}
}

// findEventIndex returns the index of the most recent history entry keyed by id, or
// -1 when none (or id is empty — one-shot events never match). Caller holds e.mu.
func (e *Engine) findEventIndex(id string) int {
	if id == "" {
		return -1
	}
	for i := len(e.history) - 1; i >= 0; i-- {
		if e.history[i].id == id {
			return i
		}
	}
	return -1
}

// History returns a copy of the current session's sync history.
func (e *Engine) History() []Event {
	e.mu.Lock()
	defer e.mu.Unlock()
	out := make([]Event, len(e.history))
	copy(out, e.history)
	return out
}

// SyncCount returns the number of successful syncs this session.
func (e *Engine) SyncCount() int {
	e.mu.Lock()
	defer e.mu.Unlock()
	n := 0
	for _, ev := range e.history {
		if ev.OK {
			n++
		}
	}
	return n
}

// SyncCounts returns the number of successful uploads and downloads this session.
func (e *Engine) SyncCounts() (up, down int) {
	e.mu.Lock()
	defer e.mu.Unlock()
	for _, ev := range e.history {
		if !ev.OK {
			continue
		}
		if ev.Direction == "upload" {
			up++
		} else if ev.Direction == "download" {
			down++
		}
	}
	return up, down
}

// Package guiservice is the application-logic layer bound to the desktop
// frontend. It owns the runtime lifecycle (credentials, sync engine, clipboard
// monitor and WSS connection) and exposes JSON-friendly methods the React UI
// calls. It is deliberately Wails-agnostic: it pushes updates through an injected
// emit callback so it stays unit-testable.
package guiservice

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"time"

	"github.com/gorilla/websocket"
	"github.com/mokeyjay/clipbridge/client/internal/apiclient"
	"github.com/mokeyjay/clipbridge/client/internal/autostart"
	"github.com/mokeyjay/clipbridge/client/internal/clipboardadapter"
	"github.com/mokeyjay/clipbridge/client/internal/credstore"
	"github.com/mokeyjay/clipbridge/client/internal/engine"
	"github.com/mokeyjay/clipbridge/client/internal/filestore"
	"github.com/mokeyjay/clipbridge/client/internal/pairing"
	"github.com/mokeyjay/clipbridge/shared/protocol"
)

// SizeSettingDTO is a per-field inherit/override size policy for the UI: when
// Inherit is true the device uses the account default (shown as InheritedBytes);
// otherwise OverrideBytes applies locally.
type SizeSettingDTO struct {
	Inherit        bool  `json:"inherit"`
	OverrideBytes  int64 `json:"override_bytes"`  // local override (0 when inheriting)
	InheritedBytes int64 `json:"inherited_bytes"` // resolved account default (for placeholder)
}

// TypesSettingDTO is the inherit/override allowed-content-types policy for the UI.
type TypesSettingDTO struct {
	Inherit   bool     `json:"inherit"`
	Override  []string `json:"override"`  // local override (nil when inheriting)
	Inherited []string `json:"inherited"` // resolved account default
}

// PeerMismatchDTO 是一台公钥指纹失配、同步被阻断的对端设备（持续告警用）。
type PeerMismatchDTO struct {
	DeviceID   string `json:"device_id"`
	DeviceName string `json:"device_name"`
	TrustedFP  string `json:"trusted_fingerprint"` // 本机曾信任的指纹
	NewFP      string `json:"new_fingerprint"`     // 服务端当前下发的指纹
}

// StatusDTO is the overview state shown by the frontend.
type StatusDTO struct {
	Paired       bool   `json:"paired"`
	Connected    bool   `json:"connected"`
	Paused       bool   `json:"paused"`
	Direction    string `json:"direction"`
	NotifyPolicy string `json:"notify_policy"`
	ServerURL    string `json:"server_url"`
	ServerName   string `json:"server_name"` // 实例显示名（来自服务端实例设置，随配置刷新）
	ServerFP     string `json:"server_fingerprint"`
	// ServerFPMismatch 表示服务器证书指纹与已固定值不一致、连接已被阻断，
	// 需要用户核对后显式处理（信任新指纹或重新配对），绝不静默接受。
	ServerFPMismatch bool   `json:"server_fp_mismatch"`
	NewServerFP      string `json:"new_server_fingerprint"` // 服务器当前出示的证书指纹
	// PeerMismatches 是公钥指纹失配被阻断的对端设备列表（界面持续告警）。
	PeerMismatches       []PeerMismatchDTO `json:"peer_mismatches"`
	SyncCount            int               `json:"sync_count"`
	UploadCount          int               `json:"upload_count"`   // successful uploads this session
	DownloadCount        int               `json:"download_count"` // successful downloads this session
	LastError            string            `json:"last_error"`
	PermissionWarn       string            `json:"permission_warning"`
	TempDir              string            `json:"temp_dir"`                // local override (empty = default)
	ReceivedDir          string            `json:"received_dir"`            // resolved received-files directory
	FileTTLInherit       bool              `json:"file_ttl_inherit"`        // inherit the account default
	FileTTLDays          int64             `json:"file_ttl_days"`           // local override (0 when inheriting)
	InheritedFileTTLDays int64             `json:"inherited_file_ttl_days"` // account default from effective config
	Autostart            bool              `json:"autostart"`               // launch at login
	Platform             string            `json:"platform"`                // "darwin" | "windows" (drives platform-specific UI)
	WindowsBackdrop      string            `json:"windows_backdrop"`        // Windows 11 窗口材质:mica | acrylic

	// Sync policy: per-field inherit/override (device settings) resolved against
	// the account default. Populated once connected; zero values until then.
	MaxSyncSize  SizeSettingDTO  `json:"max_sync_size"`
	AutoUpload   SizeSettingDTO  `json:"auto_upload"`
	AutoDownload SizeSettingDTO  `json:"auto_download"`
	SyncTypes    TypesSettingDTO `json:"sync_types"`
}

// AboutDTO is the identity/diagnostics state shown on the About tab.
type AboutDTO struct {
	DeviceID       string `json:"device_id"`
	UserID         string `json:"user_id"`
	ServerID       string `json:"server_id"`
	KeyFingerprint string `json:"key_fingerprint"`
	Version        string `json:"version"`
}

// HistoryDTO is one session sync-history row for the UI.
type HistoryDTO struct {
	At          string `json:"at"`
	Direction   string `json:"direction"`
	ContentType string `json:"content_type"`
	SizeBytes   int64  `json:"size_bytes"`
	OK          bool   `json:"ok"`
	Status      string `json:"status"` // "ok" | "ignored" | "failed"
	Detail      string `json:"detail"`
	Summary     string `json:"summary"`
}

// policy field selectors for sizeSetting.
type policyField int

const (
	fieldMaxSync policyField = iota
	fieldAutoUpload
	fieldAutoDownload
)

// sizeSetting projects a device-settings field and the resolved effective config
// into the UI's inherit/override view. When ds is nil (not yet fetched) the field
// reports as inheriting.
func sizeSetting(ds *protocol.DeviceSettings, eff *protocol.EffectiveConfig, f policyField) SizeSettingDTO {
	out := SizeSettingDTO{Inherit: true}
	if ds != nil {
		switch f {
		case fieldMaxSync:
			out.Inherit = ds.MaxSyncSizeInherit
			if v := ds.MaxSyncSizeBytes; v != nil {
				out.OverrideBytes = *v
			}
		case fieldAutoUpload:
			out.Inherit = ds.MaxAutoUploadInherit
			if v := ds.MaxAutoUploadSizeBytes; v != nil {
				out.OverrideBytes = *v
			}
		case fieldAutoDownload:
			out.Inherit = ds.MaxAutoDownloadInherit
			if v := ds.MaxAutoDownloadSizeBytes; v != nil {
				out.OverrideBytes = *v
			}
		}
	}
	if eff != nil {
		switch f {
		case fieldMaxSync:
			out.InheritedBytes = eff.MaxSyncSizeBytes
		case fieldAutoUpload:
			out.InheritedBytes = eff.MaxAutoUploadSizeBytes
		case fieldAutoDownload:
			out.InheritedBytes = eff.MaxAutoDownloadSizeBytes
		}
	}
	return out
}

// typesSetting projects the allowed-types field into the UI's inherit/override view.
func typesSetting(ds *protocol.DeviceSettings, eff *protocol.EffectiveConfig) TypesSettingDTO {
	out := TypesSettingDTO{Inherit: true}
	if ds != nil {
		out.Inherit = ds.AllowedTypesInherit
		out.Override = contentTypesToStrings(ds.AllowedTypes)
	}
	if eff != nil {
		out.Inherited = contentTypesToStrings(eff.AllowedTypes)
	}
	return out
}

// contentTypesToStrings converts protocol content types to plain strings for JSON.
func contentTypesToStrings(in []protocol.ContentType) []string {
	if in == nil {
		return nil
	}
	out := make([]string, 0, len(in))
	for _, c := range in {
		out = append(out, string(c))
	}
	return out
}

// App is the bound service instance.
type App struct {
	version  string
	clip     *clipboardadapter.System
	emit     func(name string, data any)
	notify   func(title, subtitle, body string) error
	pickDir  func() (string, error)
	clipWarn string

	// notifyAction, when set, raises an actionable OS notification (confirm/ignore
	// buttons) for over-threshold confirmations; nil in headless contexts.
	notifyAction func(title, body string, data map[string]string) error
	// onLangChange, when set, rebuilds the tray menu in the given UI language so the
	// menu follows the language switched in the settings window. nil when headless.
	onLangChange func(lang string)
	// applyBackdrop, when set, applies a new Windows window material (mica|acrylic)
	// by recreating the settings window. nil on macOS / headless contexts.
	applyBackdrop func(kind string)

	mu          sync.Mutex
	store       *credstore.Store
	eng         *engine.Engine
	api         *apiclient.Client
	files       *filestore.Store
	devSettings *protocol.DeviceSettings // cached device sync-policy overrides
	paused      bool
	direction   string
	notifyLevel string
	language    string // resolved UI language ("zh" | "en") for the tray menu
	connected   bool
	lastError   string
	// fpMismatch/fpNew:服务器证书指纹失配状态与服务器当前出示的新指纹，
	// 由 connectLoop 检测置位、成功连接或用户显式处理后清除。
	fpMismatch bool
	fpNew      string
	rootCtx    context.Context
	runCancel  context.CancelFunc

	// settings-window bounds (in-memory; flushed to profile on close/quit)
	winX, winY, winW, winH int
	winValid               bool
}

// NewApp builds the service. clip may be nil if the clipboard is unavailable
// (clipWarn then describes the problem). emit pushes events to the frontend;
// notifier sends OS-level notifications (returns an error so failures surface).
// Both may be nil (e.g. headless CLI). pickDir opens a native directory picker
// for the received-files folder and may also be nil (the UI then hides Browse).
func NewApp(version string, clip *clipboardadapter.System, clipWarn string, emit func(name string, data any), notifier func(title, subtitle, body string) error, pickDir func() (string, error)) *App {
	return &App{version: version, clip: clip, clipWarn: clipWarn, emit: emit, notify: notifier, pickDir: pickDir}
}

// TestNotification sends a system-level test notification, returning an error so
// the UI can explain why nothing appeared (e.g. permission denied / unsupported).
func (a *App) TestNotification() error {
	if a.notify == nil {
		return errors.New("此环境不支持系统通知")
	}
	// The OS shows the app name (剪驿) as the notification header, so the title here
	// is just the message.
	return a.notify("测试通知", "", "如果你看到这条系统通知，说明剪驿的通知已正常工作。")
}

// sendNotify delivers an OS notification if a notifier is wired (errors ignored).
func (a *App) sendNotify(title, subtitle, body string) {
	if a.notify != nil {
		_ = a.notify(title, subtitle, body)
	}
}

// Notification category/action identifiers shared with the host (main.go), which
// registers the category and routes responses back via HandleNotificationResponse.
const (
	NotifyCategoryConfirm = "cb-confirm"
	NotifyActionConfirm   = "cb-confirm-yes"
	NotifyActionIgnore    = "cb-confirm-no"
)

// SetActionNotifier wires the actionable (confirm/ignore) notifier. The callback
// receives the title, body and a data map carrying the pending item's kind and id,
// which come back in HandleNotificationResponse when the user taps a button.
//
//wails:ignore
func (a *App) SetActionNotifier(fn func(title, body string, data map[string]string) error) {
	a.mu.Lock()
	a.notifyAction = fn
	a.mu.Unlock()
}

// Language returns the stored UI language for the tray menu ("zh" | "en"),
// defaulting to Chinese.
func (a *App) Language() string {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.language == "en" {
		return "en"
	}
	return "zh"
}

// SetLanguage records the resolved UI language (pushed by the settings window),
// persists it to the profile, and rebuilds the tray menu so it follows the
// language switch. The frontend i18n provider calls this on load and on change.
func (a *App) SetLanguage(lang string) {
	if lang != "en" {
		lang = "zh"
	}
	a.mu.Lock()
	changed := a.language != lang
	a.language = lang
	store, relabel := a.store, a.onLangChange
	a.mu.Unlock()
	if store != nil {
		if p, err := store.LoadProfile(); err == nil {
			p.Language = lang
			_ = store.SaveProfile(p)
		}
	}
	if changed && relabel != nil {
		relabel(lang)
	}
}

// SetTrayRelabeler wires the callback that rebuilds the tray menu in a given
// language. Set by the host (main.go); nil in headless contexts.
//
//wails:ignore
func (a *App) SetTrayRelabeler(fn func(lang string)) {
	a.mu.Lock()
	a.onLangChange = fn
	a.mu.Unlock()
}

// onEngineEvent drives notifications from sync-history events per the notify
// policy. Over-threshold confirmations are handled separately (onConfirmNeeded);
// here we surface failures (default+) and successes (verbose only). It also pushes
// a fresh status so the overview counters update live.
func (a *App) onEngineEvent(ev engine.Event) {
	a.pushStatus()
	a.mu.Lock()
	level := defaultStr(a.notifyLevel, "default")
	a.mu.Unlock()
	// Titles are plain action phrases; the OS shows the app name (剪驿) as the header.
	switch ev.Status {
	case engine.StatusFailed:
		if level == "default" || level == "verbose" {
			a.sendNotify("同步失败", "", failureBody(ev))
		}
	case engine.StatusOK:
		if level == "verbose" {
			title := "已同步到其他设备"
			if ev.Direction == "download" {
				title = "已接收其他设备的内容"
			}
			a.sendNotify(title, "", describeContent(ev.ContentType, ev.Summary))
		}
	}
}

// onConfirmNeeded raises an actionable notification asking the user to confirm or
// ignore an over-threshold item. Confirmations notify at every policy level (they
// are the only way the held content ever syncs). Falls back to a plain
// notification when no actionable notifier is available.
func (a *App) onConfirmNeeded(c engine.ConfirmRequest) {
	a.mu.Lock()
	fn := a.notifyAction
	a.mu.Unlock()
	// Concise: state the item and its size; the 同步 / 忽略 action buttons carry the
	// choice. Title differs by direction so the user knows which way it would go.
	desc := describeContent(c.ContentType, c.Summary)
	title, limit := "较大内容待同步", "自动同步上限"
	if c.Kind == "download" {
		title, limit = "较大内容待接收", "自动接收上限"
	}
	body := desc + " 约 " + humanSize(c.SizeBytes) + "，超过" + limit + "。"
	if fn == nil {
		a.sendNotify(title, "", body)
		return
	}
	if err := fn(title, body, map[string]string{"kind": c.Kind, "id": c.ID}); err != nil {
		a.sendNotify(title, "", body)
	}
}

// HandleNotificationResponse routes a notification button tap back to the engine:
// confirm sends/pulls the held item, anything else discards it. data carries the
// item's kind ("upload"|"download") and id.
func (a *App) HandleNotificationResponse(action string, data map[string]string) {
	id := data["id"]
	if id == "" {
		return
	}
	kind := data["kind"]
	confirm := action == NotifyActionConfirm
	a.mu.Lock()
	eng, ctx := a.eng, a.rootCtx
	a.mu.Unlock()
	if eng == nil {
		return
	}
	go func() {
		switch kind {
		case "upload":
			if confirm {
				_ = eng.ConfirmUpload(ctx, id)
			} else {
				eng.DiscardUpload(id)
			}
		case "download":
			if confirm {
				_ = eng.ConfirmDownload(ctx, id)
			} else {
				_ = eng.DiscardDownload(ctx, id)
			}
		}
		a.pushStatus()
	}()
}

// contentTypeZh renders a content type's Chinese name.
func contentTypeZh(ct protocol.ContentType) string {
	switch ct {
	case protocol.ContentText:
		return "文本"
	case protocol.ContentImage:
		return "图片"
	case protocol.ContentFile:
		return "文件"
	case protocol.ContentRichText:
		return "富文本"
	}
	return "内容"
}

// describeContent renders a sync item as a natural Chinese phrase for notifications
// — 文件「report.pdf」/ 图片 1920×1080 / 文本「hello…world」/ 富文本「…」 — falling
// back to the bare type name when there is no preview summary.
func describeContent(ct protocol.ContentType, summary string) string {
	name := contentTypeZh(ct)
	if summary == "" {
		return name
	}
	// The image summary already reads "图片 1920×1080"; show it verbatim.
	if ct == protocol.ContentImage {
		return summary
	}
	return name + "「" + summary + "」"
}

// failureBody builds a failure notification body: the content phrase plus the
// specific reason, e.g. 文件「report.pdf」· 上传失败. ev.Detail is a stable code;
// notifications are Chinese-only, so it's mapped via detailZh.
func failureBody(ev engine.Event) string {
	s := describeContent(ev.ContentType, ev.Summary)
	if d := detailZh(ev.Detail); d != "" {
		return s + " · " + d
	}
	return s
}

// detailZh maps an engine sync-detail code to Chinese for notifications (which are
// Chinese-only). The Web/desktop UI localizes the same codes via i18n. Unknown or
// empty codes return "".
func detailZh(code string) string {
	switch code {
	case engine.DetailDownloadOnlySkip:
		return "仅下载模式，已跳过上传"
	case engine.DetailTypeNotAllowed:
		return "本机策略不允许该类型同步"
	case engine.DetailOverMaxSize:
		return "超过最大同步尺寸，已跳过"
	case engine.DetailOverAutoUpload:
		return "超过自动上传阈值，待确认"
	case engine.DetailTargetsQueryFailed:
		return "目标查询失败"
	case engine.DetailTargetFpMismatch:
		return "目标公钥指纹不一致，已阻断"
	case engine.DetailNoTrustedTarget:
		return "无可信在线目标"
	case engine.DetailUploadFailed:
		return "上传失败"
	case engine.DetailConfirmTimeout:
		return "确认超时，已跳过"
	case engine.DetailIgnoredUnconfirmed:
		return "已忽略（未确认）"
	case engine.DetailUploadOnlyReject:
		return "仅上传模式，已拒绝"
	case engine.DetailUnknownType:
		return "未知内容类型"
	case engine.DetailNoRecvDir:
		return "未配置接收文件目录"
	case engine.DetailOverAutoDownload:
		return "超过自动下载阈值，待确认"
	case engine.DetailUnwrapFailed:
		return "解封密钥失败"
	case engine.DetailDecryptFailed:
		return "解密失败"
	case engine.DetailSaveFailed:
		return "保存文件失败"
	}
	return ""
}

// humanSize renders a byte count as a compact human-readable size.
func humanSize(n int64) string {
	const unit = 1024
	if n < unit {
		return fmt.Sprintf("%d B", n)
	}
	div, exp := int64(unit), 0
	for v := n / unit; v >= unit; v /= unit {
		div *= unit
		exp++
	}
	return fmt.Sprintf("%.1f %ciB", float64(n)/float64(div), "KMGT"[exp])
}

// Boot loads credentials and, if already paired, starts the sync runtime.
func (a *App) Boot(ctx context.Context, store *credstore.Store) {
	a.mu.Lock()
	a.store = store
	a.rootCtx = ctx
	if p, err := store.LoadProfile(); err == nil {
		a.paused = p.Paused
		a.direction = p.SyncDirection
		a.notifyLevel = p.NotifyPolicy
		a.language = p.Language
	}
	paired := store.IsPaired()
	a.mu.Unlock()
	if paired {
		a.startRuntime()
	}
}

// Pair runs the pairing flow and, on success, starts the runtime. The server
// fingerprint must have been confirmed by the user against the Web pairing page.
func (a *App) Pair(serverURL, fingerprint, code, deviceName string) error {
	a.mu.Lock()
	store, ctx := a.store, a.rootCtx
	a.mu.Unlock()

	_, err := pairing.Run(ctx, store, pairing.Request{
		ServerURL: serverURL, ServerFingerprint: fingerprint, Code: code,
		DeviceName: deviceName, Platform: currentPlatform(), ClientVersion: a.version,
	})
	if err != nil {
		a.setError(err.Error())
		return err
	}
	a.startRuntime()
	return nil
}

// BeginPair makes first contact with a device port and returns the certificate
// fingerprint it presents (uppercase colon-hex), for the user to compare against
// the Web pairing page. No trust is established until ConfirmPair is called.
func (a *App) BeginPair(serverURL string) (string, error) {
	a.mu.Lock()
	ctx := a.rootCtx
	a.mu.Unlock()
	fp, err := apiclient.FetchServerFingerprint(ctx, serverURL)
	if err != nil {
		return "", err
	}
	return fp, nil
}

// SuggestedDeviceName returns the local machine name to pre-fill the pairing form.
func (a *App) SuggestedDeviceName() string {
	name, err := os.Hostname()
	if err != nil || name == "" {
		return "我的设备"
	}
	// Trim the mDNS ".local" suffix macOS appends for a cleaner default.
	return strings.TrimSuffix(name, ".local")
}

// Unpair stops the runtime and erases credentials (destructive reset).
func (a *App) Unpair() error {
	a.stopRuntime()
	a.mu.Lock()
	store := a.store
	a.mu.Unlock()
	if err := store.Reset(); err != nil {
		return err
	}
	a.pushStatus()
	return nil
}

// SetPaused toggles syncing and persists it to the local profile. Pausing stops
// both upload (clipboard monitor) and download (pending pulls); resuming pulls any
// deliveries that queued while paused.
func (a *App) SetPaused(paused bool) {
	a.mu.Lock()
	a.paused = paused
	store := a.store
	eng, ctx := a.eng, a.rootCtx
	a.mu.Unlock()
	if store != nil {
		if p, err := store.LoadProfile(); err == nil {
			p.Paused = paused
			_ = store.SaveProfile(p)
		}
	}
	// On resume, pull anything the server still holds for us (deferred while paused).
	if !paused && eng != nil {
		go func() { _ = eng.SyncPending(ctx) }()
	}
	a.pushStatus()
}

// SetDirection sets the sync direction (bidirectional | upload_only |
// download_only), applies it to the running engine and persists it.
func (a *App) SetDirection(direction string) {
	a.mu.Lock()
	a.direction = direction
	store, eng := a.store, a.eng
	a.mu.Unlock()
	if eng != nil {
		eng.SetDirection(protocol.SyncDirection(direction))
	}
	if store != nil {
		if p, err := store.LoadProfile(); err == nil {
			p.SyncDirection = direction
			_ = store.SaveProfile(p)
		}
	}
	a.pushStatus()
}

// SetNotifyPolicy sets the notification verbosity (quiet | default | verbose).
func (a *App) SetNotifyPolicy(policy string) {
	a.mu.Lock()
	a.notifyLevel = policy
	store := a.store
	a.mu.Unlock()
	if store != nil {
		if p, err := store.LoadProfile(); err == nil {
			p.NotifyPolicy = policy
			_ = store.SaveProfile(p)
		}
	}
	a.pushStatus()
}

// SetTempDir changes the received-files directory (local-only) and persists it.
// An empty dir restores the default. Existing files are not migrated; the new
// store is used for subsequent receives and cleanup.
func (a *App) SetTempDir(dir string) error {
	a.mu.Lock()
	store, eng := a.store, a.eng
	a.mu.Unlock()
	if store == nil {
		return errors.New("尚未就绪")
	}
	p, err := store.LoadProfile()
	if err != nil {
		return err
	}
	p.TempDir = strings.TrimSpace(dir)
	if err := store.SaveProfile(p); err != nil {
		return err
	}
	// Rebuild the file store at the resolved directory and swap it in for the engine.
	if eng != nil {
		fs, ferr := filestore.New(a.receivedDir(p.TempDir), filestore.DefaultTTL)
		if ferr != nil {
			return ferr
		}
		eng.SetFileSink(fs)
		a.mu.Lock()
		a.files = fs
		a.mu.Unlock()
		a.applyFileTTL()
	}
	a.pushStatus()
	return nil
}

// SetFileRetention sets the received-files retention: inherit the account default
// or override it locally with days (1..365). Persisted and applied immediately.
func (a *App) SetFileRetention(inherit bool, days int64) error {
	a.mu.Lock()
	store := a.store
	a.mu.Unlock()
	if store == nil {
		return errors.New("尚未就绪")
	}
	p, err := store.LoadProfile()
	if err != nil {
		return err
	}
	if inherit {
		p.FileTTLOverrideDays = 0
	} else {
		if days < 1 || days > 365 {
			return errors.New("文件有效期需为 1 到 365 天")
		}
		p.FileTTLOverrideDays = days
	}
	if err := store.SaveProfile(p); err != nil {
		return err
	}
	a.applyFileTTL()
	a.pushStatus()
	return nil
}

// refreshDeviceSettings fetches and caches the device's sync-policy overrides.
// Best-effort: failures (e.g. offline) leave the previous cache in place.
func (a *App) refreshDeviceSettings(ctx context.Context) {
	a.mu.Lock()
	api := a.api
	a.mu.Unlock()
	if api == nil {
		return
	}
	if ds, err := api.GetDeviceSettings(ctx); err == nil {
		a.mu.Lock()
		a.devSettings = ds
		a.mu.Unlock()
	}
}

// patchDeviceSettings applies mutate to the current device settings (fetching them
// first if not cached), PATCHes the full object to the server, caches the result,
// refreshes the engine's effective policy and pushes status.
func (a *App) patchDeviceSettings(mutate func(*protocol.DeviceSettings)) error {
	a.mu.Lock()
	api, cur, ctx := a.api, a.devSettings, a.rootCtx
	a.mu.Unlock()
	if api == nil {
		return errors.New("尚未连接服务器，无法修改同步策略")
	}
	var base protocol.DeviceSettings
	if cur != nil {
		base = *cur
	} else {
		got, err := api.GetDeviceSettings(ctx)
		if err != nil {
			return err
		}
		base = *got
	}
	mutate(&base)
	out, err := api.UpdateDeviceSettings(ctx, base)
	if err != nil {
		return err
	}
	a.mu.Lock()
	a.devSettings = out
	eng := a.eng
	a.mu.Unlock()
	if eng != nil {
		_ = eng.RefreshConfig(ctx)
	}
	a.pushStatus()
	return nil
}

// SetMaxSyncSize sets the device's max sync size: inherit the account default or
// override locally with a positive byte count. Content above it is dropped.
func (a *App) SetMaxSyncSize(inherit bool, bytes int64) error {
	if !inherit && bytes <= 0 {
		return errors.New("最大同步尺寸需大于 0")
	}
	return a.patchDeviceSettings(func(ds *protocol.DeviceSettings) {
		ds.MaxSyncSizeInherit = inherit
		ds.MaxSyncSizeBytes = optBytes(inherit, bytes)
	})
}

// SetAutoUploadSize sets the device's auto-upload threshold (inherit or override).
func (a *App) SetAutoUploadSize(inherit bool, bytes int64) error {
	if !inherit && bytes <= 0 {
		return errors.New("最大自动上传尺寸需大于 0")
	}
	return a.patchDeviceSettings(func(ds *protocol.DeviceSettings) {
		ds.MaxAutoUploadInherit = inherit
		ds.MaxAutoUploadSizeBytes = optBytes(inherit, bytes)
	})
}

// SetAutoDownloadSize sets the device's auto-download threshold (inherit or override).
func (a *App) SetAutoDownloadSize(inherit bool, bytes int64) error {
	if !inherit && bytes <= 0 {
		return errors.New("最大自动下载尺寸需大于 0")
	}
	return a.patchDeviceSettings(func(ds *protocol.DeviceSettings) {
		ds.MaxAutoDownloadInherit = inherit
		ds.MaxAutoDownloadSizeBytes = optBytes(inherit, bytes)
	})
}

// SetSyncTypes sets the device's allowed content types: inherit the account default
// or override with a local set (must be non-empty when overriding).
func (a *App) SetSyncTypes(inherit bool, types []string) error {
	if !inherit && len(types) == 0 {
		return errors.New("至少选择一种同步类型")
	}
	return a.patchDeviceSettings(func(ds *protocol.DeviceSettings) {
		ds.AllowedTypesInherit = inherit
		if inherit {
			ds.AllowedTypes = nil
			return
		}
		cts := make([]protocol.ContentType, 0, len(types))
		for _, t := range types {
			cts = append(cts, protocol.ContentType(t))
		}
		ds.AllowedTypes = cts
	})
}

// optBytes returns nil when inheriting, otherwise a pointer to v.
func optBytes(inherit bool, v int64) *int64 {
	if inherit {
		return nil
	}
	return &v
}

// PickReceivedDir opens a native directory picker and, if the user chose a folder,
// applies it as the received-files directory. Returns the chosen path (empty when
// the user cancelled).
func (a *App) PickReceivedDir() (string, error) {
	if a.pickDir == nil {
		return "", errors.New("此环境不支持目录选择")
	}
	dir, err := a.pickDir()
	if err != nil {
		return "", err
	}
	if strings.TrimSpace(dir) == "" {
		return "", nil // cancelled
	}
	if err := a.SetTempDir(dir); err != nil {
		return "", err
	}
	return dir, nil
}

// SetWindowBounds records the settings window's position/size in memory (cheap,
// called on every move/resize). Call PersistWindowBounds to flush it to disk.
func (a *App) SetWindowBounds(x, y, w, h int) {
	a.mu.Lock()
	a.winX, a.winY, a.winW, a.winH, a.winValid = x, y, w, h, true
	a.mu.Unlock()
}

// PersistWindowBounds writes the last in-memory window bounds to the local profile.
func (a *App) PersistWindowBounds() {
	a.mu.Lock()
	store, x, y, w, h, ok := a.store, a.winX, a.winY, a.winW, a.winH, a.winValid
	a.mu.Unlock()
	if store == nil || !ok || w <= 0 || h <= 0 {
		return
	}
	if p, err := store.LoadProfile(); err == nil {
		p.WindowX, p.WindowY, p.WindowWidth, p.WindowHeight = x, y, w, h
		_ = store.SaveProfile(p)
	}
}

// LoadWindowBounds returns the saved settings-window bounds. ok is false when none
// were saved (first launch), so the caller centers the window instead.
func (a *App) LoadWindowBounds() (x, y, w, h int, ok bool) {
	a.mu.Lock()
	store := a.store
	a.mu.Unlock()
	if store == nil {
		return 0, 0, 0, 0, false
	}
	if p, err := store.LoadProfile(); err == nil && p.WindowWidth > 0 && p.WindowHeight > 0 {
		return p.WindowX, p.WindowY, p.WindowWidth, p.WindowHeight, true
	}
	return 0, 0, 0, 0, false
}

// WindowsBackdrop 返回持久化的 Windows 窗口材质偏好（"mica" | "acrylic"），
// 供宿主(main.go)创建窗口时读取。
func (a *App) WindowsBackdrop() string {
	a.mu.Lock()
	store := a.store
	a.mu.Unlock()
	if store != nil {
		if p, err := store.LoadProfile(); err == nil && p.WindowsBackdrop == "acrylic" {
			return "acrylic"
		}
	}
	return "mica"
}

// SetWindowsBackdrop 设置 Windows 11 的窗口材质（mica | acrylic）并持久化；
// 通过宿主回调重建窗口即时生效（Windows 10 仍回退普通窗口）。
func (a *App) SetWindowsBackdrop(kind string) error {
	if kind != "mica" && kind != "acrylic" {
		return errors.New("未知的窗口材质")
	}
	a.mu.Lock()
	store, apply := a.store, a.applyBackdrop
	a.mu.Unlock()
	if store == nil {
		return errors.New("尚未就绪")
	}
	p, err := store.LoadProfile()
	if err != nil {
		return err
	}
	p.WindowsBackdrop = kind
	if err := store.SaveProfile(p); err != nil {
		return err
	}
	if apply != nil {
		apply(kind)
	}
	a.pushStatus()
	return nil
}

// SetBackdropApplier 注册材质变更的宿主回调（重建窗口以应用新材质）。
// 由 main.go 在 Windows 上设置;其他平台/无 GUI 环境为 nil。
//
//wails:ignore
func (a *App) SetBackdropApplier(fn func(kind string)) {
	a.mu.Lock()
	a.applyBackdrop = fn
	a.mu.Unlock()
}

// SetAutostart enables or disables launch-at-login (OS-level) and persists the
// desired state to the local profile.
func (a *App) SetAutostart(enable bool) error {
	if err := autostart.Set(enable); err != nil {
		return err
	}
	a.mu.Lock()
	store := a.store
	a.mu.Unlock()
	if store != nil {
		if p, err := store.LoadProfile(); err == nil {
			p.Autostart = enable
			_ = store.SaveProfile(p)
		}
	}
	a.pushStatus()
	return nil
}

// Status returns the current overview state.
func (a *App) Status() StatusDTO {
	a.mu.Lock()
	defer a.mu.Unlock()
	st := StatusDTO{
		Paired: a.store != nil && a.store.IsPaired(), Connected: a.connected, Paused: a.paused,
		Direction: defaultStr(a.direction, "bidirectional"), NotifyPolicy: defaultStr(a.notifyLevel, "default"),
		LastError: a.lastError, PermissionWarn: a.clipWarn, Platform: runtime.GOOS,
		ServerFPMismatch: a.fpMismatch, NewServerFP: a.fpNew,
		PeerMismatches: []PeerMismatchDTO{},
	}
	if a.eng != nil {
		st.SyncCount = a.eng.SyncCount()
		st.UploadCount, st.DownloadCount = a.eng.SyncCounts()
		// 对端指纹失配的持续告警列表。
		for _, m := range a.eng.PeerMismatches() {
			st.PeerMismatches = append(st.PeerMismatches, PeerMismatchDTO{
				DeviceID: m.DeviceID, DeviceName: m.DeviceName, TrustedFP: m.TrustedFP, NewFP: m.NewFP,
			})
		}
	}
	// Sync-policy settings: device overrides resolved against the account default.
	var eff *protocol.EffectiveConfig
	if a.eng != nil {
		eff = a.eng.EffectiveConfig()
	}
	if eff != nil {
		st.ServerName = eff.ServerName // 实例名随有效配置下发，管理员改名后会刷新
	}
	st.MaxSyncSize = sizeSetting(a.devSettings, eff, fieldMaxSync)
	st.AutoUpload = sizeSetting(a.devSettings, eff, fieldAutoUpload)
	st.AutoDownload = sizeSetting(a.devSettings, eff, fieldAutoDownload)
	st.SyncTypes = typesSetting(a.devSettings, eff)
	// Default received-files dir up front so the field is never blank, even before
	// pairing or when the store failed to open; overridden by the profile below.
	st.ReceivedDir = a.receivedDir("")
	if a.store != nil {
		if id, err := a.store.LoadIdentity(); err == nil {
			st.ServerURL = id.ServerURL
		}
		if fp, err := a.store.LoadServerFingerprint(); err == nil {
			st.ServerFP = fp
		}
		// Received-files directory + retention (local override vs inherited default).
		if p, err := a.store.LoadProfile(); err == nil {
			st.TempDir = p.TempDir
			st.ReceivedDir = a.receivedDir(p.TempDir)
			st.FileTTLDays = p.FileTTLOverrideDays
			st.FileTTLInherit = p.FileTTLOverrideDays == 0
			st.WindowsBackdrop = defaultStr(p.WindowsBackdrop, "mica")
		}
	}
	if a.eng != nil {
		if days, ok := a.eng.EffectiveFileTTLDays(); ok {
			st.InheritedFileTTLDays = days
		}
	}
	if on, err := autostart.Enabled(); err == nil {
		st.Autostart = on
	}
	return st
}

// receivedDir resolves the received-files directory from a profile override or
// the default under the config dir. When the store is unavailable (e.g. very early
// boot), it falls back to the platform default so the UI still shows the effective
// path rather than a blank field.
func (a *App) receivedDir(tempDir string) string {
	if strings.TrimSpace(tempDir) != "" {
		return tempDir
	}
	if a.store != nil {
		return filepath.Join(a.store.Dir(), "received")
	}
	base, err := os.UserConfigDir()
	if err != nil {
		base, _ = os.UserHomeDir()
	}
	return filepath.Join(base, "ClipBridge", "received")
}

// About returns identity and diagnostic info.
func (a *App) About() AboutDTO {
	a.mu.Lock()
	defer a.mu.Unlock()
	out := AboutDTO{Version: a.version}
	if a.store != nil {
		if id, err := a.store.LoadIdentity(); err == nil {
			out.DeviceID, out.UserID, out.ServerID = id.DeviceID, id.UserID, id.ServerID
			out.KeyFingerprint = protocol.KeyFingerprint(id.PublicKeyB64)
		}
	}
	return out
}

// RecentHistory returns the current session's sync history (newest last).
func (a *App) RecentHistory() []HistoryDTO {
	a.mu.Lock()
	eng := a.eng
	a.mu.Unlock()
	if eng == nil {
		return []HistoryDTO{}
	}
	var out []HistoryDTO
	for _, e := range eng.History() {
		out = append(out, HistoryDTO{
			At: e.At.Format(time.RFC3339), Direction: e.Direction, ContentType: string(e.ContentType),
			SizeBytes: e.SizeBytes, OK: e.OK, Status: e.Status, Detail: e.Detail, Summary: e.Summary,
		})
	}
	return out
}

// startRuntime builds the engine from stored credentials and launches the
// clipboard monitor and WSS connection. Safe to call when already running.
func (a *App) startRuntime() {
	a.mu.Lock()
	if a.runCancel != nil { // already running
		a.mu.Unlock()
		return
	}
	store, rootCtx := a.store, a.rootCtx
	a.mu.Unlock()

	id, err := store.LoadIdentity()
	if err != nil {
		a.setError("加载身份失败: " + err.Error())
		return
	}
	token, err := store.LoadToken()
	if err != nil {
		a.setError("加载令牌失败: " + err.Error())
		return
	}
	priv, err := store.LoadPrivateKey()
	if err != nil {
		a.setError("加载私钥失败: " + err.Error())
		return
	}
	fp, err := store.LoadServerFingerprint()
	if err != nil {
		a.setError("加载指纹失败: " + err.Error())
		return
	}

	api := apiclient.New(id.ServerURL, fp)
	api.SetToken(token)
	// Use the real clipboard, or a no-op if the platform clipboard is unavailable
	// (avoids a nil-interface panic on write-back; uploads simply never trigger).
	var clip engine.Clipboard = noopClipboard{}
	if a.clip != nil {
		clip = a.clip
	}
	eng := engine.New(engine.Identity{DeviceID: id.DeviceID, UserID: id.UserID, ServerID: id.ServerID, PrivateKey: priv}, api, clip)
	// Drive notifications + live UI from sync events, and route over-threshold
	// items through the confirm/ignore notification flow.
	eng.SetEventHook(a.onEngineEvent)
	eng.SetConfirmHook(a.onConfirmNeeded)
	// TOFU 持久化：启动时载入已知对端指纹，之后每次变化落盘（best-effort），
	// 使「换钥告警」跨重启保持（prd/03 §5.4）。
	if peers, perr := store.LoadKnownPeers(); perr == nil {
		eng.SeedTOFU(peers)
	}
	eng.SetTOFUPersist(func(peers map[string]string) { _ = store.SaveKnownPeers(peers) })
	// Apply the local sync direction + resolve the received-files directory and
	// retention from the profile (TTL inherits the account default until the first
	// effective-config fetch, then applyFileTTL adjusts it).
	dir := filepath.Join(store.Dir(), "received")
	initialTTL := filestore.DefaultTTL
	if p, perr := store.LoadProfile(); perr == nil {
		eng.SetDirection(protocol.SyncDirection(p.SyncDirection))
		dir = a.receivedDir(p.TempDir)
		if p.FileTTLOverrideDays > 0 {
			initialTTL = time.Duration(p.FileTTLOverrideDays) * 24 * time.Hour
		}
	}
	if fs, ferr := filestore.New(dir, initialTTL); ferr == nil {
		eng.SetFileSink(fs)
		a.files = fs
	}

	ctx, cancel := context.WithCancel(rootCtx)
	a.mu.Lock()
	a.api, a.eng, a.runCancel = api, eng, cancel
	a.mu.Unlock()

	if a.clip != nil {
		go a.monitorClipboard(ctx)
	}
	go a.cleanupReceivedFiles(ctx)
	go a.connectLoop(ctx)
	a.pushStatus()
}

// stopRuntime cancels the runtime goroutines.
func (a *App) stopRuntime() {
	a.mu.Lock()
	cancel := a.runCancel
	a.runCancel, a.api, a.eng, a.connected = nil, nil, nil, false
	a.mu.Unlock()
	if cancel != nil {
		cancel()
	}
}

// monitorClipboard consumes typed clipboard changes from the platform watcher
// and publishes each kind (unless paused). One source feeds text, image, rich
// text and file events; rich text and files are macOS-only.
func (a *App) monitorClipboard(ctx context.Context) {
	ch := a.clip.WatchClipboard(ctx)
	for {
		select {
		case <-ctx.Done():
			return
		case ev, ok := <-ch:
			if !ok {
				return
			}
			a.mu.Lock()
			paused, eng := a.paused, a.eng
			a.mu.Unlock()
			if paused || eng == nil {
				continue
			}
			if err := a.dispatchClip(ctx, eng, ev); err != nil {
				a.setError(err.Error())
			}
			a.pushStatus()
		}
	}
}

// dispatchClip routes one clipboard event to the matching engine publish entry.
func (a *App) dispatchClip(ctx context.Context, eng *engine.Engine, ev clipboardadapter.ClipEvent) error {
	switch ev.Kind {
	case clipboardadapter.KindText:
		return eng.OnClipboardChanged(ctx, ev.Text)
	case clipboardadapter.KindImage:
		return eng.OnImageChanged(ctx, ev.Image)
	case clipboardadapter.KindRichText:
		return eng.OnRichTextChanged(ctx, ev.RichFormat, ev.Rich, ev.Plain)
	case clipboardadapter.KindFile:
		return eng.OnFileChanged(ctx, ev.FilePath)
	}
	return nil
}

// pullPending pulls and processes pending deliveries unless syncing is paused, so
// pause stops downloads as well as uploads.
func (a *App) pullPending(ctx context.Context, eng *engine.Engine) {
	a.mu.Lock()
	paused := a.paused
	a.mu.Unlock()
	if paused {
		return
	}
	_ = eng.SyncPending(ctx)
}

// cleanupReceivedFiles prunes the received-files temp directory on start and
// hourly. It reads the current store each tick so a temp-dir change takes effect.
func (a *App) cleanupReceivedFiles(ctx context.Context) {
	prune := func() {
		a.mu.Lock()
		fs, eng := a.files, a.eng
		a.mu.Unlock()
		if fs != nil {
			_, _ = fs.Cleanup(time.Now())
		}
		// Drop over-threshold uploads the user never confirmed within the window.
		if eng != nil {
			eng.SweepPendingUploads(time.Now())
		}
	}
	prune()
	ticker := time.NewTicker(time.Hour)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			prune()
		}
	}
}

// applyFileTTL sets the received-files retention: the local override when set,
// else the inherited account default (falling back to 7 days).
func (a *App) applyFileTTL() {
	a.mu.Lock()
	fs, eng, store := a.files, a.eng, a.store
	a.mu.Unlock()
	if fs == nil || store == nil {
		return
	}
	days := int64(7)
	if p, err := store.LoadProfile(); err == nil && p.FileTTLOverrideDays > 0 {
		days = p.FileTTLOverrideDays
	} else if eng != nil {
		if d, ok := eng.EffectiveFileTTLDays(); ok && d > 0 {
			days = d
		}
	}
	fs.SetTTL(time.Duration(days) * 24 * time.Hour)
}

// connectLoop maintains the WSS connection, syncing pending deliveries on connect
// and whenever a delivery.created event arrives. It reconnects with a backoff.
func (a *App) connectLoop(ctx context.Context) {
	for {
		if ctx.Err() != nil {
			return
		}
		a.mu.Lock()
		api, eng := a.api, a.eng
		a.mu.Unlock()
		if api == nil || eng == nil {
			return
		}

		conn, err := api.DialWS(ctx)
		if err != nil {
			a.setConnected(false)
			if apiclient.IsFingerprintMismatch(err) {
				// 证书指纹变化：进入引导式重置状态（阻断 + 展示新旧指纹），
				// 不再用普通「连接失败」文案掩盖。
				a.enterServerFPMismatch(ctx)
			} else {
				a.setError("连接失败: " + err.Error())
			}
			if !sleepCtx(ctx, 5*time.Second) {
				return
			}
			continue
		}
		a.setConnected(true)
		a.setError("")
		a.clearServerFPMismatch()
		// On (re)connect, refresh effective config + device overrides, then pull
		// pending deliveries.
		_ = eng.RefreshConfig(ctx)
		a.refreshDeviceSettings(ctx)
		a.applyFileTTL()
		a.pullPending(ctx, eng)
		a.pushStatus()
		a.readEvents(ctx, conn, eng)
		a.setConnected(false)
		a.pushStatus()
		if !sleepCtx(ctx, 3*time.Second) {
			return
		}
	}
}

// readEvents processes WSS notifications until the connection drops.
func (a *App) readEvents(ctx context.Context, conn *websocket.Conn, eng *engine.Engine) {
	defer conn.Close()
	for {
		var ev protocol.Event
		if err := conn.ReadJSON(&ev); err != nil {
			return
		}
		switch ev.Event {
		case protocol.EventDeliveryCreated:
			a.pullPending(ctx, eng)
			a.pushStatus()
		case protocol.EventConfigChanged:
			// User/instance config changed; re-fetch the effective policy + overrides.
			_ = eng.RefreshConfig(ctx)
			a.refreshDeviceSettings(ctx)
			a.applyFileTTL()
			a.pushStatus()
		case protocol.EventDeviceRevoked:
			a.setError("本设备已被吊销")
			return
		}
	}
}

// enterServerFPMismatch 在检测到服务器证书指纹失配时置位引导状态：取回服务器
// 当前出示的证书指纹供用户与 Web 后台核对，并在首次检测时发一条系统通知。
// 后续重试不重复取指纹/通知，避免刷屏。
func (a *App) enterServerFPMismatch(ctx context.Context) {
	a.mu.Lock()
	already := a.fpMismatch
	a.fpMismatch = true
	serverURL := ""
	if a.store != nil {
		if id, err := a.store.LoadIdentity(); err == nil {
			serverURL = id.ServerURL
		}
	}
	a.mu.Unlock()
	if !already {
		// 首次检测：取服务器当前证书指纹（无 pin 的首联方式，仅用于展示核对）。
		if serverURL != "" {
			if fp, err := apiclient.FetchServerFingerprint(ctx, serverURL); err == nil {
				a.mu.Lock()
				a.fpNew = fp
				a.mu.Unlock()
			}
		}
		a.sendNotify("服务器证书指纹已变化", "", "同步已暂停。请打开剪驿核对新指纹后再决定是否信任。")
	}
	a.setError("服务器证书指纹与已固定值不一致，同步已阻断")
}

// clearServerFPMismatch 在成功建立(通过 pin 校验的)连接后清除失配状态。
func (a *App) clearServerFPMismatch() {
	a.mu.Lock()
	a.fpMismatch, a.fpNew = false, ""
	a.mu.Unlock()
}

// TrustServerFingerprint 在用户与 Web 后台核对一致后，显式信任服务器当前出示
// 的新证书指纹：写入凭据目录并重启同步运行时使新 pin 生效。绝不自动调用。
func (a *App) TrustServerFingerprint() error {
	a.mu.Lock()
	fp, store := a.fpNew, a.store
	a.mu.Unlock()
	if fp == "" {
		return errors.New("当前没有待信任的新指纹")
	}
	if store == nil {
		return errors.New("尚未就绪")
	}
	if err := store.SaveServerFingerprint(fp); err != nil {
		return err
	}
	a.clearServerFPMismatch()
	a.setError("")
	// 重启运行时以让 apiclient 重新按新指纹 pin。
	a.stopRuntime()
	a.startRuntime()
	return nil
}

// TrustPeer 在用户跨设备重新互验后，信任某台对端设备的新公钥指纹并恢复同步。
func (a *App) TrustPeer(deviceID string) error {
	a.mu.Lock()
	eng := a.eng
	a.mu.Unlock()
	if eng == nil {
		return errors.New("同步尚未运行")
	}
	if !eng.TrustPeer(deviceID) {
		return errors.New("该设备当前没有待信任的新指纹")
	}
	a.pushStatus()
	return nil
}

// PeerDTO 是「关于」页公钥互验列表中的一台设备。
type PeerDTO struct {
	DeviceID       string `json:"device_id"`
	Name           string `json:"name"`
	Platform       string `json:"platform"`
	Status         string `json:"status"`
	Online         bool   `json:"online"`
	Self           bool   `json:"self"`
	KeyFingerprint string `json:"key_fingerprint"`
	// TrustState: self | trusted | mismatch | unseen（本机尚未与其同步过）
	TrustState string `json:"trust_state"`
}

// Peers 拉取同用户全部设备及其公钥指纹，并结合本机 TOFU 缓存标注信任状态，
// 供「关于」页做跨设备人工互验（prd/03 §5.3）。
func (a *App) Peers() ([]PeerDTO, error) {
	a.mu.Lock()
	api, eng, ctx := a.api, a.eng, a.rootCtx
	a.mu.Unlock()
	if api == nil {
		return nil, errors.New("尚未连接服务器")
	}
	resp, err := api.GetPeers(ctx)
	if err != nil {
		return nil, err
	}
	out := make([]PeerDTO, 0, len(resp.Peers))
	for _, p := range resp.Peers {
		d := PeerDTO{
			DeviceID: p.DeviceID, Name: p.Name, Platform: p.Platform, Status: p.Status,
			Online: p.Online, Self: p.Self, KeyFingerprint: p.KeyFingerprint, TrustState: "unseen",
		}
		if p.Self {
			d.TrustState = "self"
		} else if eng != nil {
			if fp, ok := eng.TrustedFingerprint(p.DeviceID); ok {
				if fp == p.KeyFingerprint {
					d.TrustState = "trusted"
				} else {
					d.TrustState = "mismatch"
				}
			}
		}
		out = append(out, d)
	}
	return out, nil
}

// setConnected/setError/pushStatus update state and notify the frontend.
func (a *App) setConnected(v bool) {
	a.mu.Lock()
	a.connected = v
	a.mu.Unlock()
}

func (a *App) setError(msg string) {
	a.mu.Lock()
	a.lastError = msg
	a.mu.Unlock()
	a.pushStatus()
}

// pushStatus emits the latest status to the frontend if an emitter is set.
func (a *App) pushStatus() {
	if a.emit != nil {
		a.emit("status", a.Status())
	}
}

// sleepCtx sleeps for d unless ctx is cancelled first; returns false if cancelled.
func sleepCtx(ctx context.Context, d time.Duration) bool {
	select {
	case <-ctx.Done():
		return false
	case <-time.After(d):
		return true
	}
}

// noopClipboard is the engine.Clipboard used when the platform clipboard is
// unavailable: reads are empty and writes are dropped.
type noopClipboard struct{}

func (noopClipboard) ReadText() (string, bool)                   { return "", false }
func (noopClipboard) WriteText(string) error                     { return nil }
func (noopClipboard) WriteImage([]byte) error                    { return nil }
func (noopClipboard) WriteRichText(string, []byte, string) error { return nil }
func (noopClipboard) WriteFile(string) error                     { return nil }

// defaultStr returns fallback when s is empty.
func defaultStr(s, fallback string) string {
	if s == "" {
		return fallback
	}
	return s
}

// currentPlatform maps the build OS to the protocol platform enum.
func currentPlatform() protocol.Platform {
	if runtime.GOOS == "windows" {
		return protocol.PlatformWindows
	}
	return protocol.PlatformDarwin
}

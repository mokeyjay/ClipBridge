// Command clipbridge is the desktop client (macOS and Windows): a tray-resident
// app whose settings window is an embedded React UI. It wires the sync engine
// (clipboard monitor, pairing, E2EE upload/download) behind a bound service and
// applies the platform window material (macOS Liquid Glass / Windows Mica) with
// automatic fallback. Build with wails3.
package main

import (
	"crypto/sha256"
	"embed"
	"fmt"
	"io/fs"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"time"

	"github.com/mokeyjay/clipbridge/client/internal/clipboardadapter"
	"github.com/mokeyjay/clipbridge/client/internal/credstore"
	"github.com/mokeyjay/clipbridge/client/internal/guiservice"
	"github.com/wailsapp/wails/v3/pkg/application"
	"github.com/wailsapp/wails/v3/pkg/events"
	"github.com/wailsapp/wails/v3/pkg/services/notifications"
)

//go:embed all:frontend/dist
var frontendAssets embed.FS

//go:embed assets/logo-alpha-only.png
var trayIcon []byte

//go:embed assets/logo.png
var appIcon []byte

// version is the client version reported to the server and shown on the About tab.
// 发布时由构建脚本通过 -ldflags "-X main.version=<tag>" 注入，本地默认为占位值。
var version = "0.1.0"

func main() {
	// Credential/config directory (overridable for dev), opened with strict perms.
	cfgDir := resolveConfigDir()
	store, permWarn := credstore.Open(cfgDir)

	// System clipboard; a failure is non-fatal and surfaces as a warning.
	clip, clipErr := clipboardadapter.New()
	clipWarn := ""
	if clipErr != nil {
		clipWarn = "剪贴板不可用: " + clipErr.Error()
	}
	if permWarn != nil {
		clipWarn = strings.TrimSpace(clipWarn + " 凭据权限警告: " + permWarn.Error())
	}

	// Frontend pushes happen through the running app's event manager.
	emit := func(name string, data any) {
		if app := application.Get(); app != nil {
			app.Event.Emit(name, data)
		}
	}
	// OS-level notifications via the Wails notifications service. The notifier
	// requests authorization on demand and returns an error so the UI can explain
	// failures (e.g. permission denied, or running an unbundled binary).
	notifService := notifications.New()
	notifier := func(title, subtitle, body string) error {
		// Authorization check can itself fail when running an unbundled binary
		// (no app bundle identity); treat that as "needs the packaged app" rather
		// than surfacing the raw platform error.
		if ok, err := notifService.CheckNotificationAuthorization(); err != nil {
			return fmt.Errorf("系统通知不可用：请以打包后的 ClipBridge.app 运行（开发态裸二进制无法发送系统通知）")
		} else if !ok {
			granted, rerr := notifService.RequestNotificationAuthorization()
			if rerr != nil {
				return fmt.Errorf("系统通知权限申请失败：请在「系统设置 › 通知」中允许 ClipBridge")
			}
			if !granted {
				return fmt.Errorf("系统通知权限未授予：请在「系统设置 › 通知」中允许 ClipBridge")
			}
		}
		// 每条通知必须带唯一且非空的 ID：Wails 的 SendNotification 会校验
		// options.ID（空串直接返回 "notification ID cannot be empty"），且
		// UNUserNotificationCenter 的 identifier 不能为空。用纳秒时间戳保证唯一。
		id := fmt.Sprintf("clipbridge-%d", time.Now().UnixNano())
		if err := notifService.SendNotification(notifications.NotificationOptions{ID: id, Title: title, Subtitle: subtitle, Body: body}); err != nil {
			// 透传底层真实错误，便于排查（而非笼统提示「需打包」）。
			return fmt.Errorf("系统通知发送失败：%w", err)
		}
		return nil
	}
	// Native directory picker for the received-files folder, attached to the main
	// window when present. Returns "" when the user cancels.
	pickDir := func() (string, error) {
		app := application.Get()
		if app == nil {
			return "", fmt.Errorf("应用尚未就绪")
		}
		dlg := app.Dialog.OpenFile().
			CanChooseDirectories(true).
			CanChooseFiles(false).
			CanCreateDirectories(true).
			SetTitle("选择接收文件目录")
		if w, ok := app.Window.GetByName("main"); ok {
			dlg = dlg.AttachToWindow(w)
		}
		return dlg.PromptForSingleSelection()
	}
	svc := guiservice.NewApp(version, clip, clipWarn, emit, notifier, pickDir)

	// Actionable notifications (confirm/ignore buttons) for over-threshold items:
	// register a single confirm category and send notifications carrying the item's
	// kind+id as data, which comes back in the response callback.
	var confirmCategoryOnce sync.Once
	registerConfirmCategory := func() {
		// Registered lazily on first use (after the notification service has started).
		_ = notifService.RegisterNotificationCategory(notifications.NotificationCategory{
			ID: guiservice.NotifyCategoryConfirm,
			Actions: []notifications.NotificationAction{
				{ID: guiservice.NotifyActionConfirm, Title: "同步"},
				{ID: guiservice.NotifyActionIgnore, Title: "忽略", Destructive: true},
			},
		})
	}
	actionNotifier := func(title, body string, data map[string]string) error {
		if ok, err := notifService.CheckNotificationAuthorization(); err != nil || !ok {
			if granted, rerr := notifService.RequestNotificationAuthorization(); rerr != nil || !granted {
				return fmt.Errorf("系统通知权限未授予")
			}
		}
		confirmCategoryOnce.Do(registerConfirmCategory)
		d := make(map[string]any, len(data))
		for k, v := range data {
			d[k] = v
		}
		id := fmt.Sprintf("clipbridge-confirm-%d", time.Now().UnixNano())
		return notifService.SendNotificationWithActions(notifications.NotificationOptions{
			ID: id, Title: title, Body: body, CategoryID: guiservice.NotifyCategoryConfirm, Data: d,
		})
	}
	svc.SetActionNotifier(actionNotifier)
	// Route notification button taps back to the service. UserInfo round-trips the
	// data map we sent; values arrive as strings.
	notifService.OnNotificationResponse(func(result notifications.NotificationResult) {
		if result.Error != nil {
			return
		}
		data := map[string]string{}
		for k, v := range result.Response.UserInfo {
			if s, ok := v.(string); ok {
				data[k] = s
			}
		}
		svc.HandleNotificationResponse(result.Response.ActionIdentifier, data)
	})

	// 单实例锁 ID 掺入配置目录哈希：同一配置目录禁止重复运行（两个进程会抢剪贴板、
	// 共写凭据）；而 dev（CLIPBRIDGE_CONFIG_DIR 隔离）与正式版目录不同，可共存。
	cfgHash := sha256.Sum256([]byte(cfgDir))

	app := application.New(application.Options{
		Name:        "ClipBridge",
		Description: "端到端加密的剪贴板同步",
		Icon:        appIcon,
		Services:    []application.Service{application.NewService(svc), application.NewService(notifService)},
		Assets:      application.AssetOptions{Handler: spaHandler()},
		SingleInstance: &application.SingleInstanceOptions{
			UniqueID: fmt.Sprintf("com.clipbridge.desktop-%x", cfgHash[:8]),
			// 重复启动时把已运行实例的设置窗口带到前台，让用户明白应用已在运行。
			// 回调在后台 goroutine 触发，窗口操作须回到主线程执行。
			OnSecondInstanceLaunch: func(application.SecondInstanceData) {
				application.InvokeSync(func() {
					if app := application.Get(); app != nil {
						openWindow(app, svc)
					}
				})
			},
		},
		Mac: application.MacOptions{
			// Tray-resident: keep running after the window closes, no Dock icon.
			ApplicationShouldTerminateAfterLastWindowClosed: false,
			ActivationPolicy: application.ActivationPolicyAccessory,
		},
		Windows: application.WindowsOptions{
			// Tray-resident on Windows too: don't quit when the window closes.
			DisableQuitOnLastWindowClosed: true,
		},
	})

	// The settings window is created lazily (on first open) and destroyed on close
	// to save memory; the tray opens or recreates it on demand.
	newTray(app, svc)

	// Windows 材质切换：Wails 没有运行时背板 setter，切换偏好后重建窗口即时生效
	// （关窗即销毁，重开会按新偏好创建;位置尺寸经内存 bounds 保留）。
	if runtime.GOOS == "windows" {
		svc.SetBackdropApplier(func(string) {
			if w, ok := app.Window.GetByName("main"); ok {
				svc.PersistWindowBounds()
				w.Close()
				openWindow(app, svc)
			}
		})
	}

	svc.Boot(app.Context(), store)

	// First-run guidance: when not yet paired, open the settings window on launch so
	// the user sees the app is running and can start pairing. Once paired, the app
	// starts silently in the tray (the window then opens on demand). Done on the
	// ApplicationStarted event so the window is created in the running app context.
	if !store.IsPaired() {
		app.Event.OnApplicationEvent(events.Common.ApplicationStarted, func(*application.ApplicationEvent) {
			openWindow(app, svc)
		})
	}

	if err := app.Run(); err != nil {
		log.Fatalf("clipbridge: %v", err)
	}
}

// openWindow shows the settings window, creating it on demand. The WebView is
// loaded only when the user first opens the window; closing it destroys the
// window (default Wails behavior), freeing the WebView until next open. The
// window reopens at its last position/size (svc-persisted), unless that lands on
// a screen that no longer exists or it's the first launch (then centered).
func openWindow(app *application.App, svc *guiservice.App) {
	if w, ok := app.Window.GetByName("main"); ok {
		w.Show()
		w.Focus()
		return
	}
	// Window translucency differs by platform: macOS Liquid Glass needs a
	// transparent background; Windows Mica needs a translucent one.
	bg := application.BackgroundTypeTransparent
	if runtime.GOOS == "windows" {
		bg = application.BackgroundTypeTranslucent
	}
	opts := application.WebviewWindowOptions{
		Name:           "main",
		Title:          "剪驿",
		Width:          720,
		Height:         560,
		MinWidth:       640,
		MinHeight:      520,
		BackgroundType: bg,
		// Windows: no native title bar — the React top bar carries the tabs, status
		// and custom min/max/close controls (macOS uses the hidden-inset toolbar).
		Frameless: runtime.GOOS == "windows",
		URL:       "/",
		Mac: application.MacWindow{
			// Liquid Glass on macOS 15+, automatic translucent fallback otherwise.
			Backdrop:    application.MacBackdropLiquidGlass,
			LiquidGlass: application.MacLiquidGlass{Style: application.LiquidGlassStyleAutomatic},
			// Unified inset toolbar: macOS vertically centers the traffic lights in a
			// taller title region so they line up with the centered tab bar (Safari
			// style). FullSizeContent keeps the WebView full height; the custom top
			// bar is draggable via CSS --wails-draggable (see App.tsx / globals.css).
			TitleBar: application.MacTitleBarHiddenInsetUnified,
		},
		Windows: application.WindowsWindow{
			// Windows 11 默认 Mica、可选 Acrylic（用户偏好持久化在 profile）；
			// 旧版 Windows 自动回退普通窗口。
			BackdropType: winBackdropType(svc.WindowsBackdrop()),
		},
	}
	// Restore the last position/size if it still lands on a visible screen;
	// otherwise center (first launch, or the external display is gone).
	if x, y, ww, hh, ok := svc.LoadWindowBounds(); ok && boundsOnVisibleScreen(app, x, y, ww, hh) {
		// WindowXY (not the zero-value WindowCentered) so the saved X/Y are honored.
		opts.InitialPosition = application.WindowXY
		opts.X, opts.Y, opts.Width, opts.Height = x, y, ww, hh
	} else {
		opts.InitialPosition = application.WindowCentered
	}

	w := app.Window.NewWithOptions(opts)

	// Track position/size: update in memory on move/resize (cheap), flush to disk
	// when the window closes. Seed once so an unmoved window is still remembered.
	// Ignore non-positive sizes (can happen as the window is being torn down) so a
	// good remembered value isn't clobbered.
	save := func() {
		px, py := w.Position()
		sw, sh := w.Size()
		if sw > 0 && sh > 0 {
			svc.SetWindowBounds(px, py, sw, sh)
		}
	}
	save()
	w.OnWindowEvent(events.Common.WindowDidMove, func(*application.WindowEvent) { save() })
	w.OnWindowEvent(events.Common.WindowDidResize, func(*application.WindowEvent) { save() })
	// Persist the last good in-memory bounds as the window closes (the WebView is
	// destroyed on close to save memory, so capture before teardown).
	w.OnWindowEvent(events.Common.WindowClosing, func(*application.WindowEvent) { svc.PersistWindowBounds() })

	w.Show()
	w.Focus()
}

// winBackdropType 把持久化的材质偏好映射为 Wails 的 BackdropType。
// 默认 Mica;仅当显式选择 acrylic 时用 Acrylic。非 Windows 平台该值被忽略。
func winBackdropType(kind string) application.BackdropType {
	if kind == "acrylic" {
		return application.Acrylic
	}
	return application.Mica
}

// boundsOnVisibleScreen reports whether the window's center lies within any
// currently-attached screen, so a remembered position on a now-absent external
// display falls back to centering.
func boundsOnVisibleScreen(app *application.App, x, y, w, h int) bool {
	cx, cy := x+w/2, y+h/2
	for _, s := range app.Screen.GetAll() {
		b := s.Bounds
		if cx >= b.X && cx < b.X+b.Width && cy >= b.Y && cy < b.Y+b.Height {
			return true
		}
	}
	return false
}

// trayLabels are the tray menu strings for one UI language.
type trayLabels struct{ appName, open, pause, quit string }

// trayStrings returns the tray menu labels localized to lang ("zh" | "en"). The app
// name follows the Chinese-name decision: 剪驿 in Chinese, ClipBridge in English.
func trayStrings(lang string) trayLabels {
	if lang == "en" {
		return trayLabels{appName: "ClipBridge", open: "Open ClipBridge", pause: "Pause syncing", quit: "Quit"}
	}
	return trayLabels{appName: "剪驿", open: "打开剪驿", pause: "暂停同步", quit: "退出"}
}

// newTray installs the menu-bar tray: left click opens (or creates) the window,
// the menu exposes open, pause/resume and quit. The menu is built in the current UI
// language and rebuilt whenever the settings window switches language.
func newTray(app *application.App, svc *guiservice.App) {
	tray := app.SystemTray.New()
	// Template icons are a macOS concept (monochrome, auto-tinted); Windows/Linux
	// use the regular colored app icon.
	if runtime.GOOS == "darwin" {
		tray.SetTemplateIcon(trayIcon)
	} else {
		tray.SetIcon(appIcon)
	}

	// buildMenu (re)builds the tray menu + tooltip for a language, preserving the
	// current pause state. SetMenu/SetTooltip dispatch to the main thread internally,
	// so this is safe to call from the bound SetLanguage goroutine.
	buildMenu := func(lang string) {
		s := trayStrings(lang)
		tray.SetTooltip(s.appName)
		menu := app.NewMenu()
		menu.Add(s.open).OnClick(func(*application.Context) { openWindow(app, svc) })
		menu.AddSeparator()
		pauseItem := menu.AddCheckbox(s.pause, svc.Status().Paused)
		pauseItem.OnClick(func(*application.Context) { svc.SetPaused(pauseItem.Checked()) })
		menu.AddSeparator()
		menu.Add(s.quit).OnClick(func(*application.Context) {
			// Persist the window position even when quitting from the tray (the window
			// may never have fired WindowClosing).
			svc.PersistWindowBounds()
			app.Quit()
		})
		tray.SetMenu(menu)
	}
	buildMenu(svc.Language())
	svc.SetTrayRelabeler(buildMenu)
	tray.OnClick(func() { openWindow(app, svc) })
}

// resolveConfigDir returns the credential directory, honoring an env override for
// development, else the platform default (~/Library/Application Support/ClipBridge).
func resolveConfigDir() string {
	if dir := os.Getenv("CLIPBRIDGE_CONFIG_DIR"); dir != "" {
		return dir
	}
	base, err := os.UserConfigDir() // macOS: ~/Library/Application Support
	if err != nil {
		base, _ = os.UserHomeDir()
	}
	return filepath.Join(base, "ClipBridge")
}

// spaHandler serves the embedded React app, falling back to index.html for routes.
func spaHandler() http.Handler {
	sub, err := fs.Sub(frontendAssets, "frontend/dist")
	if err != nil {
		log.Fatalf("clipbridge: embed frontend: %v", err)
	}
	fileServer := http.FileServer(http.FS(sub))
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		name := strings.TrimPrefix(r.URL.Path, "/")
		if name != "" {
			if f, err := sub.Open(name); err == nil {
				_ = f.Close()
				fileServer.ServeHTTP(w, r)
				return
			}
		}
		data, err := fs.ReadFile(sub, "index.html")
		if err != nil {
			http.Error(w, "frontend not built", http.StatusNotFound)
			return
		}
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		http.ServeContent(w, r, "index.html", time.Time{}, strings.NewReader(string(data)))
	})
}

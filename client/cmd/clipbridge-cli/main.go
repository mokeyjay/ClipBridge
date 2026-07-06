// Command clipbridge-cli is a headless, cross-platform ClipBridge sync client. It
// reuses the same sync engine as the desktop app but without the Wails GUI, so it
// cross-compiles cleanly (CGO-free) to Windows for second-device sync testing on a
// single Mac. It pairs interactively (TOFU fingerprint confirmation) then runs the
// clipboard monitor + WSS sync until interrupted.
package main

import (
	"bufio"
	"context"
	"flag"
	"fmt"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"github.com/mokeyjay/clipbridge/client/internal/clipboardadapter"
	"github.com/mokeyjay/clipbridge/client/internal/credstore"
	"github.com/mokeyjay/clipbridge/client/internal/guiservice"
)

const version = "0.1.0"

func main() {
	dataDir := flag.String("data-dir", defaultDir(), "credential/config directory")
	server := flag.String("server", "", "device-port base URL for pairing, e.g. https://host:8443")
	code := flag.String("code", "", "6-digit pairing code (from the Web console)")
	name := flag.String("name", hostName(), "this device's name")
	trust := flag.Bool("trust", false, "auto-trust the server fingerprint without prompting")
	flag.Parse()

	if err := run(*dataDir, *server, *code, *name, *trust); err != nil {
		fmt.Fprintln(os.Stderr, "错误:", err)
		os.Exit(1)
	}
}

func run(dataDir, server, code, name string, trust bool) error {
	store, permWarn := credstore.Open(dataDir)
	clip, clipErr := clipboardadapter.New()
	clipWarn := ""
	if clipErr != nil {
		clipWarn = "剪贴板不可用: " + clipErr.Error()
		fmt.Fprintln(os.Stderr, "警告:", clipWarn)
	}
	if permWarn != nil {
		fmt.Fprintln(os.Stderr, "警告: 凭据权限:", permWarn)
	}

	// emit prints status changes; notifier prints would-be OS notifications.
	emit := func(eventName string, data any) {
		if eventName == "status" {
			if st, ok := data.(guiservice.StatusDTO); ok {
				fmt.Printf("[状态] 已配对=%v 已连接=%v 暂停=%v 同步=%d%s\n",
					st.Paired, st.Connected, st.Paused, st.SyncCount, errSuffix(st.LastError))
			}
		}
	}
	notifier := func(title, subtitle, body string) error {
		fmt.Printf("[通知] %s — %s %s\n", title, subtitle, body)
		return nil
	}

	app := guiservice.NewApp(version, clip, clipWarn, emit, notifier, nil)

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	app.Boot(ctx, store)

	if !store.IsPaired() {
		if err := pair(ctx, app, server, code, name, trust); err != nil {
			return err
		}
	} else {
		fmt.Println("已加载现有配对，开始同步。")
	}

	fmt.Println("正在监听剪贴板并同步，按 Ctrl-C 退出。")
	go printHistory(ctx, app)
	<-ctx.Done()
	fmt.Println("\n已退出。")
	return nil
}

// pair runs the interactive TOFU pairing flow.
func pair(ctx context.Context, app *guiservice.App, server, code, name string, trust bool) error {
	if server == "" || len(strings.TrimSpace(code)) != 6 {
		return fmt.Errorf("尚未配对：请提供 -server 和 6 位 -code")
	}
	fmt.Printf("正在连接 %s …\n", server)
	fp, err := app.BeginPair(server)
	if err != nil {
		return fmt.Errorf("连接失败: %w", err)
	}
	fmt.Println("服务器证书指纹（请与 Web 配对页核对一致）:")
	fmt.Println("  " + fp)
	if !trust && !confirm("信任此指纹并完成配对? [y/N]: ") {
		return fmt.Errorf("用户取消")
	}
	if err := app.Pair(server, fp, strings.TrimSpace(code), name); err != nil {
		return fmt.Errorf("配对失败: %w", err)
	}
	fmt.Println("配对成功。")
	return nil
}

// confirm reads a yes/no answer from stdin.
func confirm(prompt string) bool {
	fmt.Print(prompt)
	sc := bufio.NewScanner(os.Stdin)
	if !sc.Scan() {
		return false
	}
	a := strings.ToLower(strings.TrimSpace(sc.Text()))
	return a == "y" || a == "yes"
}

// printHistory polls the session sync history and prints new rows as they appear.
func printHistory(ctx context.Context, app *guiservice.App) {
	seen := 0
	ticker := time.NewTicker(time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			h := app.RecentHistory()
			for ; seen < len(h); seen++ {
				e := h[seen]
				dir := "↑上传"
				if e.Direction == "download" {
					dir = "↓下载"
				}
				result := "成功"
				if !e.OK {
					result = "失败: " + e.Detail
				}
				fmt.Printf("[同步] %s %s %s\n", dir, e.ContentType, result)
			}
		}
	}
}

// errSuffix renders a trailing error fragment for status lines.
func errSuffix(e string) string {
	if e == "" {
		return ""
	}
	return " 错误=" + e
}

// hostName returns the local machine name as the default device name.
func hostName() string {
	n, err := os.Hostname()
	if err != nil || n == "" {
		return "ClipBridge CLI"
	}
	return strings.TrimSuffix(n, ".local")
}

// defaultDir resolves the credential directory (env override or OS config dir).
func defaultDir() string {
	if d := os.Getenv("CLIPBRIDGE_CONFIG_DIR"); d != "" {
		return d
	}
	base, err := os.UserConfigDir()
	if err != nil {
		base, _ = os.UserHomeDir()
	}
	return filepath.Join(base, "ClipBridge")
}

// Command clipbridge-server is the ClipBridge control plane and ciphertext relay.
//
// On first boot it creates the runtime directory, runs migrations, generates the
// device-port self-signed certificate, the server UUID and the initial admin
// credentials (printed once). It then serves the self-signed HTTPS device API and
// the plain-HTTP Web console (with the embedded React console) on separate ports,
// runs the WSS hub, and runs the periodic cleanup worker.
package main

import (
	"context"
	"crypto/tls"
	"flag"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"
	"time"

	"github.com/mokeyjay/clipbridge/server/internal/blobstore"
	"github.com/mokeyjay/clipbridge/server/internal/bootstrap"
	"github.com/mokeyjay/clipbridge/server/internal/cleanup"
	"github.com/mokeyjay/clipbridge/server/internal/config"
	"github.com/mokeyjay/clipbridge/server/internal/httpapi"
	"github.com/mokeyjay/clipbridge/server/internal/store"
	"github.com/mokeyjay/clipbridge/server/internal/tlscert"
	"github.com/mokeyjay/clipbridge/server/internal/wshub"
	"github.com/mokeyjay/clipbridge/server/web"
)

func main() {
	dataDir := flag.String("data-dir", "./runtime", "runtime directory for db, certs, data and logs")
	resetAdmin := flag.Bool("reset-admin-password", false, "reset the admin password, print it, and exit")
	flag.Parse()

	log := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelInfo}))

	if err := run(*dataDir, *resetAdmin, log); err != nil {
		log.Error("server exited with error", "error", err)
		os.Exit(1)
	}
}

func run(dataDir string, resetAdmin bool, log *slog.Logger) error {
	if err := ensureDirs(dataDir); err != nil {
		return err
	}

	cfg, err := config.Load(dataDir)
	if err != nil {
		return err
	}

	db, err := store.Open(filepath.Join(dataDir, "clipbridge.db"))
	if err != nil {
		return err
	}
	defer db.Close()
	st := store.New(db)

	if resetAdmin {
		// Offline reset: rotate the admin password, print once, and exit without
		// starting any listener (prd/06-server.md §3).
		creds, err := bootstrap.ResetAdminPassword(st)
		if err != nil {
			return err
		}
		printAdminCredentials(log, "管理员密码已重置（请立即妥善保存，登录后修改）", creds)
		return nil
	}

	cert, fingerprint, err := tlscert.EnsureCert(filepath.Join(dataDir, "certificates"))
	if err != nil {
		return err
	}
	log.Info("device certificate ready", "fingerprint_sha256", fingerprint)

	// First-boot identity: server UUID + initial admin. Credentials print once.
	creds, err := bootstrap.InitializeIdentity(st)
	if err != nil {
		return err
	}
	if creds != nil {
		printAdminCredentials(log, "初始管理员凭据（仅显示这一次）", creds)
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	// WSS hub + ciphertext blob store + periodic cleanup.
	hub := wshub.New(st)
	go hub.Run(ctx)
	blobs := blobstore.New(dataDir)
	go cleanup.New(st, blobs, log).Run(ctx)

	// HTTP/JSON + WSS API on both ports. Web cookies are Secure in production.
	// The Web port also serves the embedded React console when it was built in.
	var consoleHandler http.Handler
	if h, ok := web.Handler(); ok {
		consoleHandler = h
		log.Info("web console embedded")
	} else {
		log.Warn("web console not built in; serving API only")
	}
	api := httpapi.New(st, hub, blobs, fingerprint, true, consoleHandler)

	deviceSrv := &http.Server{
		Addr:      cfg.DeviceListenAddress,
		Handler:   api.DeviceHandler(),
		TLSConfig: &tls.Config{Certificates: []tls.Certificate{cert}, MinVersion: tls.VersionTLS12},
	}
	webSrv := &http.Server{
		Addr:    cfg.WebListenAddress,
		Handler: api.WebHandler(),
	}

	errCh := make(chan error, 2)
	go func() {
		log.Info("device port listening (self-signed HTTPS)", "addr", cfg.DeviceListenAddress)
		if err := deviceSrv.ListenAndServeTLS("", ""); err != nil && err != http.ErrServerClosed {
			errCh <- fmt.Errorf("device port: %w", err)
		}
	}()
	go func() {
		log.Info("web port listening (HTTP)", "addr", cfg.WebListenAddress)
		if err := webSrv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			errCh <- fmt.Errorf("web port: %w", err)
		}
	}()

	select {
	case <-ctx.Done():
		log.Info("shutdown signal received")
	case err := <-errCh:
		return err
	}

	shutCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	_ = deviceSrv.Shutdown(shutCtx)
	_ = webSrv.Shutdown(shutCtx)
	return nil
}

// printAdminCredentials emits admin credentials to the console exactly when the
// caller decides to (first boot or explicit reset). The password is logged here
// only — never persisted in plaintext.
func printAdminCredentials(log *slog.Logger, title string, creds *bootstrap.AdminCredentials) {
	log.Info(title, "username", creds.Username, "password", creds.Password)
}

// ensureDirs creates the runtime directory layout described in prd/06-server.md.
func ensureDirs(dataDir string) error {
	dirs := []string{
		dataDir,
		filepath.Join(dataDir, "certificates"),
		filepath.Join(dataDir, "data", "ciphertext"),
		filepath.Join(dataDir, "data", "incoming"),
		filepath.Join(dataDir, "logs"),
	}
	for _, d := range dirs {
		if err := os.MkdirAll(d, 0o700); err != nil {
			return fmt.Errorf("create %s: %w", d, err)
		}
	}
	return nil
}

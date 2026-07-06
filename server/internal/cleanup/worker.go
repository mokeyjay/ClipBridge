// Package cleanup runs the server's periodic maintenance: expiring stale pairing
// codes/requests and Web sessions, and deleting expired ciphertext and orphaned
// blob files. It runs once immediately at startup, then on a fixed interval.
package cleanup

import (
	"context"
	"log/slog"
	"time"

	"github.com/mokeyjay/clipbridge/server/internal/blobstore"
	"github.com/mokeyjay/clipbridge/server/internal/store"
)

// Interval is the periodic cleanup cadence.
const Interval = 60 * time.Second

// Worker performs periodic cleanup against the store and blob storage.
type Worker struct {
	store *store.Store
	blobs *blobstore.Store
	log   *slog.Logger
}

// New builds a cleanup worker.
func New(st *store.Store, blobs *blobstore.Store, log *slog.Logger) *Worker {
	return &Worker{store: st, blobs: blobs, log: log}
}

// Run executes one immediate sweep, then sweeps every Interval until ctx ends.
func (w *Worker) Run(ctx context.Context) {
	w.runOnce()
	ticker := time.NewTicker(Interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			w.runOnce()
		}
	}
}

// runOnce performs a single maintenance sweep. Each step is independent so one
// failure does not skip the others.
func (w *Worker) runOnce() {
	if n, err := w.store.ExpireStalePairingArtifacts(); err != nil {
		w.log.Warn("cleanup: 过期配对清理失败", "error", err)
	} else if n > 0 {
		w.log.Info("cleanup: 已标记过期配对", "count", n)
	}

	if n, err := w.store.DeleteExpiredWebSessions(); err != nil {
		w.log.Warn("cleanup: 过期会话清理失败", "error", err)
	} else if n > 0 {
		w.log.Info("cleanup: 已删除过期会话", "count", n)
	}

	// Expire items past their TTL and delete their ciphertext files.
	if due, err := w.store.ExpireDueItems(); err != nil {
		w.log.Warn("cleanup: 过期密文清理失败", "error", err)
	} else if len(due) > 0 {
		for _, d := range due {
			w.blobs.Remove(d.CiphertextPath)
		}
		w.log.Info("cleanup: 已删除过期密文", "count", len(due))
	}

	// Privacy-first orphan sweep: delete ciphertext files not referenced by any
	// still-active item (e.g. left over from a crash between promote and commit).
	w.sweepOrphans()

	// 按实例配置的日志保留天数修剪同步日志与操作日志（0 表示永久保留）。
	if ss, err := w.store.GetServerSettings(); err != nil {
		w.log.Warn("cleanup: 读取实例配置失败", "error", err)
	} else if n, err := w.store.PruneLogs(ss.SyncLogRetentionDays); err != nil {
		w.log.Warn("cleanup: 日志修剪失败", "error", err)
	} else if n > 0 {
		w.log.Info("cleanup: 已修剪过期日志", "count", n)
	}
}

// sweepOrphans removes promoted ciphertext files with no active item referencing
// them. Completed/expired items already had their files deleted on resolution.
func (w *Worker) sweepOrphans() {
	files, err := w.blobs.ListCiphertextFiles()
	if err != nil {
		w.log.Warn("cleanup: 列出密文文件失败", "error", err)
		return
	}
	if len(files) == 0 {
		return
	}
	active, err := w.store.ActiveCiphertextPaths()
	if err != nil {
		w.log.Warn("cleanup: 读取活跃密文失败", "error", err)
		return
	}
	var removed int
	for _, name := range files {
		if !active[name] {
			w.blobs.Remove(name)
			removed++
		}
	}
	if removed > 0 {
		w.log.Info("cleanup: 已删除孤立密文文件", "count", removed)
	}
}

// Package filestore manages received clipboard files on disk: a flat temp
// directory with a default 7-day TTL. Saving sanitizes names to a single path
// component so a malicious filename cannot escape the directory, and cleanup only
// touches regular files directly inside the directory (never recurses or follows
// names outside it).
package filestore

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

// DefaultTTL is the default retention for received files.
const DefaultTTL = 7 * 24 * time.Hour

// Store is a flat directory of received files with a retention TTL. The TTL is
// guarded by a mutex so it can be changed at runtime (e.g. when the user edits
// the file-retention setting).
type Store struct {
	dir string
	mu  sync.Mutex
	ttl time.Duration
}

// New creates the store directory (0700) and returns the store. A non-positive
// ttl falls back to DefaultTTL.
func New(dir string, ttl time.Duration) (*Store, error) {
	if ttl <= 0 {
		ttl = DefaultTTL
	}
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return nil, fmt.Errorf("filestore: 创建目录: %w", err)
	}
	return &Store{dir: dir, ttl: ttl}, nil
}

// Dir returns the storage directory path.
func (s *Store) Dir() string { return s.dir }

// SetTTL updates the retention applied by Cleanup. A non-positive value falls
// back to DefaultTTL.
func (s *Store) SetTTL(ttl time.Duration) {
	if ttl <= 0 {
		ttl = DefaultTTL
	}
	s.mu.Lock()
	s.ttl = ttl
	s.mu.Unlock()
}

// Save writes r to a file named after a sanitized form of name, returning the
// absolute path. Existing files with the same sanitized name are overwritten.
func (s *Store) Save(name string, r io.Reader) (string, error) {
	path := filepath.Join(s.dir, sanitize(name))
	f, err := os.OpenFile(path, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0o600)
	if err != nil {
		return "", fmt.Errorf("filestore: 创建文件: %w", err)
	}
	if _, err := io.Copy(f, r); err != nil {
		_ = f.Close()
		_ = os.Remove(path)
		return "", fmt.Errorf("filestore: 写入文件: %w", err)
	}
	if err := f.Close(); err != nil {
		return "", fmt.Errorf("filestore: 关闭文件: %w", err)
	}
	return path, nil
}

// Cleanup removes regular files in the directory older than the TTL, returning
// the count removed. It never recurses and never touches files outside the
// directory, so it cannot delete unrelated files.
func (s *Store) Cleanup(now time.Time) (int, error) {
	s.mu.Lock()
	ttl := s.ttl
	s.mu.Unlock()
	entries, err := os.ReadDir(s.dir)
	if err != nil {
		return 0, err
	}
	removed := 0
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		info, err := e.Info()
		if err != nil {
			continue
		}
		if now.Sub(info.ModTime()) > ttl {
			if os.Remove(filepath.Join(s.dir, e.Name())) == nil {
				removed++
			}
		}
	}
	return removed, nil
}

// sanitize reduces an arbitrary name to a safe single path component, never empty
// and never a traversal token.
func sanitize(name string) string {
	base := filepath.Base(strings.TrimSpace(name))
	if base == "" || base == "." || base == ".." || base == string(filepath.Separator) {
		return "received.bin"
	}
	return base
}

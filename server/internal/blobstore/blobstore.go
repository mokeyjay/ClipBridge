// Package blobstore manages the temporary ciphertext files on disk: streaming an
// upload into data/incoming while hashing it, atomically promoting the verified
// file into data/ciphertext, streaming it back to a downloader, and deleting it
// when a content's deliveries are all resolved or it expires. It never inspects
// or decrypts the bytes — they are opaque ciphertext (prd/06-server.md §7).
package blobstore

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
)

// ErrTooLarge is returned when an upload exceeds the allowed byte ceiling.
var ErrTooLarge = errors.New("blobstore: 内容超过允许的最大尺寸")

// Store owns the incoming and ciphertext directories under the runtime data dir.
type Store struct {
	incomingDir   string
	ciphertextDir string
}

// New builds a Store for the given runtime data directory. The directories are
// created by the server bootstrap; New does not create them.
func New(dataDir string) *Store {
	return &Store{
		incomingDir:   filepath.Join(dataDir, "data", "incoming"),
		ciphertextDir: filepath.Join(dataDir, "data", "ciphertext"),
	}
}

// WriteIncoming streams r into a temp file in incoming/, returning the temp path,
// the bytes written and the lowercase-hex SHA-256. Writing stops with ErrTooLarge
// if more than maxBytes are read. The caller must Promote or Abort the temp file.
func (s *Store) WriteIncoming(r io.Reader, maxBytes int64) (tmpPath string, size int64, sha256hex string, err error) {
	f, err := os.CreateTemp(s.incomingDir, "upload-*.tmp")
	if err != nil {
		return "", 0, "", fmt.Errorf("blobstore: 创建临时文件: %w", err)
	}
	tmpPath = f.Name()
	hash := sha256.New()
	// Read one extra byte past the ceiling to detect oversize without trusting
	// any client-declared length.
	limited := io.LimitReader(r, maxBytes+1)
	n, copyErr := io.Copy(io.MultiWriter(f, hash), limited)
	closeErr := f.Close()
	if copyErr != nil {
		s.Abort(tmpPath)
		return "", 0, "", fmt.Errorf("blobstore: 写入临时文件: %w", copyErr)
	}
	if closeErr != nil {
		s.Abort(tmpPath)
		return "", 0, "", fmt.Errorf("blobstore: 关闭临时文件: %w", closeErr)
	}
	if n > maxBytes {
		s.Abort(tmpPath)
		return "", 0, "", ErrTooLarge
	}
	return tmpPath, n, hex.EncodeToString(hash.Sum(nil)), nil
}

// RelPath is the stored ciphertext_path for an item id (relative to ciphertext/).
func RelPath(itemID string) string { return itemID + ".bin" }

// Promote atomically renames a verified temp file to ciphertext/<itemID>.bin and
// returns the relative path to store in the database.
func (s *Store) Promote(tmpPath, itemID string) (string, error) {
	rel := RelPath(itemID)
	if err := os.Rename(tmpPath, filepath.Join(s.ciphertextDir, rel)); err != nil {
		return "", fmt.Errorf("blobstore: 提交密文文件: %w", err)
	}
	return rel, nil
}

// Open opens a promoted ciphertext file for streaming download.
func (s *Store) Open(relPath string) (io.ReadCloser, error) {
	// relPath comes from our own DB (an item-id based name); guard traversal anyway.
	clean := filepath.Base(relPath)
	f, err := os.Open(filepath.Join(s.ciphertextDir, clean))
	if err != nil {
		return nil, err
	}
	return f, nil
}

// Remove deletes a promoted ciphertext file, ignoring a missing file.
func (s *Store) Remove(relPath string) {
	clean := filepath.Base(relPath)
	_ = os.Remove(filepath.Join(s.ciphertextDir, clean))
}

// Abort removes an incoming temp file, ignoring a missing file.
func (s *Store) Abort(tmpPath string) { _ = os.Remove(tmpPath) }

// ListCiphertextFiles returns the relative names of all promoted ciphertext files,
// used by the cleanup worker to detect orphans not referenced by the database.
func (s *Store) ListCiphertextFiles() ([]string, error) {
	entries, err := os.ReadDir(s.ciphertextDir)
	if err != nil {
		return nil, err
	}
	var out []string
	for _, e := range entries {
		if !e.IsDir() {
			out = append(out, e.Name())
		}
	}
	return out, nil
}

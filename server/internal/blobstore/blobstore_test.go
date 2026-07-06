package blobstore

import (
	"bytes"
	"io"
	"os"
	"path/filepath"
	"testing"
)

// newTestStore builds a Store with its directories created under a temp dir.
func newTestStore(t *testing.T) *Store {
	t.Helper()
	dir := t.TempDir()
	for _, sub := range []string{"data/incoming", "data/ciphertext"} {
		if err := os.MkdirAll(filepath.Join(dir, sub), 0o700); err != nil {
			t.Fatalf("mkdir: %v", err)
		}
	}
	return New(dir)
}

// TestWritePromoteOpenRemove covers the happy-path lifecycle and hashing.
func TestWritePromoteOpenRemove(t *testing.T) {
	s := newTestStore(t)
	payload := bytes.Repeat([]byte("clip"), 1000)

	tmp, size, sha, err := s.WriteIncoming(bytes.NewReader(payload), 1<<20)
	if err != nil {
		t.Fatalf("write: %v", err)
	}
	if size != int64(len(payload)) {
		t.Errorf("size = %d, want %d", size, len(payload))
	}
	if len(sha) != 64 {
		t.Errorf("sha length = %d, want 64 hex", len(sha))
	}

	rel, err := s.Promote(tmp, "item-uuid")
	if err != nil {
		t.Fatalf("promote: %v", err)
	}
	if rel != "item-uuid.bin" {
		t.Errorf("relpath = %q", rel)
	}

	rc, err := s.Open(rel)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	got, _ := io.ReadAll(rc)
	rc.Close()
	if !bytes.Equal(got, payload) {
		t.Error("round-trip mismatch")
	}

	s.Remove(rel)
	if _, err := s.Open(rel); err == nil {
		t.Error("file still present after remove")
	}
}

// TestWriteIncomingTooLarge enforces the byte ceiling without trusting any
// declared length.
func TestWriteIncomingTooLarge(t *testing.T) {
	s := newTestStore(t)
	_, _, _, err := s.WriteIncoming(bytes.NewReader(bytes.Repeat([]byte("x"), 100)), 50)
	if err != ErrTooLarge {
		t.Errorf("err = %v, want ErrTooLarge", err)
	}
	// The rejected temp file must not linger.
	entries, _ := os.ReadDir(s.incomingDir)
	if len(entries) != 0 {
		t.Errorf("incoming dir not cleaned: %d entries", len(entries))
	}
}

// TestListCiphertextFiles reports promoted files for orphan sweeping.
func TestListCiphertextFiles(t *testing.T) {
	s := newTestStore(t)
	tmp, _, _, _ := s.WriteIncoming(bytes.NewReader([]byte("a")), 10)
	if _, err := s.Promote(tmp, "abc"); err != nil {
		t.Fatalf("promote: %v", err)
	}
	files, err := s.ListCiphertextFiles()
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(files) != 1 || files[0] != "abc.bin" {
		t.Errorf("files = %v", files)
	}
}

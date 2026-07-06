package filestore

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// TestSaveAndRead writes and reads back a received file.
func TestSaveAndRead(t *testing.T) {
	s, err := New(filepath.Join(t.TempDir(), "files"), time.Hour)
	if err != nil {
		t.Fatalf("new: %v", err)
	}
	path, err := s.Save("photo.png", strings.NewReader("PNGDATA"))
	if err != nil {
		t.Fatalf("save: %v", err)
	}
	if filepath.Dir(path) != s.Dir() {
		t.Errorf("saved outside dir: %s", path)
	}
	data, _ := os.ReadFile(path)
	if string(data) != "PNGDATA" {
		t.Errorf("content = %q", data)
	}
}

// TestSanitizeBlocksTraversal ensures a malicious name cannot escape the dir.
func TestSanitizeBlocksTraversal(t *testing.T) {
	s, _ := New(filepath.Join(t.TempDir(), "files"), time.Hour)
	for _, name := range []string{"../../etc/passwd", "/etc/hosts", "..", "", "a/b/c.txt"} {
		path, err := s.Save(name, strings.NewReader("x"))
		if err != nil {
			t.Fatalf("save %q: %v", name, err)
		}
		if filepath.Dir(path) != s.Dir() {
			t.Errorf("name %q escaped dir → %s", name, path)
		}
	}
}

// TestSaveSameNameOverwrites verifies a second file with the same name overwrites
// the first (keeping the original filename), per the "保留文件名，可覆盖" decision.
func TestSaveSameNameOverwrites(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "files")
	s, _ := New(dir, time.Hour)

	p1, err := s.Save("report.pdf", strings.NewReader("first"))
	if err != nil {
		t.Fatalf("save 1: %v", err)
	}
	p2, err := s.Save("report.pdf", strings.NewReader("second"))
	if err != nil {
		t.Fatalf("save 2: %v", err)
	}
	if p1 != p2 {
		t.Errorf("same name produced different paths: %q vs %q", p1, p2)
	}
	if filepath.Base(p2) != "report.pdf" {
		t.Errorf("filename not preserved: %q", filepath.Base(p2))
	}
	// Exactly one file in the directory, holding the latest content.
	entries, _ := os.ReadDir(dir)
	if len(entries) != 1 {
		t.Errorf("expected 1 file after overwrite, got %d", len(entries))
	}
	data, _ := os.ReadFile(p2)
	if string(data) != "second" {
		t.Errorf("content = %q, want second", data)
	}
}

// TestSetTTLApplied verifies a runtime TTL change takes effect for cleanup.
func TestSetTTLApplied(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "files")
	s, _ := New(dir, time.Hour)
	p, _ := s.Save("a.txt", strings.NewReader("x"))
	past := time.Now().Add(-30 * time.Minute)
	_ = os.Chtimes(p, past, past)

	// At 1h TTL the 30-min-old file stays; tightening to 1 min removes it.
	if n, _ := s.Cleanup(time.Now()); n != 0 {
		t.Fatalf("removed %d before TTL change, want 0", n)
	}
	s.SetTTL(time.Minute)
	if n, _ := s.Cleanup(time.Now()); n != 1 {
		t.Errorf("removed %d after SetTTL, want 1", n)
	}
}
func TestCleanupRespectsTTLAndScope(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "files")
	s, _ := New(dir, time.Hour)

	oldPath, _ := s.Save("old.txt", strings.NewReader("old"))
	newPath, _ := s.Save("new.txt", strings.NewReader("new"))
	// Age the old file beyond the TTL.
	past := time.Now().Add(-2 * time.Hour)
	if err := os.Chtimes(oldPath, past, past); err != nil {
		t.Fatalf("chtimes: %v", err)
	}
	// A file outside the store dir must never be touched.
	outside := filepath.Join(t.TempDir(), "outside.txt")
	_ = os.WriteFile(outside, []byte("keep"), 0o600)
	_ = os.Chtimes(outside, past, past)

	removed, err := s.Cleanup(time.Now())
	if err != nil {
		t.Fatalf("cleanup: %v", err)
	}
	if removed != 1 {
		t.Errorf("removed = %d, want 1", removed)
	}
	if _, err := os.Stat(oldPath); !os.IsNotExist(err) {
		t.Error("old file not removed")
	}
	if _, err := os.Stat(newPath); err != nil {
		t.Error("new file wrongly removed")
	}
	if _, err := os.Stat(outside); err != nil {
		t.Error("file outside store dir was removed")
	}
}

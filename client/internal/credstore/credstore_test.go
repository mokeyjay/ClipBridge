package credstore

import (
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

// TestRoundTrip covers saving and loading identity, token, key and fingerprint.
func TestRoundTrip(t *testing.T) {
	s, err := Open(filepath.Join(t.TempDir(), "cfg"))
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	if s.IsPaired() {
		t.Error("fresh store should not be paired")
	}

	id := &Identity{DeviceID: "d1", UserID: "u1", ServerID: "s1", ServerURL: "https://h:8443", PublicKeyB64: "pk"}
	if err := s.SaveIdentity(id); err != nil {
		t.Fatalf("save identity: %v", err)
	}
	if err := s.SaveToken("secret-token"); err != nil {
		t.Fatalf("save token: %v", err)
	}
	if err := s.SavePrivateKey([]byte("priv-bytes")); err != nil {
		t.Fatalf("save key: %v", err)
	}
	if err := s.SaveServerFingerprint("AB:CD"); err != nil {
		t.Fatalf("save fp: %v", err)
	}

	if !s.IsPaired() {
		t.Error("store should be paired after saving identity+token")
	}

	gotID, _ := s.LoadIdentity()
	if gotID.DeviceID != "d1" || gotID.Version != 1 {
		t.Errorf("identity = %+v", gotID)
	}
	if tok, _ := s.LoadToken(); tok != "secret-token" {
		t.Errorf("token = %q", tok)
	}
	if key, _ := s.LoadPrivateKey(); string(key) != "priv-bytes" {
		t.Errorf("key = %q", key)
	}
	if fp, _ := s.LoadServerFingerprint(); fp != "AB:CD" {
		t.Errorf("fp = %q", fp)
	}
}

// TestStrictPermissions verifies secret files are 0600 and dirs 0700 on Unix.
func TestStrictPermissions(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("ACL semantics differ on Windows (M5)")
	}
	dir := filepath.Join(t.TempDir(), "cfg")
	s, err := Open(dir)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	_ = s.SaveToken("t")

	dInfo, _ := os.Stat(filepath.Join(dir, "credentials"))
	if dInfo.Mode().Perm() != 0o700 {
		t.Errorf("credentials dir mode = %v, want 0700", dInfo.Mode().Perm())
	}
	fInfo, _ := os.Stat(filepath.Join(dir, "credentials", "device-token"))
	if fInfo.Mode().Perm() != 0o600 {
		t.Errorf("token file mode = %v, want 0600", fInfo.Mode().Perm())
	}
}

// TestRepairPermissions verifies loosened permissions are tightened on open.
func TestRepairPermissions(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("ACL semantics differ on Windows (M5)")
	}
	dir := filepath.Join(t.TempDir(), "cfg")
	s, _ := Open(dir)
	_ = s.SaveToken("t")

	// Loosen perms, then reopen which should repair them.
	tokenPath := filepath.Join(dir, "credentials", "device-token")
	if err := os.Chmod(tokenPath, 0o644); err != nil {
		t.Fatalf("chmod: %v", err)
	}
	if _, err := Open(dir); err != nil {
		t.Fatalf("reopen repair returned error: %v", err)
	}
	info, _ := os.Stat(tokenPath)
	if info.Mode().Perm() != 0o600 {
		t.Errorf("token mode after repair = %v, want 0600", info.Mode().Perm())
	}
}

// TestProfileDefaults verifies profile load returns defaults when absent and
// round-trips when saved.
func TestProfileDefaults(t *testing.T) {
	s, _ := Open(filepath.Join(t.TempDir(), "cfg"))
	p, err := s.LoadProfile()
	if err != nil {
		t.Fatalf("load profile: %v", err)
	}
	if p.Theme != "system" || p.SyncDirection != "bidirectional" {
		t.Errorf("defaults = %+v", p)
	}
	p.Paused = true
	p.Theme = "dark"
	if err := s.SaveProfile(p); err != nil {
		t.Fatalf("save profile: %v", err)
	}
	got, _ := s.LoadProfile()
	if !got.Paused || got.Theme != "dark" {
		t.Errorf("profile round-trip = %+v", got)
	}
}

// TestReset removes credentials.
func TestReset(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "cfg")
	s, _ := Open(dir)
	_ = s.SaveToken("t")
	if err := s.Reset(); err != nil {
		t.Fatalf("reset: %v", err)
	}
	if s.IsPaired() {
		t.Error("store should not be paired after reset")
	}
}

// TestWriteAfterReset guards the re-pair flow: Reset removes the credentials dir,
// and writing a secret afterwards must recreate it rather than fail.
func TestWriteAfterReset(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "cfg")
	s, _ := Open(dir)
	if err := s.SavePrivateKey([]byte("k1")); err != nil {
		t.Fatalf("initial save: %v", err)
	}
	if err := s.Reset(); err != nil {
		t.Fatalf("reset: %v", err)
	}
	if err := s.SavePrivateKey([]byte("k2")); err != nil {
		t.Fatalf("save after reset: %v", err)
	}
	got, err := s.LoadPrivateKey()
	if err != nil || string(got) != "k2" {
		t.Fatalf("load after reset = %q, %v", got, err)
	}
}

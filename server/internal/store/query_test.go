package store

import (
	"path/filepath"
	"testing"
	"time"

	"github.com/mokeyjay/clipbridge/shared/protocol"
)

// newTestStore opens a fresh in-temp Store with a controllable clock.
func newTestStore(t *testing.T) (*Store, *int64) {
	t.Helper()
	db, err := Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	clock := time.Now().Unix()
	st := New(db)
	st.now = func() time.Time { return time.Unix(clock, 0) }
	return st, &clock
}

// TestUserLifecycleAndSettings covers user creation (with default settings),
// unique username, status changes and settings update.
func TestUserLifecycleAndSettings(t *testing.T) {
	st, _ := newTestStore(t)

	u, err := st.CreateUser("alice", "hash")
	if err != nil {
		t.Fatalf("create user: %v", err)
	}
	if u.Status != protocol.UserActive {
		t.Errorf("new user status = %q, want active", u.Status)
	}

	// Default settings row must exist with schema defaults.
	us, err := st.GetUserSettings(u.ID)
	if err != nil {
		t.Fatalf("get user settings: %v", err)
	}
	if us.MaxSyncSizeBytes != 104857600 {
		t.Errorf("default max sync size = %d, want 100 MiB", us.MaxSyncSizeBytes)
	}
	if len(us.AllowedTypes) != 4 {
		t.Errorf("default allowed types = %v, want 4 entries", us.AllowedTypes)
	}

	// Duplicate username must violate the UNIQUE constraint.
	if _, err := st.CreateUser("alice", "hash2"); err == nil {
		t.Error("duplicate username was accepted")
	}

	// Update settings round-trip.
	us.AllowedTypes = []protocol.ContentType{protocol.ContentText}
	us.MaxAutoUploadSizeBytes = 123
	if err := st.UpdateUserSettings(us); err != nil {
		t.Fatalf("update settings: %v", err)
	}
	got, _ := st.GetUserSettings(u.ID)
	if len(got.AllowedTypes) != 1 || got.AllowedTypes[0] != protocol.ContentText {
		t.Errorf("allowed types not persisted: %v", got.AllowedTypes)
	}
	if got.MaxAutoUploadSizeBytes != 123 {
		t.Errorf("auto upload not persisted: %d", got.MaxAutoUploadSizeBytes)
	}

	// Disable and verify.
	if err := st.SetUserStatus(u.ID, protocol.UserDisabled); err != nil {
		t.Fatalf("disable: %v", err)
	}
	got2, _ := st.GetUserByID(u.ID)
	if got2.Status != protocol.UserDisabled {
		t.Errorf("status = %q, want disabled", got2.Status)
	}
}

// TestWebSessionExpiry covers lookup, expiry and subject-wide deletion.
func TestWebSessionExpiry(t *testing.T) {
	st, clock := newTestStore(t)

	sess, err := st.CreateWebSession(protocol.SubjectUser, "u1", "tokhash", 60)
	if err != nil {
		t.Fatalf("create session: %v", err)
	}
	if _, err := st.GetWebSessionByTokenHash("tokhash"); err != nil {
		t.Fatalf("lookup fresh session: %v", err)
	}

	// Advance past expiry.
	*clock += 61
	if _, err := st.GetWebSessionByTokenHash("tokhash"); err != ErrNotFound {
		t.Errorf("expired session lookup err = %v, want ErrNotFound", err)
	}

	// Re-create and verify subject-wide deletion (used on disable/password change).
	*clock = sess.CreatedAt
	if _, err := st.CreateWebSession(protocol.SubjectUser, "u1", "tokhash2", 60); err != nil {
		t.Fatalf("recreate: %v", err)
	}
	if err := st.DeleteSessionsForSubject(protocol.SubjectUser, "u1"); err != nil {
		t.Fatalf("delete for subject: %v", err)
	}
	if _, err := st.GetWebSessionByTokenHash("tokhash2"); err != ErrNotFound {
		t.Errorf("session not deleted: %v", err)
	}
}

// TestSingleActivePairingCode verifies creating a new code cancels the prior one,
// honoring the "at most one active per user" rule.
func TestSingleActivePairingCode(t *testing.T) {
	st, _ := newTestStore(t)
	u, _ := st.CreateUser("alice", "h")

	first, err := st.CreatePairingCode(u.ID, "hash1", 300)
	if err != nil {
		t.Fatalf("create code 1: %v", err)
	}
	second, err := st.CreatePairingCode(u.ID, "hash2", 300)
	if err != nil {
		t.Fatalf("create code 2: %v", err)
	}

	active, err := st.GetActivePairingCodeByUser(u.ID)
	if err != nil {
		t.Fatalf("get active: %v", err)
	}
	if active.ID != second.ID {
		t.Errorf("active code = %s, want newest %s", active.ID, second.ID)
	}
	// First must no longer be resolvable by hash (it was cancelled).
	if _, err := st.GetActivePairingCodeByHash("hash1"); err != ErrNotFound {
		t.Errorf("cancelled code still active: %v", err)
	}
	_ = first
}

// TestPairingFlowAndOneTimeToken walks submit → confirm → claim and asserts the
// token is issued exactly once.
func TestPairingFlowAndOneTimeToken(t *testing.T) {
	st, _ := newTestStore(t)
	u, _ := st.CreateUser("alice", "h")

	code, _ := st.CreatePairingCode(u.ID, "codehash", 300)
	req, err := st.CreatePairingRequest(code, "MacBook", protocol.PlatformDarwin, "0.1.0", "pubkey", "pollhash")
	if err != nil {
		t.Fatalf("create request: %v", err)
	}

	// Pending request shows up for the user.
	pending, _ := st.ListPendingPairingRequestsByUser(u.ID)
	if len(pending) != 1 || pending[0].ID != req.ID {
		t.Fatalf("pending list = %v", pending)
	}

	// Confirming creates the device and consumes the code.
	device, err := st.ConfirmPairingRequest(u.ID, req.ID)
	if err != nil {
		t.Fatalf("confirm: %v", err)
	}
	if device.UserID != u.ID || device.HPKEPublicKey != "pubkey" {
		t.Errorf("device mismatch: %+v", device)
	}
	if _, err := st.GetActivePairingCodeByUser(u.ID); err != ErrNotFound {
		t.Errorf("code not consumed: %v", err)
	}
	// Device must have a default settings row.
	if _, err := st.GetDeviceSettings(device.ID); err != nil {
		t.Errorf("device settings missing: %v", err)
	}

	// First claim issues a usable token.
	claimed, err := st.ClaimDeviceToken(req.ID, "tokhash")
	if err != nil {
		t.Fatalf("first claim: %v", err)
	}
	if claimed.ID != device.ID {
		t.Errorf("claimed device = %s, want %s", claimed.ID, device.ID)
	}
	gotDev, gotTok, err := st.AuthenticateDeviceToken("tokhash")
	if err != nil {
		t.Fatalf("auth token: %v", err)
	}
	if gotDev.ID != device.ID {
		t.Errorf("auth device = %s, want %s", gotDev.ID, device.ID)
	}

	// Second claim must be refused (one-time).
	if _, err := st.ClaimDeviceToken(req.ID, "tokhash-2"); err != ErrPairingNotConfirmed {
		t.Errorf("second claim err = %v, want ErrPairingNotConfirmed", err)
	}

	// Revocation invalidates the token immediately.
	if err := st.RevokeDeviceTokens(device.ID); err != nil {
		t.Fatalf("revoke: %v", err)
	}
	if _, _, err := st.AuthenticateDeviceToken("tokhash"); err != ErrNotFound {
		t.Errorf("revoked token still valid: %v", err)
	}
	_ = gotTok
}

// TestConfirmRejectsCrossUserAndExpired covers authorization and expiry guards.
func TestConfirmRejectsCrossUserAndExpired(t *testing.T) {
	st, clock := newTestStore(t)
	alice, _ := st.CreateUser("alice", "h")
	bob, _ := st.CreateUser("bob", "h")

	code, _ := st.CreatePairingCode(alice.ID, "codehash", 300)
	req, _ := st.CreatePairingRequest(code, "Mac", protocol.PlatformDarwin, "0.1.0", "pk", "ph")

	// Bob cannot confirm Alice's request.
	if _, err := st.ConfirmPairingRequest(bob.ID, req.ID); err != ErrNotFound {
		t.Errorf("cross-user confirm err = %v, want ErrNotFound", err)
	}

	// After expiry, even the owner cannot confirm.
	*clock += 301
	if _, err := st.ConfirmPairingRequest(alice.ID, req.ID); err != ErrPairingNotPending {
		t.Errorf("expired confirm err = %v, want ErrPairingNotPending", err)
	}
}

// TestExpireStalePairingArtifacts checks the cleanup helper flips stale rows.
func TestExpireStalePairingArtifacts(t *testing.T) {
	st, clock := newTestStore(t)
	u, _ := st.CreateUser("alice", "h")
	code, _ := st.CreatePairingCode(u.ID, "ch", 300)
	_, _ = st.CreatePairingRequest(code, "Mac", protocol.PlatformDarwin, "0.1.0", "pk", "ph")

	*clock += 301
	n, err := st.ExpireStalePairingArtifacts()
	if err != nil {
		t.Fatalf("expire: %v", err)
	}
	if n < 2 {
		t.Errorf("expired rows = %d, want >= 2 (code + request)", n)
	}
}

// TestDeviceSettingsOverride checks inherit flags and override values round-trip.
func TestDeviceSettingsOverride(t *testing.T) {
	st, _ := newTestStore(t)
	u, _ := st.CreateUser("alice", "h")
	code, _ := st.CreatePairingCode(u.ID, "ch", 300)
	req, _ := st.CreatePairingRequest(code, "Mac", protocol.PlatformDarwin, "0.1.0", "pk", "ph")
	device, _ := st.ConfirmPairingRequest(u.ID, req.ID)

	ds, _ := st.GetDeviceSettings(device.ID)
	if !ds.MaxSyncSizeInherit || !ds.AllowedTypesInherit {
		t.Error("new device settings should fully inherit")
	}
	override := int64(2048)
	ds.MaxSyncSizeInherit = false
	ds.MaxSyncSizeBytes = &override
	ds.AllowedTypesInherit = false
	ds.AllowedTypes = []protocol.ContentType{protocol.ContentText, protocol.ContentImage}
	if err := st.UpdateDeviceSettings(ds); err != nil {
		t.Fatalf("update device settings: %v", err)
	}
	got, _ := st.GetDeviceSettings(device.ID)
	if got.MaxSyncSizeInherit || got.MaxSyncSizeBytes == nil || *got.MaxSyncSizeBytes != 2048 {
		t.Errorf("override not persisted: %+v", got)
	}
	if got.AllowedTypesInherit || len(got.AllowedTypes) != 2 {
		t.Errorf("allowed types override not persisted: %+v", got)
	}
}

// TestServerSettings covers ensure (idempotent) + admin update.
func TestServerSettings(t *testing.T) {
	st, _ := newTestStore(t)
	if err := st.EnsureServerSettings("server-uuid"); err != nil {
		t.Fatalf("ensure: %v", err)
	}
	// Second ensure with a different id must not overwrite the first.
	if err := st.EnsureServerSettings("other-uuid"); err != nil {
		t.Fatalf("ensure 2: %v", err)
	}
	ss, _ := st.GetServerSettings()
	if ss.ServerID != "server-uuid" {
		t.Errorf("server id = %q, want server-uuid", ss.ServerID)
	}
	if ss.RegistrationEnabled {
		t.Error("registration should default to disabled")
	}
	if err := st.UpdateServerSettings("My Server", true, 50, 7, []protocol.ContentType{protocol.ContentText, protocol.ContentImage}); err != nil {
		t.Fatalf("update: %v", err)
	}
	ss2, _ := st.GetServerSettings()
	if !ss2.RegistrationEnabled || ss2.MaxSyncSizeBytes != 50 || ss2.ServerName != "My Server" || ss2.SyncLogRetentionDays != 7 {
		t.Errorf("settings not updated: %+v", ss2)
	}
	if len(ss2.AllowedTypes) != 2 || ss2.AllowedTypes[0] != protocol.ContentText || ss2.AllowedTypes[1] != protocol.ContentImage {
		t.Errorf("allowed types not persisted: %v", ss2.AllowedTypes)
	}
}

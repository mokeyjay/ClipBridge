package bootstrap

import (
	"path/filepath"
	"testing"

	"github.com/mokeyjay/clipbridge/server/internal/security"
	"github.com/mokeyjay/clipbridge/server/internal/store"
)

// openStore opens a throwaway Store for bootstrap tests.
func openStore(t *testing.T) *store.Store {
	t.Helper()
	db, err := store.Open(filepath.Join(t.TempDir(), "b.db"))
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	return store.New(db)
}

// TestInitializeIdentityCreatesAdminOnce verifies credentials are returned on the
// first call and nil thereafter (idempotent, no credential re-print).
func TestInitializeIdentityCreatesAdminOnce(t *testing.T) {
	st := openStore(t)

	creds, err := InitializeIdentity(st)
	if err != nil {
		t.Fatalf("first init: %v", err)
	}
	if creds == nil {
		t.Fatal("first init returned no credentials")
	}
	if creds.Username == "" || creds.Password == "" {
		t.Errorf("incomplete credentials: %+v", creds)
	}

	// The generated password must verify against the stored hash.
	admin, err := st.GetAdmin()
	if err != nil {
		t.Fatalf("get admin: %v", err)
	}
	if !security.VerifyPassword(admin.PasswordHash, creds.Password) {
		t.Error("generated password does not verify against stored hash")
	}

	// Server settings row must exist with a server UUID.
	ss, err := st.GetServerSettings()
	if err != nil {
		t.Fatalf("get settings: %v", err)
	}
	if ss.ServerID == "" {
		t.Error("server id was not generated")
	}

	// Second init must not create/print new credentials.
	creds2, err := InitializeIdentity(st)
	if err != nil {
		t.Fatalf("second init: %v", err)
	}
	if creds2 != nil {
		t.Errorf("second init re-issued credentials: %+v", creds2)
	}
	// Server UUID must remain stable across boots.
	ss2, _ := st.GetServerSettings()
	if ss2.ServerID != ss.ServerID {
		t.Errorf("server id changed: %q -> %q", ss.ServerID, ss2.ServerID)
	}
}

// TestResetAdminPassword verifies the offline reset rotates the password while
// keeping the username, and the new password verifies.
func TestResetAdminPassword(t *testing.T) {
	st := openStore(t)
	orig, err := InitializeIdentity(st)
	if err != nil {
		t.Fatalf("init: %v", err)
	}

	reset, err := ResetAdminPassword(st)
	if err != nil {
		t.Fatalf("reset: %v", err)
	}
	if reset.Username != orig.Username {
		t.Errorf("username changed on reset: %q -> %q", orig.Username, reset.Username)
	}
	if reset.Password == orig.Password {
		t.Error("reset returned the same password")
	}
	admin, _ := st.GetAdmin()
	if !security.VerifyPassword(admin.PasswordHash, reset.Password) {
		t.Error("new password does not verify")
	}
	if security.VerifyPassword(admin.PasswordHash, orig.Password) {
		t.Error("old password still verifies after reset")
	}
}

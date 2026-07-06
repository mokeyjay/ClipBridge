package security

import (
	"regexp"
	"strings"
	"testing"
)

// TestHashVerifyRoundTrip checks a hashed password verifies and a wrong one fails.
func TestHashVerifyRoundTrip(t *testing.T) {
	hash, err := HashPassword("correct horse battery staple")
	if err != nil {
		t.Fatalf("hash: %v", err)
	}
	if !VerifyPassword(hash, "correct horse battery staple") {
		t.Error("correct password did not verify")
	}
	if VerifyPassword(hash, "wrong password") {
		t.Error("wrong password verified")
	}
}

// TestHashEncodesDefaultParams checks the encoded hash carries the pinned params.
func TestHashEncodesDefaultParams(t *testing.T) {
	hash, err := HashPassword("pw")
	if err != nil {
		t.Fatalf("hash: %v", err)
	}
	if !strings.HasPrefix(hash, "$argon2id$") {
		t.Errorf("hash missing argon2id prefix: %q", hash)
	}
	if !strings.Contains(hash, "m=65536,t=1,p=4") {
		t.Errorf("hash missing pinned params: %q", hash)
	}
}

// TestHashIsSalted checks two hashes of the same password differ (random salt).
func TestHashIsSalted(t *testing.T) {
	a, _ := HashPassword("same")
	b, _ := HashPassword("same")
	if a == b {
		t.Error("two hashes of the same password are identical; salt missing")
	}
}

// TestEmptyPasswordRejected checks empty passwords are refused.
func TestEmptyPasswordRejected(t *testing.T) {
	if _, err := HashPassword(""); err == nil {
		t.Error("empty password was hashed")
	}
}

// TestVerifyMalformedHash checks malformed hashes fail closed rather than panic.
func TestVerifyMalformedHash(t *testing.T) {
	for _, bad := range []string{"", "plain", "$argon2id$bad", "$argon2id$v=19$m=0,t=0,p=0$$"} {
		if VerifyPassword(bad, "x") {
			t.Errorf("malformed hash verified: %q", bad)
		}
	}
}

// TestRandomTokenDistinctAndURLSafe checks tokens are unique and URL-safe.
func TestRandomTokenDistinctAndURLSafe(t *testing.T) {
	urlSafe := regexp.MustCompile(`^[A-Za-z0-9_-]+$`)
	seen := make(map[string]bool)
	for i := 0; i < 100; i++ {
		tok, err := RandomToken(32)
		if err != nil {
			t.Fatalf("token: %v", err)
		}
		if !urlSafe.MatchString(tok) {
			t.Errorf("token not URL-safe: %q", tok)
		}
		if seen[tok] {
			t.Errorf("duplicate token: %q", tok)
		}
		seen[tok] = true
	}
}

// TestTokenHashStableAndIrreversibleShape checks the hash is deterministic hex.
func TestTokenHashStableAndIrreversibleShape(t *testing.T) {
	h1 := TokenHash("abc")
	h2 := TokenHash("abc")
	if h1 != h2 {
		t.Error("token hash not deterministic")
	}
	if len(h1) != 64 {
		t.Errorf("token hash length = %d, want 64 hex chars", len(h1))
	}
	if h1 == "abc" || strings.Contains(h1, "abc") {
		t.Error("token hash leaks plaintext")
	}
}

// TestRandomNumericCode checks length and digit-only output.
func TestRandomNumericCode(t *testing.T) {
	code, err := RandomNumericCode(6)
	if err != nil {
		t.Fatalf("code: %v", err)
	}
	if len(code) != 6 {
		t.Errorf("code length = %d, want 6", len(code))
	}
	for _, r := range code {
		if r < '0' || r > '9' {
			t.Errorf("code has non-digit %q", r)
		}
	}
}

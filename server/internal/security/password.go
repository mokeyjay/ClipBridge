// Package security holds the server's credential primitives: Argon2id password
// hashing, high-entropy token and pairing-code generation, and the irreversible
// hashing applied to device tokens and session tokens before storage. Nothing in
// this package logs or returns plaintext secrets beyond the value being created.
package security

import (
	"crypto/rand"
	"crypto/subtle"
	"encoding/base64"
	"fmt"
	"strconv"
	"strings"

	"golang.org/x/crypto/argon2"
)

// Argon2Params are the Argon2id cost parameters. The defaults are pinned by the
// PRD (prd/06-server.md §5): m=65536 KiB, t=1, p=4. They are encoded into every
// hash so verification stays correct if the defaults are later tuned.
type Argon2Params struct {
	MemoryKiB uint32 // memory cost in KiB
	Time      uint32 // number of iterations
	Threads   uint8  // parallelism
	SaltLen   uint32 // random salt length in bytes
	KeyLen    uint32 // derived key length in bytes
}

// DefaultArgon2Params is the pinned cost profile.
var DefaultArgon2Params = Argon2Params{
	MemoryKiB: 64 * 1024, // 65536 KiB
	Time:      1,
	Threads:   4,
	SaltLen:   16,
	KeyLen:    32,
}

// HashPassword hashes a password with the default Argon2id parameters and
// returns the self-describing PHC-style encoded string.
func HashPassword(password string) (string, error) {
	return HashPasswordWithParams(password, DefaultArgon2Params)
}

// HashPasswordWithParams hashes a password with explicit parameters. It generates
// a fresh random salt and encodes params, salt and key into one string so that
// VerifyPassword needs no external state.
func HashPasswordWithParams(password string, params Argon2Params) (string, error) {
	if password == "" {
		return "", fmt.Errorf("security: 密码不能为空")
	}
	salt := make([]byte, params.SaltLen)
	if _, err := rand.Read(salt); err != nil {
		return "", fmt.Errorf("security: 生成密码盐: %w", err)
	}
	key := argon2.IDKey([]byte(password), salt, params.Time, params.MemoryKiB, params.Threads, params.KeyLen)
	return fmt.Sprintf(
		"$argon2id$v=%d$m=%d,t=%d,p=%d$%s$%s",
		argon2.Version,
		params.MemoryKiB,
		params.Time,
		params.Threads,
		base64.RawStdEncoding.EncodeToString(salt),
		base64.RawStdEncoding.EncodeToString(key),
	), nil
}

// VerifyPassword reports whether password matches encodedHash in constant time.
// A malformed hash returns false rather than an error so callers can treat it as
// an authentication failure uniformly.
func VerifyPassword(encodedHash, password string) bool {
	params, salt, expected, ok := parsePasswordHash(encodedHash)
	if !ok {
		return false
	}
	actual := argon2.IDKey([]byte(password), salt, params.Time, params.MemoryKiB, params.Threads, uint32(len(expected)))
	return subtle.ConstantTimeCompare(actual, expected) == 1
}

// parsePasswordHash decodes a PHC-style argon2id string back into its parameters,
// salt and derived key. ok is false for any structural or value problem.
func parsePasswordHash(encodedHash string) (Argon2Params, []byte, []byte, bool) {
	parts := strings.Split(encodedHash, "$")
	// Leading empty segment from the initial '$' makes 6 parts total.
	if len(parts) != 6 || parts[1] != "argon2id" || parts[2] != "v="+strconv.Itoa(argon2.Version) {
		return Argon2Params{}, nil, nil, false
	}
	var params Argon2Params
	if _, err := fmt.Sscanf(parts[3], "m=%d,t=%d,p=%d", &params.MemoryKiB, &params.Time, &params.Threads); err != nil {
		return Argon2Params{}, nil, nil, false
	}
	salt, err := base64.RawStdEncoding.DecodeString(parts[4])
	if err != nil {
		return Argon2Params{}, nil, nil, false
	}
	expected, err := base64.RawStdEncoding.DecodeString(parts[5])
	if err != nil {
		return Argon2Params{}, nil, nil, false
	}
	if params.MemoryKiB == 0 || params.Time == 0 || params.Threads == 0 || len(salt) == 0 || len(expected) == 0 {
		return Argon2Params{}, nil, nil, false
	}
	return params, salt, expected, true
}

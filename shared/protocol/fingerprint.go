package protocol

import (
	"crypto/sha256"
	"encoding/base64"
	"strings"
)

// KeyFingerprintBytes is the number of leading SHA-256 bytes shown in a device
// public-key short fingerprint. Eight bytes (16 hex chars in colon-pairs) is
// short enough for humans to compare yet collision-resistant for this use.
const KeyFingerprintBytes = 8

// KeyFingerprint derives the human-comparable short fingerprint of a device HPKE
// public key from its stored base64 (standard encoding) form. It hashes the raw
// key bytes so the value is independent of base64 padding/whitespace, and is the
// single source of truth shared by the server console and the desktop client's
// about page for cross-device manual verification (prd/03-security-and-e2ee.md §5.3).
func KeyFingerprint(publicKeyBase64 string) string {
	raw, err := base64.StdEncoding.DecodeString(strings.TrimSpace(publicKeyBase64))
	if err != nil {
		// Fall back to hashing the string as-is so a malformed key still yields a
		// stable, comparable (if non-canonical) value rather than an empty one.
		raw = []byte(publicKeyBase64)
	}
	sum := sha256.Sum256(raw)
	return colonHex(sum[:KeyFingerprintBytes])
}

// CertFingerprint renders a certificate's SHA-256 (over its DER bytes) as the
// uppercase, colon-grouped hex string shown on the Web pairing page and pinned
// by clients. It is the single source of truth for the device-port certificate
// fingerprint format shared by the server (tlscert) and the client (apiclient),
// so both sides always agree on what the user is comparing.
func CertFingerprint(der []byte) string {
	sum := sha256.Sum256(der)
	return colonHex(sum[:])
}

// colonHex renders bytes as uppercase, colon-grouped hex, e.g. "AB:CD:EF".
func colonHex(b []byte) string {
	const hexdigits = "0123456789ABCDEF"
	parts := make([]string, len(b))
	for i, x := range b {
		parts[i] = string([]byte{hexdigits[x>>4], hexdigits[x&0x0f]})
	}
	return strings.Join(parts, ":")
}

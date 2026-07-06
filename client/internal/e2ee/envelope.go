// Package e2ee implements ClipBridge's end-to-end content encryption: device
// HPKE key pairs, the chunked AEAD body format, and per-device DEK wrapping. The
// server only ever sees the opaque ciphertext stream and the per-device wrapped
// DEKs — never the DEK or plaintext. See prd/03-security-and-e2ee.md §6.
//
// Ciphersuite (RFC 9180): KEM = ML-KEM-768-X25519, KDF = HKDF-SHA256,
// AEAD = AES-256-GCM.
package e2ee

import (
	"crypto/hpke"
	"crypto/rand"
	"encoding/base64"
	"fmt"

	"github.com/mokeyjay/clipbridge/shared/protocol"
)

// DEKLen is the content-encryption-key length in bytes (AES-256).
const DEKLen = 32

// DefaultChunkSize is the plaintext chunk size for the chunked AEAD body. Small
// content (text) becomes a single-chunk special case of the same format.
const DefaultChunkSize = 64 * 1024

// suiteKEM/KDF/AEAD return the fixed HPKE ciphersuite components.
func suiteKEM() hpke.KEM   { return hpke.MLKEM768X25519() }
func suiteKDF() hpke.KDF   { return hpke.HKDFSHA256() }
func suiteAEAD() hpke.AEAD { return hpke.AES256GCM() }

// Header carries the immutable context bound as AAD into both the body chunks and
// the per-device DEK wrap. Any mismatch on decryption is a hard failure.
type Header struct {
	ProtocolVersion int
	ItemID          string // content UUID
	SourceDeviceID  string // uploading device UUID
	ContentType     protocol.ContentType
}

// GenerateDeviceKey creates a new device HPKE key pair, returning the serialized
// private and public keys (RFC 9180 Serialize{Private,Public}Key form). The
// private bytes are written to the device credential file; the public bytes are
// registered with the server during pairing.
func GenerateDeviceKey() (privateKey, publicKey []byte, err error) {
	priv, err := suiteKEM().GenerateKey()
	if err != nil {
		return nil, nil, fmt.Errorf("e2ee: 生成设备密钥: %w", err)
	}
	privBytes, err := priv.Bytes()
	if err != nil {
		return nil, nil, fmt.Errorf("e2ee: 序列化私钥: %w", err)
	}
	return privBytes, priv.PublicKey().Bytes(), nil
}

// PublicKeyBase64 encodes a serialized public key for transport/registration.
func PublicKeyBase64(publicKey []byte) string {
	return base64.StdEncoding.EncodeToString(publicKey)
}

// Fingerprint returns the human-comparable short fingerprint for a serialized
// public key, matching what the server console and client about page display.
func Fingerprint(publicKey []byte) string {
	return protocol.KeyFingerprint(PublicKeyBase64(publicKey))
}

// NewDEK returns a fresh random content key. A unique DEK per content guarantees
// chunk nonces (derived from the chunk index) never repeat under the same key.
func NewDEK() ([]byte, error) {
	dek := make([]byte, DEKLen)
	if _, err := rand.Read(dek); err != nil {
		return nil, fmt.Errorf("e2ee: 生成内容密钥: %w", err)
	}
	return dek, nil
}

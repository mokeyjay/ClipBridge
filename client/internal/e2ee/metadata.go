package e2ee

import "fmt"

// metaAADLabel domain-separates the encrypted-metadata AAD from body chunks.
var metaAADLabel = []byte("clipbridge/aad/meta\x01")

// metaNonce is the fixed 12-byte nonce for the single metadata block. Its first
// byte is 1, distinct from every body-chunk nonce (whose first 4 bytes are 0), so
// (DEK, nonce) is never reused between the metadata block and the body.
func metaNonce() []byte {
	n := make([]byte, 12)
	n[0] = 1
	return n
}

// metaAAD binds the immutable header into the metadata block's AAD (no chunk
// index, since metadata is a single block).
func metaAAD(hdr Header) []byte {
	b := append([]byte(nil), metaAADLabel...)
	return appendHeader(b, hdr)
}

// SealMetadata encrypts a small metadata blob (e.g. filename, image dimensions,
// rich-text format flags) with the content DEK. The server stores the result
// opaquely alongside the ciphertext; only devices holding the DEK can read it.
func SealMetadata(dek []byte, hdr Header, plaintext []byte) ([]byte, error) {
	gcm, err := newGCM(dek)
	if err != nil {
		return nil, err
	}
	return gcm.Seal(nil, metaNonce(), plaintext, metaAAD(hdr)), nil
}

// OpenMetadata decrypts a metadata blob sealed by SealMetadata. The header must
// match the one used to seal, or authentication fails.
func OpenMetadata(dek []byte, hdr Header, sealed []byte) ([]byte, error) {
	gcm, err := newGCM(dek)
	if err != nil {
		return nil, err
	}
	plain, err := gcm.Open(nil, metaNonce(), sealed, metaAAD(hdr))
	if err != nil {
		return nil, fmt.Errorf("e2ee: 元数据认证失败: %w", err)
	}
	return plain, nil
}

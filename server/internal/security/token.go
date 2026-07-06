package security

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"fmt"
	"math/big"
)

// RandomToken returns a URL-safe, unpadded base64 string carrying numBytes of
// cryptographic entropy. Used for device tokens, poll tokens and session tokens.
func RandomToken(numBytes int) (string, error) {
	value := make([]byte, numBytes)
	if _, err := rand.Read(value); err != nil {
		return "", fmt.Errorf("security: 生成随机 token: %w", err)
	}
	return base64.RawURLEncoding.EncodeToString(value), nil
}

// TokenHash returns the hex-encoded SHA-256 of a token. The server stores only
// this irreversible hash; the plaintext token never touches the database.
func TokenHash(token string) string {
	sum := sha256.Sum256([]byte(token))
	return hex.EncodeToString(sum[:])
}

// RandomNumericCode returns a length-digit decimal string using rejection-free
// uniform sampling over 0-9. Used for the 6-digit pairing code.
func RandomNumericCode(length int) (string, error) {
	digits := make([]byte, length)
	for i := range digits {
		n, err := rand.Int(rand.Reader, big.NewInt(10))
		if err != nil {
			return "", fmt.Errorf("security: 生成数字配对码: %w", err)
		}
		digits[i] = byte('0' + n.Int64())
	}
	return string(digits), nil
}

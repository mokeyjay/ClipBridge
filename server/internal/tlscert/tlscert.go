// Package tlscert generates and loads the device port's long-lived self-signed
// TLS certificate. The certificate is created once on first boot and never
// rotated during normal operation so that the SHA-256 fingerprint clients pin
// stays stable. Regenerating it is an explicit operator action.
package tlscert

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"fmt"
	"math/big"
	"os"
	"path/filepath"
	"time"

	"github.com/mokeyjay/clipbridge/shared/protocol"
)

// validity is the self-signed certificate lifetime. Long enough that operators
// never hit rotation in normal use.
const validity = 10 * 365 * 24 * time.Hour

// EnsureCert loads the device certificate from dir, generating a new long-lived
// self-signed certificate (and 0600 key) if one is not already present. It
// returns the parsed TLS certificate and its colon-grouped SHA-256 fingerprint.
func EnsureCert(dir string) (tls.Certificate, string, error) {
	certPath := filepath.Join(dir, "server.crt")
	keyPath := filepath.Join(dir, "server.key")

	if fileExists(certPath) && fileExists(keyPath) {
		cert, err := tls.LoadX509KeyPair(certPath, keyPath)
		if err != nil {
			return tls.Certificate{}, "", fmt.Errorf("tlscert: load: %w", err)
		}
		return cert, FingerprintDER(cert.Certificate[0]), nil
	}

	if err := os.MkdirAll(dir, 0o700); err != nil {
		return tls.Certificate{}, "", fmt.Errorf("tlscert: mkdir: %w", err)
	}

	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return tls.Certificate{}, "", fmt.Errorf("tlscert: gen key: %w", err)
	}

	serial, err := rand.Int(rand.Reader, new(big.Int).Lsh(big.NewInt(1), 128))
	if err != nil {
		return tls.Certificate{}, "", fmt.Errorf("tlscert: serial: %w", err)
	}
	now := time.Now()
	tmpl := x509.Certificate{
		SerialNumber:          serial,
		Subject:               pkix.Name{CommonName: "ClipBridge Device Port"},
		NotBefore:             now.Add(-time.Hour),
		NotAfter:              now.Add(validity),
		KeyUsage:              x509.KeyUsageDigitalSignature | x509.KeyUsageKeyEncipherment,
		ExtKeyUsage:           []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
		BasicConstraintsValid: true,
		IsCA:                  true,
		DNSNames:              []string{"localhost"},
	}
	der, err := x509.CreateCertificate(rand.Reader, &tmpl, &tmpl, &key.PublicKey, key)
	if err != nil {
		return tls.Certificate{}, "", fmt.Errorf("tlscert: create cert: %w", err)
	}

	certPEM := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der})
	keyDER, err := x509.MarshalPKCS8PrivateKey(key)
	if err != nil {
		return tls.Certificate{}, "", fmt.Errorf("tlscert: marshal key: %w", err)
	}
	keyPEM := pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: keyDER})

	if err := os.WriteFile(certPath, certPEM, 0o600); err != nil {
		return tls.Certificate{}, "", fmt.Errorf("tlscert: write cert: %w", err)
	}
	if err := os.WriteFile(keyPath, keyPEM, 0o600); err != nil {
		return tls.Certificate{}, "", fmt.Errorf("tlscert: write key: %w", err)
	}

	cert, err := tls.X509KeyPair(certPEM, keyPEM)
	if err != nil {
		return tls.Certificate{}, "", fmt.Errorf("tlscert: pair: %w", err)
	}
	return cert, FingerprintDER(der), nil
}

// FingerprintDER returns the SHA-256 of a DER-encoded certificate as an
// uppercase, colon-grouped hex string, e.g. "AB:CD:...". This is the value shown
// on the pairing page and pinned by clients; the format lives in shared/protocol
// so the server and client always agree.
func FingerprintDER(der []byte) string {
	return protocol.CertFingerprint(der)
}

func fileExists(p string) bool {
	info, err := os.Stat(p)
	return err == nil && !info.IsDir()
}

package e2ee

import (
	"crypto/hpke"
	"fmt"
)

// WrapDEK encapsulates the content key to a target device's HPKE public key,
// binding the immutable header and target device UUID as HPKE info. The result is
// the opaque per-device wrapped DEK uploaded alongside the ciphertext.
func WrapDEK(targetPublicKey, dek []byte, hdr Header, targetDeviceID string) ([]byte, error) {
	pub, err := suiteKEM().NewPublicKey(targetPublicKey)
	if err != nil {
		return nil, fmt.Errorf("e2ee: 解析目标公钥: %w", err)
	}
	wrapped, err := hpke.Seal(pub, suiteKDF(), suiteAEAD(), wrapInfo(hdr, targetDeviceID), dek)
	if err != nil {
		return nil, fmt.Errorf("e2ee: 封装内容密钥: %w", err)
	}
	return wrapped, nil
}

// UnwrapDEK recovers the content key from a wrapped DEK using this device's HPKE
// private key. The header and target device UUID must match those used to wrap,
// or HPKE authentication fails.
func UnwrapDEK(privateKey, wrapped []byte, hdr Header, targetDeviceID string) ([]byte, error) {
	priv, err := suiteKEM().NewPrivateKey(privateKey)
	if err != nil {
		return nil, fmt.Errorf("e2ee: 解析设备私钥: %w", err)
	}
	dek, err := hpke.Open(priv, suiteKDF(), suiteAEAD(), wrapInfo(hdr, targetDeviceID), wrapped)
	if err != nil {
		return nil, fmt.Errorf("e2ee: 解封内容密钥: %w", err)
	}
	if len(dek) != DEKLen {
		return nil, fmt.Errorf("e2ee: 解封内容密钥长度异常 %d", len(dek))
	}
	return dek, nil
}

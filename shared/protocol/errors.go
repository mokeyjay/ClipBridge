// Package protocol holds the DTOs, enums, error codes and event types shared
// between the ClipBridge server and desktop client. Nothing in this package may
// import server- or client-specific code; it is the wire contract only.
package protocol

// ErrorCode is a stable, machine-readable error identifier. Clients must branch
// on these codes, never on human-facing messages. See prd/05-api-and-events.md.
type ErrorCode string

const (
	// ErrAuthRequired is returned when a request lacks valid authentication.
	ErrAuthRequired ErrorCode = "AUTH_REQUIRED"
	// ErrForbidden is returned when an authenticated caller lacks permission.
	ErrForbidden ErrorCode = "FORBIDDEN"
	// ErrUserDisabled is returned when the owning user has been disabled.
	ErrUserDisabled ErrorCode = "USER_DISABLED"
	// ErrDeviceDisabled is returned when the calling device has been disabled.
	ErrDeviceDisabled ErrorCode = "DEVICE_DISABLED"
	// ErrDeviceRevoked is returned when the calling device has been revoked.
	ErrDeviceRevoked ErrorCode = "DEVICE_REVOKED"
	// ErrPairingCodeInvalid is returned when a pairing code does not match.
	ErrPairingCodeInvalid ErrorCode = "PAIRING_CODE_INVALID"
	// ErrPairingCodeExpired is returned when a pairing code has expired.
	ErrPairingCodeExpired ErrorCode = "PAIRING_CODE_EXPIRED"
	// ErrPairingRateLimited is returned when pairing attempts exceed the IP limit.
	ErrPairingRateLimited ErrorCode = "PAIRING_RATE_LIMITED"
	// ErrNoOnlineTargets is returned when an upload has no eligible online targets.
	ErrNoOnlineTargets ErrorCode = "NO_ONLINE_TARGETS"
	// ErrContentTypeNotAllowed is returned when the content type is not permitted.
	ErrContentTypeNotAllowed ErrorCode = "CONTENT_TYPE_NOT_ALLOWED"
	// ErrContentTooLarge is returned when content exceeds the effective max size.
	ErrContentTooLarge ErrorCode = "CONTENT_TOO_LARGE"
	// ErrDeliveryExpired is returned when a delivery has already expired.
	ErrDeliveryExpired ErrorCode = "DELIVERY_EXPIRED"
	// ErrCiphertextIntegrityFailed is returned when size/hash verification fails.
	ErrCiphertextIntegrityFailed ErrorCode = "CIPHERTEXT_INTEGRITY_FAILED"
	// ErrProtocolVersionUnsupported is returned when the client protocol version
	// falls outside the server's supported range.
	ErrProtocolVersionUnsupported ErrorCode = "PROTOCOL_VERSION_UNSUPPORTED"

	// ErrCertificateFingerprintChanged is a client-local error: the server device
	// port certificate fingerprint no longer matches the pinned value.
	ErrCertificateFingerprintChanged ErrorCode = "CERTIFICATE_FINGERPRINT_CHANGED"
	// ErrDeviceKeyFingerprintChanged is a client-local error: a target device's
	// public key fingerprint no longer matches the TOFU cache.
	ErrDeviceKeyFingerprintChanged ErrorCode = "DEVICE_KEY_FINGERPRINT_CHANGED"
)

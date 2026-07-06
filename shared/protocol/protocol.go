package protocol

// ProtocolVersion is the wire protocol version this build speaks. The device
// declares it during the WSS handshake and repeats it in every upload manifest;
// the server rejects mismatches with ErrProtocolVersionUnsupported.
const ProtocolVersion = 1

// MinSupportedProtocolVersion and MaxSupportedProtocolVersion bound the range a
// server will accept during the WSS handshake.
const (
	MinSupportedProtocolVersion = 1
	MaxSupportedProtocolVersion = 1
)

// ContentType enumerates the clipboard payload kinds.
type ContentType string

const (
	ContentText     ContentType = "text"
	ContentImage    ContentType = "image"
	ContentFile     ContentType = "file"
	ContentRichText ContentType = "rich_text"
)

// AllContentTypes returns every known content type, in canonical order. Used as
// the default instance allowlist (admin may then narrow it).
func AllContentTypes() []ContentType {
	return []ContentType{ContentText, ContentImage, ContentFile, ContentRichText}
}

// SyncDirection is a client-local policy controlling whether this device may
// upload, download, or both.
type SyncDirection string

const (
	DirectionBidirectional SyncDirection = "bidirectional"
	DirectionUploadOnly    SyncDirection = "upload_only"
	DirectionDownloadOnly  SyncDirection = "download_only"
)

// NotifyPolicy is a client-local notification verbosity setting.
type NotifyPolicy string

const (
	NotifyQuiet   NotifyPolicy = "quiet"
	NotifyDefault NotifyPolicy = "default"
	NotifyVerbose NotifyPolicy = "verbose"
)

// Platform identifies a device operating system family.
type Platform string

const (
	PlatformDarwin  Platform = "darwin"
	PlatformWindows Platform = "windows"
)

// UserStatus is the lifecycle state of a user account.
type UserStatus string

const (
	UserActive   UserStatus = "active"
	UserDisabled UserStatus = "disabled"
)

// DeviceStatus is the lifecycle state of a paired device.
type DeviceStatus string

const (
	DeviceActive   DeviceStatus = "active"
	DeviceDisabled DeviceStatus = "disabled"
	DeviceRevoked  DeviceStatus = "revoked"
)

// ItemStatus is the lifecycle state of an uploaded ciphertext item.
type ItemStatus string

const (
	ItemActive    ItemStatus = "active"
	ItemCompleted ItemStatus = "completed"
	ItemExpired   ItemStatus = "expired"
)

// DeliveryStatus is the per-target delivery state.
type DeliveryStatus string

const (
	DeliveryPending  DeliveryStatus = "pending"
	DeliveryAcked    DeliveryStatus = "acked"
	DeliveryRejected DeliveryStatus = "rejected"
	DeliveryExpired  DeliveryStatus = "expired"
)

// PairingCodeStatus is the lifecycle state of a 6-digit pairing code.
type PairingCodeStatus string

const (
	PairingCodeActive    PairingCodeStatus = "active"
	PairingCodeConsumed  PairingCodeStatus = "consumed"
	PairingCodeCancelled PairingCodeStatus = "cancelled"
	PairingCodeExpired   PairingCodeStatus = "expired"
)

// PairingRequestStatus is the lifecycle state of a device pairing request.
type PairingRequestStatus string

const (
	PairingRequestPending   PairingRequestStatus = "pending"
	PairingRequestConfirmed PairingRequestStatus = "confirmed"
	PairingRequestRejected  PairingRequestStatus = "rejected"
	PairingRequestExpired   PairingRequestStatus = "expired"
	PairingRequestClaimed   PairingRequestStatus = "claimed"
)

// SubjectType distinguishes the two Web session principals.
type SubjectType string

const (
	SubjectAdmin SubjectType = "admin"
	SubjectUser  SubjectType = "user"
)

// RejectReason is the enumerated, body-only reason a target device rejects a
// delivery. Never carries free text.
type RejectReason string

const (
	RejectUserDeclined           RejectReason = "USER_DECLINED"
	RejectConfirmationTimeout    RejectReason = "CONFIRMATION_TIMEOUT"
	RejectPolicyBlocked          RejectReason = "POLICY_BLOCKED"
	RejectDecryptFailed          RejectReason = "DECRYPT_FAILED"
	RejectKeyFingerprintMismatch RejectReason = "KEY_FINGERPRINT_MISMATCH"
)

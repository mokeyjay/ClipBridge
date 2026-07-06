package protocol

// ErrorBody is the structured error nested in ErrorResponse.
type ErrorBody struct {
	Code    ErrorCode `json:"code"`
	Message string    `json:"message,omitempty"`
}

// ErrorResponse is the standard error envelope. Clients branch only on Code.
type ErrorResponse struct {
	RequestID string    `json:"request_id"`
	Error     ErrorBody `json:"error"`
}

// LoginRequest is the body for POST /auth/login.
type LoginRequest struct {
	Username string `json:"username"`
	Password string `json:"password"`
}

// MeResponse describes the currently authenticated Web principal.
type MeResponse struct {
	SubjectType SubjectType `json:"subject_type"`
	SubjectID   string      `json:"subject_id"`
	Username    string      `json:"username"`
}

// ChangePasswordRequest is the body for PATCH /auth/password.
type ChangePasswordRequest struct {
	CurrentPassword string `json:"current_password"`
	NewPassword     string `json:"new_password"`
}

// PairingCodeResponse is returned when a user creates or views a pairing code.
// The plaintext code is only present in the creation response.
type PairingCodeResponse struct {
	Code                    string `json:"code,omitempty"`
	ExpiresAt               string `json:"expires_at"`
	ServerName              string `json:"server_name"`
	ServerFingerprintSHA256 string `json:"server_fingerprint_sha256"`
}

// SubmitPairingRequest is the client body for POST /pairing-requests.
type SubmitPairingRequest struct {
	Code          string `json:"code"`
	DeviceName    string `json:"device_name"`
	Platform      string `json:"platform"`
	ClientVersion string `json:"client_version"`
	HPKEPublicKey string `json:"hpke_public_key"`
}

// SubmitPairingResponse returns the one-time poll token used to await confirm.
type SubmitPairingResponse struct {
	RequestID string `json:"request_id"`
	PollToken string `json:"poll_token"`
	ExpiresAt string `json:"expires_at"`
}

// PairingResultDevice is the device identity returned on a confirmed pairing.
type PairingResultDevice struct {
	ID       string `json:"id"`
	UserID   string `json:"user_id"`
	ServerID string `json:"server_id"`
}

// PairingResultResponse is returned to a polling client. DeviceToken is present
// exactly once, on first claim of a confirmed request.
type PairingResultResponse struct {
	Status      PairingRequestStatus `json:"status"`
	Device      *PairingResultDevice `json:"device,omitempty"`
	DeviceToken string               `json:"device_token,omitempty"`
}

// TargetDevice is one eligible online target returned by GET /device/targets.
// DeviceName 用于客户端在 TOFU 指纹告警等界面里展示可读的设备名。
type TargetDevice struct {
	DeviceID      string `json:"device_id"`
	DeviceName    string `json:"device_name,omitempty"`
	HPKEPublicKey string `json:"hpke_public_key"`
}

// TargetsResponse lists the current online targets for the source device's user.
type TargetsResponse struct {
	Targets []TargetDevice `json:"targets"`
}

// PeerDevice 是 GET /device/peers 返回的同用户单台设备（用于客户端「关于」页
// 的公钥指纹互验展示，prd/03 §5.3）。KeyFingerprint 与 Web「我的设备」一致，
// 由 shared/protocol.KeyFingerprint 统一派生。
type PeerDevice struct {
	DeviceID       string `json:"device_id"`
	Name           string `json:"name"`
	Platform       string `json:"platform"`
	Status         string `json:"status"`
	Online         bool   `json:"online"`
	KeyFingerprint string `json:"key_fingerprint"`
	Self           bool   `json:"self"` // 是否为调用方设备本身
}

// PeersResponse lists all of the calling device's sibling devices (same user).
type PeersResponse struct {
	Peers []PeerDevice `json:"peers"`
}

// EffectiveConfig is the three-layer resolved policy the device enforces locally.
// MaxSyncSizeBytes is already clamped by the server instance ceiling.
type EffectiveConfig struct {
	// ServerName is the instance display name (admin-configurable); surfaced so the
	// client overview can show it and keep it in sync with admin renames.
	ServerName               string        `json:"server_name"`
	MaxSyncSizeBytes         int64         `json:"max_sync_size_bytes"`
	AllowedTypes             []ContentType `json:"allowed_types"`
	MaxAutoUploadSizeBytes   int64         `json:"max_auto_upload_size_bytes"`
	MaxAutoDownloadSizeBytes int64         `json:"max_auto_download_size_bytes"`
	// FileTTLDays is the user-level default retention for received files (days);
	// the device may inherit it or override it locally on the client.
	FileTTLDays int64 `json:"file_ttl_days"`
}

// DeviceSettings is a device's per-field inherit/override sync policy as exchanged
// over GET/PATCH /device/settings. A nil pointer (or inherit=true) means the field
// falls back to the user-level default; a non-nil override applies locally.
type DeviceSettings struct {
	MaxSyncSizeInherit       bool          `json:"max_sync_size_inherit"`
	MaxSyncSizeBytes         *int64        `json:"max_sync_size_bytes"`
	AllowedTypesInherit      bool          `json:"allowed_types_inherit"`
	AllowedTypes             []ContentType `json:"allowed_types"`
	MaxAutoUploadInherit     bool          `json:"max_auto_upload_inherit"`
	MaxAutoUploadSizeBytes   *int64        `json:"max_auto_upload_size_bytes"`
	MaxAutoDownloadInherit   bool          `json:"max_auto_download_inherit"`
	MaxAutoDownloadSizeBytes *int64        `json:"max_auto_download_size_bytes"`
}

// DeliveryTarget pairs a target device with its wrapped DEK in an upload manifest.
type DeliveryTarget struct {
	TargetDeviceID string `json:"target_device_id"`
	WrappedDEK     string `json:"wrapped_dek"`
}

// UploadManifest is the JSON part of a POST /clipboard/items multipart upload.
// Sizes and the SHA-256 cover the final ciphertext stream including every chunk
// authentication tag. Text and other small content use TotalChunks == 1.
type UploadManifest struct {
	ProtocolVersion     int         `json:"protocol_version"`
	ItemID              string      `json:"item_id"`
	ContentType         ContentType `json:"content_type"`
	CiphertextSizeBytes int64       `json:"ciphertext_size_bytes"`
	CiphertextSHA256    string      `json:"ciphertext_sha256"`
	ChunkSizeBytes      int         `json:"chunk_size_bytes"`
	TotalChunks         int         `json:"total_chunks"`
	// EncryptedMetadata is the DEK-sealed, base64 metadata blob (filename, image
	// dimensions, rich-text flags). Opaque to the server; empty for plain text.
	EncryptedMetadata string           `json:"encrypted_metadata,omitempty"`
	Deliveries        []DeliveryTarget `json:"deliveries"`
}

// UploadResponse is returned after a successful ciphertext upload.
type UploadResponse struct {
	ItemID                  string   `json:"item_id"`
	ExpiresAt               string   `json:"expires_at"`
	AcceptedTargetDeviceIDs []string `json:"accepted_target_device_ids"`
}

// DeliveryManifest is returned by GET /clipboard/deliveries/{id}. It carries only
// this device's wrapped DEK plus the metadata needed to stream and verify.
type DeliveryManifest struct {
	DeliveryID          string      `json:"delivery_id"`
	ItemID              string      `json:"item_id"`
	SourceDeviceID      string      `json:"source_device_id"`
	ContentType         ContentType `json:"content_type"`
	CiphertextSizeBytes int64       `json:"ciphertext_size_bytes"`
	CiphertextSHA256    string      `json:"ciphertext_sha256"`
	ChunkSizeBytes      int         `json:"chunk_size_bytes"`
	TotalChunks         int         `json:"total_chunks"`
	WrappedDEK          string      `json:"wrapped_dek"`
	EncryptedMetadata   string      `json:"encrypted_metadata,omitempty"`
	ExpiresAt           string      `json:"expires_at"`
}

// PendingDeliveriesResponse lists this device's unresolved, unexpired deliveries.
type PendingDeliveriesResponse struct {
	Deliveries []DeliveryManifest `json:"deliveries"`
}

// RejectRequest is the body for POST /clipboard/deliveries/{id}/reject.
type RejectRequest struct {
	Reason RejectReason `json:"reason"`
}

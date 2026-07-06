package store

import "github.com/mokeyjay/clipbridge/shared/protocol"

// Models mirror the rows in 0001_init.sql. Timestamps are Unix seconds (UTC);
// the API layer renders them as RFC 3339 at its boundary. Pointer fields map to
// nullable columns.

// Admin is the single administrator account.
type Admin struct {
	ID           string
	Username     string
	PasswordHash string
	CreatedAt    int64
	UpdatedAt    int64
}

// User is a normal account.
type User struct {
	ID           string
	Username     string
	PasswordHash string
	Status       protocol.UserStatus
	CreatedAt    int64
	UpdatedAt    int64
}

// WebSession is a browser session for an admin or user principal.
type WebSession struct {
	ID          string
	SubjectType protocol.SubjectType
	SubjectID   string
	TokenHash   string
	ExpiresAt   int64
	CreatedAt   int64
	LastSeenAt  int64
}

// ServerSettings is the single-row instance configuration.
type ServerSettings struct {
	ServerID             string
	ServerName           string
	RegistrationEnabled  bool
	MaxSyncSizeBytes     int64
	AllowedTypes         []protocol.ContentType // instance-level allowlist (hard ceiling)
	CiphertextTTLSeconds int64
	SyncLogRetentionDays int64
	CreatedAt            int64
	UpdatedAt            int64
}

// UserSettings is a user's sync-policy default template.
type UserSettings struct {
	UserID                   string
	MaxSyncSizeBytes         int64
	AllowedTypes             []protocol.ContentType
	MaxAutoUploadSizeBytes   int64
	MaxAutoDownloadSizeBytes int64
	FileTTLDays              int64 // received-file retention default (days)
	UpdatedAt                int64
}

// DeviceSettings holds per-field inherit/override flags for a device.
type DeviceSettings struct {
	DeviceID                 string
	MaxSyncSizeInherit       bool
	MaxSyncSizeBytes         *int64
	AllowedTypesInherit      bool
	AllowedTypes             []protocol.ContentType // nil when inheriting
	MaxAutoUploadInherit     bool
	MaxAutoUploadSizeBytes   *int64
	MaxAutoDownloadInherit   bool
	MaxAutoDownloadSizeBytes *int64
	UpdatedAt                int64
}

// Device is a paired device.
type Device struct {
	ID            string
	UserID        string
	Name          string
	Platform      protocol.Platform
	ClientVersion string
	HPKEPublicKey string
	Status        protocol.DeviceStatus
	LastSeenAt    *int64
	CreatedAt     int64
	UpdatedAt     int64
}

// DeviceToken is one issued device bearer token (stored only as a hash).
type DeviceToken struct {
	ID         string
	DeviceID   string
	TokenHash  string
	ExpiresAt  *int64
	RevokedAt  *int64
	CreatedAt  int64
	LastUsedAt *int64
}

// PairingCode is a 6-digit pairing code (stored only as a hash).
type PairingCode struct {
	ID        string
	UserID    string
	CodeHash  string
	Status    protocol.PairingCodeStatus
	ExpiresAt int64
	CreatedAt int64
}

// PairingRequest is a device's request to pair against a code.
type PairingRequest struct {
	ID                string
	PairingCodeID     string
	DeviceName        string
	Platform          protocol.Platform
	ClientVersion     string
	HPKEPublicKey     string
	PollTokenHash     string
	Status            protocol.PairingRequestStatus
	ConfirmedDeviceID *string
	ExpiresAt         int64
	CreatedAt         int64
	UpdatedAt         int64
}

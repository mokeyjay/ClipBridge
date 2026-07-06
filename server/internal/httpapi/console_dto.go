package httpapi

import (
	"github.com/mokeyjay/clipbridge/server/internal/store"
	"github.com/mokeyjay/clipbridge/shared/protocol"
)

// These response shapes are specific to the Web console and intentionally omit
// secrets (password hashes, token hashes, raw public keys are summarized).

// userView is a user row as shown in the console.
type userView struct {
	ID        string `json:"id"`
	Username  string `json:"username"`
	Status    string `json:"status"`
	CreatedAt string `json:"created_at"`
}

// deviceView is a device row as shown in the console, including the public-key
// short fingerprint used for cross-device manual verification.
type deviceView struct {
	ID             string `json:"id"`
	Name           string `json:"name"`
	Platform       string `json:"platform"`
	ClientVersion  string `json:"client_version"`
	Status         string `json:"status"`
	KeyFingerprint string `json:"key_fingerprint"`
	Online         bool   `json:"online"`
	LastSeenAt     string `json:"last_seen_at,omitempty"`
	CreatedAt      string `json:"created_at"`
}

// serverSettingsView mirrors the admin-editable instance configuration.
type serverSettingsView struct {
	ServerID             string                 `json:"server_id"`
	ServerName           string                 `json:"server_name"`
	MaxSyncSizeBytes     int64                  `json:"max_sync_size_bytes"`
	AllowedTypes         []protocol.ContentType `json:"allowed_types"`
	CiphertextTTLSeconds int64                  `json:"ciphertext_ttl_seconds"`
	SyncLogRetentionDays int64                  `json:"sync_log_retention_days"`
}

// syncLogView is one body-free sync-log row for the admin console, with the
// owning user's name and source/target device names resolved for display.
type syncLogView struct {
	ID                  string `json:"id"`
	Username            string `json:"username,omitempty"`
	SourceDeviceName    string `json:"source_device_name,omitempty"`
	TargetDeviceName    string `json:"target_device_name,omitempty"`
	EventType           string `json:"event_type"`
	ContentType         string `json:"content_type,omitempty"`
	CiphertextSizeBytes int64  `json:"ciphertext_size_bytes,omitempty"`
	Result              string `json:"result"`
	ErrorCode           string `json:"error_code,omitempty"`
	CreatedAt           string `json:"created_at"`
}

// userSettingsView mirrors a user's sync-policy template.
type userSettingsView struct {
	MaxSyncSizeBytes         int64                  `json:"max_sync_size_bytes"`
	AllowedTypes             []protocol.ContentType `json:"allowed_types"`
	MaxAutoUploadSizeBytes   int64                  `json:"max_auto_upload_size_bytes"`
	MaxAutoDownloadSizeBytes int64                  `json:"max_auto_download_size_bytes"`
	FileTTLDays              int64                  `json:"file_ttl_days"`
}

// toUserView projects a store user to its console view.
func toUserView(u *store.User) userView {
	return userView{ID: u.ID, Username: u.Username, Status: string(u.Status), CreatedAt: rfc3339(u.CreatedAt)}
}

// toDeviceView projects a store device to its console view.
func toDeviceView(d *store.Device) deviceView {
	return deviceView{
		ID: d.ID, Name: d.Name, Platform: string(d.Platform), ClientVersion: d.ClientVersion,
		Status: string(d.Status), KeyFingerprint: protocol.KeyFingerprint(d.HPKEPublicKey),
		LastSeenAt: rfc3339Ptr(d.LastSeenAt), CreatedAt: rfc3339(d.CreatedAt),
	}
}

// toSyncLogView projects a joined store sync-log row to its console view.
func toSyncLogView(l *store.SyncLogRow) syncLogView {
	v := syncLogView{ID: l.ID, EventType: l.EventType, Result: l.Result, CreatedAt: rfc3339(l.CreatedAt)}
	if l.CiphertextSizeBytes != nil {
		v.CiphertextSizeBytes = *l.CiphertextSizeBytes
	}
	if l.Username != nil {
		v.Username = *l.Username
	}
	if l.SourceDeviceName != nil {
		v.SourceDeviceName = *l.SourceDeviceName
	}
	if l.TargetDeviceName != nil {
		v.TargetDeviceName = *l.TargetDeviceName
	}
	if l.ContentType != nil {
		v.ContentType = *l.ContentType
	}
	if l.ErrorCode != nil {
		v.ErrorCode = *l.ErrorCode
	}
	return v
}

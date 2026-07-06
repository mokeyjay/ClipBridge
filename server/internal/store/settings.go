package store

import (
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/mokeyjay/clipbridge/shared/protocol"
)

// encodeTypes marshals a content-type slice to the JSON array stored in SQLite.
func encodeTypes(types []protocol.ContentType) (string, error) {
	if types == nil {
		types = []protocol.ContentType{}
	}
	b, err := json.Marshal(types)
	if err != nil {
		return "", fmt.Errorf("store: 编码内容类型: %w", err)
	}
	return string(b), nil
}

// decodeTypes parses the stored JSON array back into a content-type slice.
func decodeTypes(raw string) ([]protocol.ContentType, error) {
	var types []protocol.ContentType
	if err := json.Unmarshal([]byte(raw), &types); err != nil {
		return nil, fmt.Errorf("store: 解析内容类型: %w", err)
	}
	return types, nil
}

// EnsureServerSettings inserts the single settings row (id=1) with the given
// server UUID if it does not exist yet. It is idempotent.
func (s *Store) EnsureServerSettings(serverID string) error {
	now := s.nowUnix()
	_, err := s.db.Exec(
		`INSERT INTO server_settings(id, server_id, created_at, updated_at) VALUES (1, ?, ?, ?)
		 ON CONFLICT(id) DO NOTHING`,
		serverID, now, now,
	)
	return err
}

// GetServerSettings returns the single-row instance configuration.
func (s *Store) GetServerSettings() (*ServerSettings, error) {
	ss := &ServerSettings{}
	var reg int
	var typesRaw string
	err := s.db.QueryRow(
		`SELECT server_id, server_name, registration_enabled, max_sync_size_bytes, allowed_types,
		        ciphertext_ttl_seconds, sync_log_retention_days, created_at, updated_at
		 FROM server_settings WHERE id = 1`,
	).Scan(&ss.ServerID, &ss.ServerName, &reg, &ss.MaxSyncSizeBytes, &typesRaw,
		&ss.CiphertextTTLSeconds, &ss.SyncLogRetentionDays, &ss.CreatedAt, &ss.UpdatedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	types, err := decodeTypes(typesRaw)
	if err != nil {
		return nil, err
	}
	ss.AllowedTypes = types
	ss.RegistrationEnabled = reg != 0
	return ss, nil
}

// UpdateServerSettings persists the admin-editable instance fields.
func (s *Store) UpdateServerSettings(serverName string, registrationEnabled bool, maxSyncSizeBytes, syncLogRetentionDays int64, allowedTypes []protocol.ContentType) error {
	reg := 0
	if registrationEnabled {
		reg = 1
	}
	typesRaw, err := encodeTypes(allowedTypes)
	if err != nil {
		return err
	}
	_, err = s.db.Exec(
		`UPDATE server_settings SET server_name = ?, registration_enabled = ?, max_sync_size_bytes = ?,
		        allowed_types = ?, sync_log_retention_days = ?, updated_at = ? WHERE id = 1`,
		serverName, reg, maxSyncSizeBytes, typesRaw, syncLogRetentionDays, s.nowUnix(),
	)
	return err
}

// GetUserSettings returns a user's sync-policy template.
func (s *Store) GetUserSettings(userID string) (*UserSettings, error) {
	us := &UserSettings{UserID: userID}
	var typesRaw string
	err := s.db.QueryRow(
		`SELECT max_sync_size_bytes, allowed_types, max_auto_upload_size_bytes,
		        max_auto_download_size_bytes, file_ttl_days, updated_at
		 FROM user_settings WHERE user_id = ?`, userID,
	).Scan(&us.MaxSyncSizeBytes, &typesRaw, &us.MaxAutoUploadSizeBytes, &us.MaxAutoDownloadSizeBytes, &us.FileTTLDays, &us.UpdatedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	types, err := decodeTypes(typesRaw)
	if err != nil {
		return nil, err
	}
	us.AllowedTypes = types
	return us, nil
}

// UpdateUserSettings persists a user's sync-policy template.
func (s *Store) UpdateUserSettings(us *UserSettings) error {
	typesRaw, err := encodeTypes(us.AllowedTypes)
	if err != nil {
		return err
	}
	_, err = s.db.Exec(
		`UPDATE user_settings SET max_sync_size_bytes = ?, allowed_types = ?,
		        max_auto_upload_size_bytes = ?, max_auto_download_size_bytes = ?,
		        file_ttl_days = ?, updated_at = ?
		 WHERE user_id = ?`,
		us.MaxSyncSizeBytes, typesRaw, us.MaxAutoUploadSizeBytes, us.MaxAutoDownloadSizeBytes, us.FileTTLDays, s.nowUnix(), us.UserID,
	)
	return err
}

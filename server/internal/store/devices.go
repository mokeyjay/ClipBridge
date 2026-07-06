package store

import (
	"database/sql"
	"errors"

	"github.com/mokeyjay/clipbridge/shared/protocol"
)

const deviceColumns = `id, user_id, name, platform, client_version, hpke_public_key, status, last_seen_at, created_at, updated_at`

// scanDevice reads a device row.
func scanDevice(scan func(dest ...any) error) (*Device, error) {
	d := &Device{}
	var platform, status string
	if err := scan(&d.ID, &d.UserID, &d.Name, &platform, &d.ClientVersion, &d.HPKEPublicKey, &status, &d.LastSeenAt, &d.CreatedAt, &d.UpdatedAt); err != nil {
		return nil, err
	}
	d.Platform = protocol.Platform(platform)
	d.Status = protocol.DeviceStatus(status)
	return d, nil
}

// CreateDevice inserts a device plus its default (fully inheriting) device_settings
// row in one transaction. Called when a user confirms a pairing request.
func (s *Store) CreateDevice(userID, name string, platform protocol.Platform, clientVersion, hpkePublicKey string) (*Device, error) {
	now := s.nowUnix()
	d := &Device{
		ID: newID(), UserID: userID, Name: name, Platform: platform,
		ClientVersion: clientVersion, HPKEPublicKey: hpkePublicKey,
		Status: protocol.DeviceActive, CreatedAt: now, UpdatedAt: now,
	}
	tx, err := s.db.Begin()
	if err != nil {
		return nil, err
	}
	defer func() { _ = tx.Rollback() }()

	if _, err := tx.Exec(
		`INSERT INTO devices(id, user_id, name, platform, client_version, hpke_public_key, status, created_at, updated_at)
		 VALUES (?,?,?,?,?,?,?,?,?)`,
		d.ID, d.UserID, d.Name, string(d.Platform), d.ClientVersion, d.HPKEPublicKey, string(d.Status), d.CreatedAt, d.UpdatedAt,
	); err != nil {
		return nil, err
	}
	if _, err := tx.Exec(`INSERT INTO device_settings(device_id, updated_at) VALUES (?, ?)`, d.ID, now); err != nil {
		return nil, err
	}
	if err := tx.Commit(); err != nil {
		return nil, err
	}
	return d, nil
}

// GetDeviceByID looks up a device.
func (s *Store) GetDeviceByID(id string) (*Device, error) {
	d, err := scanDevice(s.db.QueryRow(`SELECT `+deviceColumns+` FROM devices WHERE id = ?`, id).Scan)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	return d, err
}

// ListDevicesByUser returns a user's devices ordered by creation time.
func (s *Store) ListDevicesByUser(userID string) ([]*Device, error) {
	rows, err := s.db.Query(`SELECT `+deviceColumns+` FROM devices WHERE user_id = ? ORDER BY created_at`, userID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []*Device
	for rows.Next() {
		d, err := scanDevice(rows.Scan)
		if err != nil {
			return nil, err
		}
		out = append(out, d)
	}
	return out, rows.Err()
}

// UpdateDeviceName renames a device.
func (s *Store) UpdateDeviceName(id, name string) error {
	_, err := s.db.Exec(`UPDATE devices SET name = ?, updated_at = ? WHERE id = ?`, name, s.nowUnix(), id)
	return err
}

// SetDeviceStatus sets a device's lifecycle status (active/disabled/revoked).
func (s *Store) SetDeviceStatus(id string, status protocol.DeviceStatus) error {
	_, err := s.db.Exec(`UPDATE devices SET status = ?, updated_at = ? WHERE id = ?`, string(status), s.nowUnix(), id)
	return err
}

// UpdateDeviceLastSeen records the last online time (throttled by the caller).
func (s *Store) UpdateDeviceLastSeen(id string, at int64) error {
	_, err := s.db.Exec(`UPDATE devices SET last_seen_at = ? WHERE id = ?`, at, id)
	return err
}

// CountDevices returns the total number of registered devices.
func (s *Store) CountDevices() (int, error) {
	var n int
	err := s.db.QueryRow(`SELECT COUNT(*) FROM devices`).Scan(&n)
	return n, err
}

// DeleteDevice removes a device record and its dependent rows (tokens, settings).
// Used to purge a revoked device the user no longer wants listed.
func (s *Store) DeleteDevice(id string) error {
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	for _, q := range []string{
		`DELETE FROM device_tokens WHERE device_id = ?`,
		`DELETE FROM device_settings WHERE device_id = ?`,
		`DELETE FROM devices WHERE id = ?`,
	} {
		if _, err := tx.Exec(q, id); err != nil {
			return err
		}
	}
	return tx.Commit()
}

// CreateDeviceToken stores a new device token by its hash and returns the row.
func (s *Store) CreateDeviceToken(deviceID, tokenHash string) (*DeviceToken, error) {
	now := s.nowUnix()
	dt := &DeviceToken{ID: newID(), DeviceID: deviceID, TokenHash: tokenHash, CreatedAt: now}
	_, err := s.db.Exec(
		`INSERT INTO device_tokens(id, device_id, token_hash, created_at) VALUES (?,?,?,?)`,
		dt.ID, dt.DeviceID, dt.TokenHash, dt.CreatedAt,
	)
	if err != nil {
		return nil, err
	}
	return dt, nil
}

// AuthenticateDeviceToken resolves a device-token hash to its device, enforcing
// that the token is not revoked/expired. It does not check device status; the
// caller layers that policy so it can return the right error code. Returns
// ErrNotFound when no usable token matches.
func (s *Store) AuthenticateDeviceToken(tokenHash string) (*Device, *DeviceToken, error) {
	dt := &DeviceToken{}
	err := s.db.QueryRow(
		`SELECT id, device_id, token_hash, expires_at, revoked_at, created_at, last_used_at
		 FROM device_tokens WHERE token_hash = ?`, tokenHash,
	).Scan(&dt.ID, &dt.DeviceID, &dt.TokenHash, &dt.ExpiresAt, &dt.RevokedAt, &dt.CreatedAt, &dt.LastUsedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil, ErrNotFound
	}
	if err != nil {
		return nil, nil, err
	}
	now := s.nowUnix()
	if dt.RevokedAt != nil || (dt.ExpiresAt != nil && *dt.ExpiresAt <= now) {
		return nil, nil, ErrNotFound
	}
	device, err := s.GetDeviceByID(dt.DeviceID)
	if err != nil {
		return nil, nil, err
	}
	return device, dt, nil
}

// TouchDeviceToken records last_used_at for a token (throttled by the caller).
func (s *Store) TouchDeviceToken(id string, at int64) error {
	_, err := s.db.Exec(`UPDATE device_tokens SET last_used_at = ? WHERE id = ?`, at, id)
	return err
}

// RevokeDeviceTokens marks every token of a device revoked (rotation/revocation).
func (s *Store) RevokeDeviceTokens(deviceID string) error {
	_, err := s.db.Exec(
		`UPDATE device_tokens SET revoked_at = ? WHERE device_id = ? AND revoked_at IS NULL`,
		s.nowUnix(), deviceID,
	)
	return err
}

// GetDeviceSettings returns a device's inherit/override flags.
func (s *Store) GetDeviceSettings(deviceID string) (*DeviceSettings, error) {
	ds := &DeviceSettings{DeviceID: deviceID}
	var (
		maxInherit, typesInherit, upInherit, downInherit int
		typesRaw                                         sql.NullString
	)
	err := s.db.QueryRow(
		`SELECT max_sync_size_inherit, max_sync_size_bytes, allowed_types_inherit, allowed_types,
		        max_auto_upload_size_inherit, max_auto_upload_size_bytes,
		        max_auto_download_size_inherit, max_auto_download_size_bytes, updated_at
		 FROM device_settings WHERE device_id = ?`, deviceID,
	).Scan(&maxInherit, &ds.MaxSyncSizeBytes, &typesInherit, &typesRaw,
		&upInherit, &ds.MaxAutoUploadSizeBytes, &downInherit, &ds.MaxAutoDownloadSizeBytes, &ds.UpdatedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	ds.MaxSyncSizeInherit = maxInherit != 0
	ds.AllowedTypesInherit = typesInherit != 0
	ds.MaxAutoUploadInherit = upInherit != 0
	ds.MaxAutoDownloadInherit = downInherit != 0
	if typesRaw.Valid {
		types, err := decodeTypes(typesRaw.String)
		if err != nil {
			return nil, err
		}
		ds.AllowedTypes = types
	}
	return ds, nil
}

// UpdateDeviceSettings persists a device's inherit/override flags. Override
// columns are written only when the corresponding field is not inheriting.
func (s *Store) UpdateDeviceSettings(ds *DeviceSettings) error {
	var typesRaw any
	if !ds.AllowedTypesInherit && ds.AllowedTypes != nil {
		raw, err := encodeTypes(ds.AllowedTypes)
		if err != nil {
			return err
		}
		typesRaw = raw
	}
	_, err := s.db.Exec(
		`UPDATE device_settings SET
		   max_sync_size_inherit = ?, max_sync_size_bytes = ?,
		   allowed_types_inherit = ?, allowed_types = ?,
		   max_auto_upload_size_inherit = ?, max_auto_upload_size_bytes = ?,
		   max_auto_download_size_inherit = ?, max_auto_download_size_bytes = ?,
		   updated_at = ?
		 WHERE device_id = ?`,
		boolToInt(ds.MaxSyncSizeInherit), ds.MaxSyncSizeBytes,
		boolToInt(ds.AllowedTypesInherit), typesRaw,
		boolToInt(ds.MaxAutoUploadInherit), ds.MaxAutoUploadSizeBytes,
		boolToInt(ds.MaxAutoDownloadInherit), ds.MaxAutoDownloadSizeBytes,
		s.nowUnix(), ds.DeviceID,
	)
	return err
}

// boolToInt maps a Go bool to the SQLite 0/1 integer convention.
func boolToInt(b bool) int {
	if b {
		return 1
	}
	return 0
}

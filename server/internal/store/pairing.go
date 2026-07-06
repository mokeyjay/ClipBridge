package store

import (
	"database/sql"
	"errors"

	"github.com/mokeyjay/clipbridge/shared/protocol"
)

// CreatePairingCode cancels any existing active code for the user, then inserts a
// fresh active code valid for ttlSeconds. Enforces "at most one active per user".
func (s *Store) CreatePairingCode(userID, codeHash string, ttlSeconds int64) (*PairingCode, error) {
	now := s.nowUnix()
	pc := &PairingCode{ID: newID(), UserID: userID, CodeHash: codeHash, Status: protocol.PairingCodeActive, ExpiresAt: now + ttlSeconds, CreatedAt: now}

	tx, err := s.db.Begin()
	if err != nil {
		return nil, err
	}
	defer func() { _ = tx.Rollback() }()

	if _, err := tx.Exec(
		`UPDATE pairing_codes SET status = 'cancelled' WHERE user_id = ? AND status = 'active'`, userID,
	); err != nil {
		return nil, err
	}
	if _, err := tx.Exec(
		`INSERT INTO pairing_codes(id, user_id, code_hash, status, expires_at, created_at) VALUES (?,?,?,?,?,?)`,
		pc.ID, pc.UserID, pc.CodeHash, string(pc.Status), pc.ExpiresAt, pc.CreatedAt,
	); err != nil {
		return nil, err
	}
	if err := tx.Commit(); err != nil {
		return nil, err
	}
	return pc, nil
}

const pairingCodeColumns = `id, user_id, code_hash, status, expires_at, created_at`

// scanPairingCode reads a pairing-code row.
func scanPairingCode(scan func(dest ...any) error) (*PairingCode, error) {
	pc := &PairingCode{}
	var status string
	if err := scan(&pc.ID, &pc.UserID, &pc.CodeHash, &status, &pc.ExpiresAt, &pc.CreatedAt); err != nil {
		return nil, err
	}
	pc.Status = protocol.PairingCodeStatus(status)
	return pc, nil
}

// GetActivePairingCodeByUser returns the user's current active, non-expired code.
func (s *Store) GetActivePairingCodeByUser(userID string) (*PairingCode, error) {
	pc, err := scanPairingCode(s.db.QueryRow(
		`SELECT `+pairingCodeColumns+` FROM pairing_codes WHERE user_id = ? AND status = 'active' AND expires_at > ?`,
		userID, s.nowUnix(),
	).Scan)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	return pc, err
}

// GetActivePairingCodeByHash resolves a submitted code (hashed) to its active,
// non-expired pairing code. Used when a device submits a pairing request.
func (s *Store) GetActivePairingCodeByHash(codeHash string) (*PairingCode, error) {
	pc, err := scanPairingCode(s.db.QueryRow(
		`SELECT `+pairingCodeColumns+` FROM pairing_codes WHERE code_hash = ? AND status = 'active' AND expires_at > ?`,
		codeHash, s.nowUnix(),
	).Scan)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	return pc, err
}

// CancelActivePairingCode cancels the user's current active code, if any.
func (s *Store) CancelActivePairingCode(userID string) error {
	_, err := s.db.Exec(`UPDATE pairing_codes SET status = 'cancelled' WHERE user_id = ? AND status = 'active'`, userID)
	return err
}

// CreatePairingRequest records a device's pairing attempt against a code. The
// request expiry is clamped to not outlast the code.
func (s *Store) CreatePairingRequest(code *PairingCode, deviceName string, platform protocol.Platform, clientVersion, hpkePublicKey, pollTokenHash string) (*PairingRequest, error) {
	now := s.nowUnix()
	pr := &PairingRequest{
		ID: newID(), PairingCodeID: code.ID, DeviceName: deviceName, Platform: platform,
		ClientVersion: clientVersion, HPKEPublicKey: hpkePublicKey, PollTokenHash: pollTokenHash,
		Status: protocol.PairingRequestPending, ExpiresAt: code.ExpiresAt, CreatedAt: now, UpdatedAt: now,
	}
	_, err := s.db.Exec(
		`INSERT INTO pairing_requests(id, pairing_code_id, device_name, platform, client_version,
		        hpke_public_key, poll_token_hash, status, expires_at, created_at, updated_at)
		 VALUES (?,?,?,?,?,?,?,?,?,?,?)`,
		pr.ID, pr.PairingCodeID, pr.DeviceName, string(pr.Platform), pr.ClientVersion,
		pr.HPKEPublicKey, pr.PollTokenHash, string(pr.Status), pr.ExpiresAt, pr.CreatedAt, pr.UpdatedAt,
	)
	if err != nil {
		return nil, err
	}
	return pr, nil
}

const pairingRequestColumns = `id, pairing_code_id, device_name, platform, client_version, hpke_public_key, poll_token_hash, status, confirmed_device_id, expires_at, created_at, updated_at`

// scanPairingRequest reads a pairing-request row.
func scanPairingRequest(scan func(dest ...any) error) (*PairingRequest, error) {
	pr := &PairingRequest{}
	var platform, status string
	if err := scan(&pr.ID, &pr.PairingCodeID, &pr.DeviceName, &platform, &pr.ClientVersion,
		&pr.HPKEPublicKey, &pr.PollTokenHash, &status, &pr.ConfirmedDeviceID, &pr.ExpiresAt, &pr.CreatedAt, &pr.UpdatedAt); err != nil {
		return nil, err
	}
	pr.Platform = protocol.Platform(platform)
	pr.Status = protocol.PairingRequestStatus(status)
	return pr, nil
}

// GetPairingRequestByID looks up a pairing request.
func (s *Store) GetPairingRequestByID(id string) (*PairingRequest, error) {
	pr, err := scanPairingRequest(s.db.QueryRow(`SELECT `+pairingRequestColumns+` FROM pairing_requests WHERE id = ?`, id).Scan)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	return pr, err
}

// ListPendingPairingRequestsByUser returns the user's pending, non-expired
// requests across their codes, for the Web console to render confirm prompts.
func (s *Store) ListPendingPairingRequestsByUser(userID string) ([]*PairingRequest, error) {
	rows, err := s.db.Query(
		`SELECT `+prefixColumns("pr", pairingRequestColumns)+`
		 FROM pairing_requests pr
		 JOIN pairing_codes pc ON pc.id = pr.pairing_code_id
		 WHERE pc.user_id = ? AND pr.status = 'pending' AND pr.expires_at > ?
		 ORDER BY pr.created_at`, userID, s.nowUnix())
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []*PairingRequest
	for rows.Next() {
		pr, err := scanPairingRequest(rows.Scan)
		if err != nil {
			return nil, err
		}
		out = append(out, pr)
	}
	return out, rows.Err()
}

// ConfirmPairingRequest verifies the request belongs to userID and is still
// pending, creates the device (and its default settings), marks the request
// confirmed and consumes the code — all in one transaction. The device token is
// NOT issued here; it is minted on first claim. Returns the new device.
func (s *Store) ConfirmPairingRequest(userID, requestID string) (*Device, error) {
	now := s.nowUnix()
	tx, err := s.db.Begin()
	if err != nil {
		return nil, err
	}
	defer func() { _ = tx.Rollback() }()

	// Load request joined to its code so we can authorize against userID.
	pr := &PairingRequest{}
	var platform, status, codeUserID string
	err = tx.QueryRow(
		`SELECT pr.id, pr.pairing_code_id, pr.device_name, pr.platform, pr.client_version, pr.hpke_public_key,
		        pr.status, pr.expires_at, pc.user_id
		 FROM pairing_requests pr JOIN pairing_codes pc ON pc.id = pr.pairing_code_id
		 WHERE pr.id = ?`, requestID,
	).Scan(&pr.ID, &pr.PairingCodeID, &pr.DeviceName, &platform, &pr.ClientVersion, &pr.HPKEPublicKey, &status, &pr.ExpiresAt, &codeUserID)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	if codeUserID != userID {
		return nil, ErrNotFound // cross-user access is indistinguishable from "missing"
	}
	if status != string(protocol.PairingRequestPending) || pr.ExpiresAt <= now {
		return nil, ErrPairingNotPending
	}

	// Create the device and its fully-inheriting settings row.
	device := &Device{
		ID: newID(), UserID: userID, Name: pr.DeviceName, Platform: protocol.Platform(platform),
		ClientVersion: pr.ClientVersion, HPKEPublicKey: pr.HPKEPublicKey, Status: protocol.DeviceActive,
		CreatedAt: now, UpdatedAt: now,
	}
	if _, err := tx.Exec(
		`INSERT INTO devices(id, user_id, name, platform, client_version, hpke_public_key, status, created_at, updated_at)
		 VALUES (?,?,?,?,?,?,?,?,?)`,
		device.ID, device.UserID, device.Name, string(device.Platform), device.ClientVersion,
		device.HPKEPublicKey, string(device.Status), device.CreatedAt, device.UpdatedAt,
	); err != nil {
		return nil, err
	}
	if _, err := tx.Exec(`INSERT INTO device_settings(device_id, updated_at) VALUES (?, ?)`, device.ID, now); err != nil {
		return nil, err
	}
	if _, err := tx.Exec(
		`UPDATE pairing_requests SET status = 'confirmed', confirmed_device_id = ?, updated_at = ? WHERE id = ?`,
		device.ID, now, requestID,
	); err != nil {
		return nil, err
	}
	if _, err := tx.Exec(
		`UPDATE pairing_codes SET status = 'consumed' WHERE id = ?`, pr.PairingCodeID,
	); err != nil {
		return nil, err
	}
	if err := tx.Commit(); err != nil {
		return nil, err
	}
	return device, nil
}

// RejectPairingRequest verifies ownership and marks a pending request rejected.
func (s *Store) RejectPairingRequest(userID, requestID string) error {
	res, err := s.db.Exec(
		`UPDATE pairing_requests SET status = 'rejected', updated_at = ?
		 WHERE id = ? AND status = 'pending'
		   AND pairing_code_id IN (SELECT id FROM pairing_codes WHERE user_id = ?)`,
		s.nowUnix(), requestID, userID,
	)
	if err != nil {
		return err
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		return ErrPairingNotPending
	}
	return nil
}

// ClaimDeviceToken atomically transitions a confirmed request to claimed and
// stores the issued token hash against the confirmed device. It returns the
// confirmed device. A request that is not currently 'confirmed' yields
// ErrPairingNotConfirmed, so the one-time token is never re-issued.
func (s *Store) ClaimDeviceToken(requestID, tokenHash string) (*Device, error) {
	now := s.nowUnix()
	tx, err := s.db.Begin()
	if err != nil {
		return nil, err
	}
	defer func() { _ = tx.Rollback() }()

	var deviceID sql.NullString
	var status string
	err = tx.QueryRow(`SELECT status, confirmed_device_id FROM pairing_requests WHERE id = ?`, requestID).Scan(&status, &deviceID)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	if status != string(protocol.PairingRequestConfirmed) || !deviceID.Valid {
		return nil, ErrPairingNotConfirmed
	}
	if _, err := tx.Exec(
		`INSERT INTO device_tokens(id, device_id, token_hash, created_at) VALUES (?,?,?,?)`,
		newID(), deviceID.String, tokenHash, now,
	); err != nil {
		return nil, err
	}
	if _, err := tx.Exec(`UPDATE pairing_requests SET status = 'claimed', updated_at = ? WHERE id = ?`, now, requestID); err != nil {
		return nil, err
	}

	device, err := scanDevice(tx.QueryRow(`SELECT `+deviceColumns+` FROM devices WHERE id = ?`, deviceID.String).Scan)
	if err != nil {
		return nil, err
	}
	if err := tx.Commit(); err != nil {
		return nil, err
	}
	return device, nil
}

// ExpireStalePairingArtifacts marks past-due active codes and pending requests as
// expired. Called by the cleanup worker. Returns rows affected across both.
func (s *Store) ExpireStalePairingArtifacts() (int64, error) {
	now := s.nowUnix()
	r1, err := s.db.Exec(`UPDATE pairing_codes SET status = 'expired' WHERE status = 'active' AND expires_at <= ?`, now)
	if err != nil {
		return 0, err
	}
	r2, err := s.db.Exec(`UPDATE pairing_requests SET status = 'expired', updated_at = ? WHERE status IN ('pending','confirmed') AND expires_at <= ?`, now, now)
	if err != nil {
		return 0, err
	}
	n1, _ := r1.RowsAffected()
	n2, _ := r2.RowsAffected()
	return n1 + n2, nil
}

// prefixColumns rewrites a comma-separated column list to alias.column form so a
// shared column constant can be reused inside JOIN queries.
func prefixColumns(alias, columns string) string {
	var b []byte
	col := make([]byte, 0, 32)
	flush := func() {
		if len(col) == 0 {
			return
		}
		b = append(b, alias...)
		b = append(b, '.')
		b = append(b, col...)
		col = col[:0]
	}
	for i := 0; i < len(columns); i++ {
		c := columns[i]
		switch c {
		case ',':
			flush()
			b = append(b, ',')
		case ' ':
			// skip surrounding whitespace
		default:
			col = append(col, c)
		}
	}
	flush()
	return string(b)
}

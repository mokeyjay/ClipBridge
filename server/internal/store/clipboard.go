package store

import (
	"database/sql"
	"errors"

	"github.com/mokeyjay/clipbridge/shared/protocol"
)

// ClipboardItem is one uploaded ciphertext content's routing/state metadata. The
// body itself lives only as a temporary file at CiphertextPath.
type ClipboardItem struct {
	ID                  string
	UserID              string
	SourceDeviceID      string
	ContentType         protocol.ContentType
	CiphertextSizeBytes int64
	CiphertextPath      string
	CiphertextSHA256    string
	ChunkSizeBytes      int
	TotalChunks         int
	EncryptedMetadata   string
	Status              protocol.ItemStatus
	ExpiresAt           int64
	CreatedAt           int64
	CompletedAt         *int64
}

// Delivery is one per-target delivery of an item, carrying that device's wrapped
// DEK and its resolution state.
type Delivery struct {
	ID              string
	ClipboardItemID string
	TargetDeviceID  string
	WrappedDEK      string
	Status          protocol.DeliveryStatus
	RejectReason    *string
	CreatedAt       int64
	ResolvedAt      *int64
}

// NewDeliveryTarget pairs a target device with its wrapped DEK for item creation.
type NewDeliveryTarget struct {
	TargetDeviceID string
	WrappedDEK     string
}

// CreateItemWithDeliveries inserts a clipboard item and one delivery per target in
// a single transaction, returning the created deliveries (id + target) so the
// caller can send a per-target WSS notification. The ciphertext file must already
// be promoted on disk.
func (s *Store) CreateItemWithDeliveries(item *ClipboardItem, targets []NewDeliveryTarget) ([]Delivery, error) {
	if len(targets) == 0 {
		return nil, errors.New("store: 至少需要一个投递目标")
	}
	tx, err := s.db.Begin()
	if err != nil {
		return nil, err
	}
	defer func() { _ = tx.Rollback() }()

	if _, err := tx.Exec(
		`INSERT INTO clipboard_items(id, user_id, source_device_id, content_type, ciphertext_size_bytes,
		        ciphertext_path, ciphertext_sha256, chunk_size_bytes, total_chunks, encrypted_metadata, status, expires_at, created_at)
		 VALUES (?,?,?,?,?,?,?,?,?,?,?,?,?)`,
		item.ID, item.UserID, item.SourceDeviceID, string(item.ContentType), item.CiphertextSizeBytes,
		item.CiphertextPath, item.CiphertextSHA256, item.ChunkSizeBytes, item.TotalChunks, item.EncryptedMetadata,
		string(protocol.ItemActive), item.ExpiresAt, item.CreatedAt,
	); err != nil {
		return nil, err
	}
	created := make([]Delivery, 0, len(targets))
	for _, tgt := range targets {
		id := newID()
		if _, err := tx.Exec(
			`INSERT INTO clipboard_deliveries(id, clipboard_item_id, target_device_id, wrapped_dek, status, created_at)
			 VALUES (?,?,?,?,?,?)`,
			id, item.ID, tgt.TargetDeviceID, tgt.WrappedDEK, string(protocol.DeliveryPending), item.CreatedAt,
		); err != nil {
			return nil, err
		}
		created = append(created, Delivery{ID: id, ClipboardItemID: item.ID, TargetDeviceID: tgt.TargetDeviceID, Status: protocol.DeliveryPending, CreatedAt: item.CreatedAt})
	}
	if err := tx.Commit(); err != nil {
		return nil, err
	}
	return created, nil
}

// ItemExists reports whether an item id is already used (upload idempotency guard).
func (s *Store) ItemExists(id string) (bool, error) {
	var x int
	err := s.db.QueryRow(`SELECT 1 FROM clipboard_items WHERE id = ?`, id).Scan(&x)
	if errors.Is(err, sql.ErrNoRows) {
		return false, nil
	}
	return err == nil, err
}

// DeliveryDetail is a delivery joined with the item metadata its target needs to
// stream, verify and decrypt the content.
type DeliveryDetail struct {
	Delivery
	SourceDeviceID      string
	ContentType         protocol.ContentType
	CiphertextSizeBytes int64
	CiphertextSHA256    string
	CiphertextPath      string
	ChunkSizeBytes      int
	TotalChunks         int
	EncryptedMetadata   string
	ExpiresAt           int64
}

const deliveryDetailSelect = `
	SELECT d.id, d.clipboard_item_id, d.target_device_id, d.wrapped_dek, d.status, d.reject_reason,
	       d.created_at, d.resolved_at,
	       i.source_device_id, i.content_type, i.ciphertext_size_bytes, i.ciphertext_sha256,
	       i.ciphertext_path, i.chunk_size_bytes, i.total_chunks, i.encrypted_metadata, i.expires_at
	FROM clipboard_deliveries d JOIN clipboard_items i ON i.id = d.clipboard_item_id`

// scanDeliveryDetail reads a joined delivery+item row.
func scanDeliveryDetail(scan func(dest ...any) error) (*DeliveryDetail, error) {
	dd := &DeliveryDetail{}
	var status, contentType string
	if err := scan(&dd.ID, &dd.ClipboardItemID, &dd.TargetDeviceID, &dd.WrappedDEK, &status, &dd.RejectReason,
		&dd.CreatedAt, &dd.ResolvedAt,
		&dd.SourceDeviceID, &contentType, &dd.CiphertextSizeBytes, &dd.CiphertextSHA256,
		&dd.CiphertextPath, &dd.ChunkSizeBytes, &dd.TotalChunks, &dd.EncryptedMetadata, &dd.ExpiresAt); err != nil {
		return nil, err
	}
	dd.Status = protocol.DeliveryStatus(status)
	dd.ContentType = protocol.ContentType(contentType)
	return dd, nil
}

// GetDeliveryForDevice returns a delivery (with item metadata) only if it belongs
// to deviceID. The caller checks status/expiry.
func (s *Store) GetDeliveryForDevice(deliveryID, deviceID string) (*DeliveryDetail, error) {
	dd, err := scanDeliveryDetail(s.db.QueryRow(deliveryDetailSelect+` WHERE d.id = ? AND d.target_device_id = ?`, deliveryID, deviceID).Scan)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	return dd, err
}

// ListPendingDeliveries returns a device's unresolved, unexpired deliveries.
func (s *Store) ListPendingDeliveries(deviceID string) ([]*DeliveryDetail, error) {
	rows, err := s.db.Query(deliveryDetailSelect+
		` WHERE d.target_device_id = ? AND d.status = 'pending' AND i.expires_at > ? ORDER BY d.created_at`,
		deviceID, s.nowUnix())
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []*DeliveryDetail
	for rows.Next() {
		dd, err := scanDeliveryDetail(rows.Scan)
		if err != nil {
			return nil, err
		}
		out = append(out, dd)
	}
	return out, rows.Err()
}

// ResolveResult reports the outcome of resolving a delivery.
type ResolveResult struct {
	ItemCompleted  bool   // true when this was the last pending delivery
	CiphertextPath string // set when ItemCompleted, for file deletion
	ItemID         string
	SourceDeviceID string // set when ItemCompleted, to notify the source
}

// ResolveDelivery transitions a pending delivery to acked/rejected (with optional
// reason) for the owning device. When it was the item's last pending delivery the
// item is marked completed and the ciphertext path + source device are returned. A
// non-pending or foreign delivery yields ErrNotFound.
func (s *Store) ResolveDelivery(deliveryID, deviceID string, status protocol.DeliveryStatus, reason *string) (*ResolveResult, error) {
	now := s.nowUnix()
	tx, err := s.db.Begin()
	if err != nil {
		return nil, err
	}
	defer func() { _ = tx.Rollback() }()

	var itemID string
	err = tx.QueryRow(
		`SELECT clipboard_item_id FROM clipboard_deliveries WHERE id = ? AND target_device_id = ? AND status = 'pending'`,
		deliveryID, deviceID,
	).Scan(&itemID)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, err
	}

	if _, err := tx.Exec(
		`UPDATE clipboard_deliveries SET status = ?, reject_reason = ?, resolved_at = ? WHERE id = ?`,
		string(status), reason, now, deliveryID,
	); err != nil {
		return nil, err
	}

	var pending int
	if err := tx.QueryRow(
		`SELECT COUNT(*) FROM clipboard_deliveries WHERE clipboard_item_id = ? AND status = 'pending'`, itemID,
	).Scan(&pending); err != nil {
		return nil, err
	}

	res := &ResolveResult{ItemID: itemID}
	if pending == 0 {
		if err := tx.QueryRow(`SELECT ciphertext_path, source_device_id FROM clipboard_items WHERE id = ?`, itemID).Scan(&res.CiphertextPath, &res.SourceDeviceID); err != nil {
			return nil, err
		}
		if _, err := tx.Exec(
			`UPDATE clipboard_items SET status = ?, completed_at = ? WHERE id = ?`,
			string(protocol.ItemCompleted), now, itemID,
		); err != nil {
			return nil, err
		}
		res.ItemCompleted = true
	}
	if err := tx.Commit(); err != nil {
		return nil, err
	}
	return res, nil
}

// DueItem is an expired item that still has a ciphertext file to delete.
type DueItem struct {
	ID             string
	CiphertextPath string
}

// ExpireDueItems marks active items past their TTL as expired, expires their
// still-pending deliveries, and returns the items whose ciphertext files should
// be deleted. Idempotent; safe to call on every cleanup tick.
func (s *Store) ExpireDueItems() ([]DueItem, error) {
	now := s.nowUnix()
	rows, err := s.db.Query(`SELECT id, ciphertext_path FROM clipboard_items WHERE status = 'active' AND expires_at <= ?`, now)
	if err != nil {
		return nil, err
	}
	var due []DueItem
	for rows.Next() {
		var d DueItem
		if err := rows.Scan(&d.ID, &d.CiphertextPath); err != nil {
			rows.Close()
			return nil, err
		}
		due = append(due, d)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return nil, err
	}

	for _, d := range due {
		if _, err := s.db.Exec(
			`UPDATE clipboard_deliveries SET status = 'expired', resolved_at = ? WHERE clipboard_item_id = ? AND status = 'pending'`,
			now, d.ID,
		); err != nil {
			return nil, err
		}
		if _, err := s.db.Exec(
			`UPDATE clipboard_items SET status = 'expired', completed_at = ? WHERE id = ?`, now, d.ID,
		); err != nil {
			return nil, err
		}
	}
	return due, nil
}

// ActiveCiphertextPaths returns the ciphertext paths of all still-active items, so
// the cleanup worker can delete orphaned files not referenced by any active item.
func (s *Store) ActiveCiphertextPaths() (map[string]bool, error) {
	rows, err := s.db.Query(`SELECT ciphertext_path FROM clipboard_items WHERE status = 'active'`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	set := make(map[string]bool)
	for rows.Next() {
		var p string
		if err := rows.Scan(&p); err != nil {
			return nil, err
		}
		set[p] = true
	}
	return set, rows.Err()
}

// InsertSyncLog appends a minimal, body-free sync log entry for troubleshooting.
func (s *Store) InsertSyncLog(log *SyncLog) error {
	_, err := s.db.Exec(
		`INSERT INTO sync_logs(id, user_id, item_id, source_device_id, target_device_id, event_type,
		        content_type, ciphertext_size_bytes, result, error_code, created_at)
		 VALUES (?,?,?,?,?,?,?,?,?,?,?)`,
		newID(), log.UserID, log.ItemID, log.SourceDeviceID, log.TargetDeviceID, log.EventType,
		log.ContentType, log.CiphertextSizeBytes, log.Result, log.ErrorCode, s.nowUnix(),
	)
	return err
}

// SyncLog is one minimal sync-log row (no plaintext, names or secrets). ID and
// CreatedAt are populated on read (InsertSyncLog generates them).
type SyncLog struct {
	ID                  string
	UserID              string
	ItemID              *string
	SourceDeviceID      *string
	TargetDeviceID      *string
	EventType           string
	ContentType         *string
	CiphertextSizeBytes *int64
	Result              string
	ErrorCode           *string
	CreatedAt           int64
}

// SyncLogRow is a sync-log entry joined with the owning user's name and the
// source/target device names, for the admin diagnostics view.
type SyncLogRow struct {
	ID                  string
	EventType           string
	ContentType         *string
	CiphertextSizeBytes *int64
	Result              string
	ErrorCode           *string
	CreatedAt           int64
	Username            *string
	SourceDeviceName    *string
	TargetDeviceName    *string
}

// ListSyncLogsFiltered returns sync-log rows (newest first) with resolved
// user/device names, filtered by result (success|failure|"") and a free-text
// query q (matched against username, device names, event and content type),
// paginated by limit/offset. It also returns the total matching count.
func (s *Store) ListSyncLogsFiltered(result, q string, limit, offset int) ([]*SyncLogRow, int, error) {
	if limit <= 0 || limit > 200 {
		limit = 20
	}
	if offset < 0 {
		offset = 0
	}
	where := "1=1"
	var args []any
	if result == "success" || result == "failure" {
		where += " AND l.result = ?"
		args = append(args, result)
	}
	if q != "" {
		like := "%" + q + "%"
		where += " AND (u.username LIKE ? OR sd.name LIKE ? OR td.name LIKE ? OR l.event_type LIKE ? OR l.content_type LIKE ?)"
		args = append(args, like, like, like, like, like)
	}
	joins := `FROM sync_logs l
	          LEFT JOIN users u ON u.id = l.user_id
	          LEFT JOIN devices sd ON sd.id = l.source_device_id
	          LEFT JOIN devices td ON td.id = l.target_device_id`

	var total int
	if err := s.db.QueryRow("SELECT COUNT(*) "+joins+" WHERE "+where, args...).Scan(&total); err != nil {
		return nil, 0, err
	}

	rowsQuery := `SELECT l.id, l.event_type, l.content_type, l.ciphertext_size_bytes, l.result,
	                     l.error_code, l.created_at, u.username, sd.name, td.name ` + joins +
		" WHERE " + where + " ORDER BY l.created_at DESC, l.id DESC LIMIT ? OFFSET ?"
	rows, err := s.db.Query(rowsQuery, append(args, limit, offset)...)
	if err != nil {
		return nil, 0, err
	}
	defer rows.Close()
	var out []*SyncLogRow
	for rows.Next() {
		r := &SyncLogRow{}
		if err := rows.Scan(&r.ID, &r.EventType, &r.ContentType, &r.CiphertextSizeBytes, &r.Result,
			&r.ErrorCode, &r.CreatedAt, &r.Username, &r.SourceDeviceName, &r.TargetDeviceName); err != nil {
			return nil, 0, err
		}
		out = append(out, r)
	}
	return out, total, rows.Err()
}

package store

import (
	"path/filepath"
	"sync"
	"testing"
)

// TestOpenAppliesSchema verifies Open runs migrations and the expected tables exist.
func TestOpenAppliesSchema(t *testing.T) {
	db, err := Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })

	want := []string{
		"admins", "users", "web_sessions", "server_settings", "user_settings",
		"device_settings", "devices", "device_tokens", "pairing_codes",
		"pairing_requests", "clipboard_items", "clipboard_deliveries", "sync_logs",
		"audit_logs", "schema_migrations",
	}
	for _, table := range want {
		var name string
		err := db.QueryRow(
			`SELECT name FROM sqlite_master WHERE type='table' AND name=?`, table,
		).Scan(&name)
		if err != nil {
			t.Errorf("table %q missing: %v", table, err)
		}
	}
}

// TestMigrationsIdempotent verifies a second Open on the same file is a no-op.
func TestMigrationsIdempotent(t *testing.T) {
	path := filepath.Join(t.TempDir(), "test.db")

	db1, err := Open(path)
	if err != nil {
		t.Fatalf("first open: %v", err)
	}
	var first int
	if err := db1.QueryRow(`SELECT COUNT(*) FROM schema_migrations`).Scan(&first); err != nil {
		t.Fatalf("count migrations after first open: %v", err)
	}
	_ = db1.Close()

	db2, err := Open(path)
	if err != nil {
		t.Fatalf("second open: %v", err)
	}
	t.Cleanup(func() { _ = db2.Close() })

	var second int
	if err := db2.QueryRow(`SELECT COUNT(*) FROM schema_migrations`).Scan(&second); err != nil {
		t.Fatalf("count migrations: %v", err)
	}
	// Idempotency: reopening must not re-apply anything. (Count-agnostic so the
	// test survives squashing/adding migrations.)
	if first == 0 {
		t.Error("no migrations were applied on first open")
	}
	if second != first {
		t.Errorf("schema_migrations changed on reopen: first=%d second=%d (not idempotent)", first, second)
	}
}

// TestTransactionRollback verifies an uncommitted insert leaves no trace.
func TestTransactionRollback(t *testing.T) {
	db, err := Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })

	tx, err := db.Begin()
	if err != nil {
		t.Fatalf("begin: %v", err)
	}
	_, err = tx.Exec(
		`INSERT INTO users(id, username, password_hash, status, created_at, updated_at)
		 VALUES ('u1','alice','x','active',1,1)`,
	)
	if err != nil {
		t.Fatalf("insert: %v", err)
	}
	if err := tx.Rollback(); err != nil {
		t.Fatalf("rollback: %v", err)
	}

	var n int
	if err := db.QueryRow(`SELECT COUNT(*) FROM users`).Scan(&n); err != nil {
		t.Fatalf("count: %v", err)
	}
	if n != 0 {
		t.Errorf("after rollback users = %d, want 0", n)
	}
}

// TestSerialConcurrentWrites verifies many goroutines can write without
// SQLITE_BUSY errors under the single-connection + busy_timeout configuration.
func TestSerialConcurrentWrites(t *testing.T) {
	db, err := Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })

	const writers = 16
	const perWriter = 25

	var wg sync.WaitGroup
	errs := make(chan error, writers)
	for w := 0; w < writers; w++ {
		wg.Add(1)
		go func(w int) {
			defer wg.Done()
			for i := 0; i < perWriter; i++ {
				_, err := db.Exec(
					`INSERT INTO sync_logs(id, user_id, event_type, result, created_at)
					 VALUES (?, 'u1', 'upload_created', 'success', unixepoch())`,
					// deterministic, collision-free id from (writer, index)
					"log-"+itoa(w)+"-"+itoa(i),
				)
				if err != nil {
					errs <- err
					return
				}
			}
		}(w)
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		t.Fatalf("concurrent write failed: %v", err)
	}

	var n int
	if err := db.QueryRow(`SELECT COUNT(*) FROM sync_logs`).Scan(&n); err != nil {
		t.Fatalf("count: %v", err)
	}
	if n != writers*perWriter {
		t.Errorf("sync_logs = %d, want %d", n, writers*perWriter)
	}
}

// itoa is a tiny dependency-free int-to-string used to build unique ids.
func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	var b [20]byte
	i := len(b)
	for n > 0 {
		i--
		b[i] = byte('0' + n%10)
		n /= 10
	}
	return string(b[i:])
}

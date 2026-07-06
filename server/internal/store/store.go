// Package store owns the SQLite connection, schema lifecycle and typed data
// access for ClipBridge. All business queries live here behind the Store type so
// the HTTP and WSS layers never touch SQL directly. Clipboard bodies are never
// stored here — only routing/state metadata.
package store

import (
	"database/sql"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/mokeyjay/clipbridge/server/migrations"

	_ "modernc.org/sqlite" // registers the CGo-free "sqlite" driver
)

// ErrNotFound is returned by lookup methods when no row matches.
var ErrNotFound = errors.New("store: 记录不存在")

// ErrPairingNotPending is returned when confirming/rejecting a request that is no
// longer pending (already resolved or expired).
var ErrPairingNotPending = errors.New("store: 配对请求不是待确认状态")

// ErrPairingNotConfirmed is returned when claiming a token for a request that is
// not in the confirmed state, guarding the one-time token issuance.
var ErrPairingNotConfirmed = errors.New("store: 配对请求不是已确认状态")

// Store is the typed data-access facade over the SQLite database. It is safe for
// concurrent use; the underlying single connection serializes writes.
type Store struct {
	db  *sql.DB
	now func() time.Time // injectable clock; tests override for deterministic time
}

// New wraps an open *sql.DB in a Store using the wall clock.
func New(db *sql.DB) *Store {
	return &Store{db: db, now: time.Now}
}

// DB exposes the underlying handle for callers that need raw access (migrations,
// cleanup worker, tests).
func (s *Store) DB() *sql.DB { return s.db }

// Close closes the underlying database.
func (s *Store) Close() error { return s.db.Close() }

// nowUnix returns the current time as Unix seconds via the injected clock.
func (s *Store) nowUnix() int64 { return s.now().Unix() }

// Now returns the current time via the injected clock, for callers that need to
// stamp API responses with the same clock the store uses.
func (s *Store) Now() time.Time { return s.now() }

// newID returns a fresh lowercase UUID string for a business primary key.
func newID() string { return uuid.NewString() }

// Open opens (creating if needed) the SQLite database at path, applies pragmas
// suited to a single-process self-hosted instance, and runs all migrations.
//
// Pragmas:
//   - journal_mode(WAL): readers don't block the single writer.
//   - foreign_keys(ON): enforce the REFERENCES in the schema.
//   - busy_timeout(5000): wait out brief write contention instead of erroring.
func Open(path string) (*sql.DB, error) {
	dsn := fmt.Sprintf(
		"file:%s?_pragma=journal_mode(WAL)&_pragma=foreign_keys(ON)&_pragma=busy_timeout(5000)",
		path,
	)
	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, fmt.Errorf("store: open: %w", err)
	}
	// A single connection serializes writes and sidesteps SQLITE_BUSY under the
	// modest concurrency this tool targets. WAL still allows concurrent readers
	// via separate read connections if we later raise this.
	db.SetMaxOpenConns(1)

	if err := db.Ping(); err != nil {
		_ = db.Close()
		return nil, fmt.Errorf("store: ping: %w", err)
	}
	if err := migrations.Apply(db); err != nil {
		_ = db.Close()
		return nil, err
	}
	return db, nil
}

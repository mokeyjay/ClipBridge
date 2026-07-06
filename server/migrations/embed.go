// Package migrations embeds the ClipBridge SQL schema migrations and applies
// them in order. The runner is intentionally tiny and dependency-free: it tracks
// applied versions in a schema_migrations table and is safe to run on every boot.
package migrations

import (
	"database/sql"
	"embed"
	"fmt"
	"io/fs"
	"sort"
	"strconv"
	"strings"
)

//go:embed *.sql
var files embed.FS

// migration is one numbered SQL file.
type migration struct {
	version int
	name    string
	sql     string
}

// load reads and orders every embedded migration. File names must start with a
// zero-padded version followed by an underscore, e.g. 0001_init.sql.
func load() ([]migration, error) {
	entries, err := fs.ReadDir(files, ".")
	if err != nil {
		return nil, err
	}
	var out []migration
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".sql") {
			continue
		}
		prefix, _, ok := strings.Cut(e.Name(), "_")
		if !ok {
			return nil, fmt.Errorf("migrations: bad file name %q", e.Name())
		}
		v, err := strconv.Atoi(prefix)
		if err != nil {
			return nil, fmt.Errorf("migrations: bad version in %q: %w", e.Name(), err)
		}
		body, err := files.ReadFile(e.Name())
		if err != nil {
			return nil, err
		}
		out = append(out, migration{version: v, name: e.Name(), sql: string(body)})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].version < out[j].version })
	return out, nil
}

// Apply runs every migration newer than the recorded version inside a single
// transaction per migration. It is idempotent and safe to call on each startup.
func Apply(db *sql.DB) error {
	if _, err := db.Exec(`CREATE TABLE IF NOT EXISTS schema_migrations (
		version    INTEGER PRIMARY KEY,
		name       TEXT NOT NULL,
		applied_at INTEGER NOT NULL
	)`); err != nil {
		return fmt.Errorf("migrations: ensure table: %w", err)
	}

	var current int
	if err := db.QueryRow(`SELECT COALESCE(MAX(version), 0) FROM schema_migrations`).Scan(&current); err != nil {
		return fmt.Errorf("migrations: read current version: %w", err)
	}

	all, err := load()
	if err != nil {
		return err
	}
	for _, m := range all {
		if m.version <= current {
			continue
		}
		tx, err := db.Begin()
		if err != nil {
			return fmt.Errorf("migrations: begin %s: %w", m.name, err)
		}
		if _, err := tx.Exec(m.sql); err != nil {
			_ = tx.Rollback()
			return fmt.Errorf("migrations: exec %s: %w", m.name, err)
		}
		if _, err := tx.Exec(
			`INSERT INTO schema_migrations(version, name, applied_at) VALUES (?, ?, unixepoch())`,
			m.version, m.name,
		); err != nil {
			_ = tx.Rollback()
			return fmt.Errorf("migrations: record %s: %w", m.name, err)
		}
		if err := tx.Commit(); err != nil {
			return fmt.Errorf("migrations: commit %s: %w", m.name, err)
		}
	}
	return nil
}

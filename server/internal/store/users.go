package store

import (
	"database/sql"
	"errors"

	"github.com/mokeyjay/clipbridge/shared/protocol"
)

// CreateUser inserts a user and its default user_settings row in one transaction.
// The settings row uses the schema defaults so every user always has a template.
func (s *Store) CreateUser(username, passwordHash string) (*User, error) {
	now := s.nowUnix()
	u := &User{ID: newID(), Username: username, PasswordHash: passwordHash, Status: protocol.UserActive, CreatedAt: now, UpdatedAt: now}

	tx, err := s.db.Begin()
	if err != nil {
		return nil, err
	}
	defer func() { _ = tx.Rollback() }()

	if _, err := tx.Exec(
		`INSERT INTO users(id, username, password_hash, status, created_at, updated_at) VALUES (?,?,?,?,?,?)`,
		u.ID, u.Username, u.PasswordHash, string(u.Status), u.CreatedAt, u.UpdatedAt,
	); err != nil {
		return nil, err
	}
	// Default sync-policy template; column DEFAULTs supply the values.
	if _, err := tx.Exec(`INSERT INTO user_settings(user_id, updated_at) VALUES (?, ?)`, u.ID, now); err != nil {
		return nil, err
	}
	if err := tx.Commit(); err != nil {
		return nil, err
	}
	return u, nil
}

// scanUser reads a user row from a *sql.Row / *sql.Rows-compatible scanner.
func scanUser(scan func(dest ...any) error) (*User, error) {
	u := &User{}
	var status string
	if err := scan(&u.ID, &u.Username, &u.PasswordHash, &status, &u.CreatedAt, &u.UpdatedAt); err != nil {
		return nil, err
	}
	u.Status = protocol.UserStatus(status)
	return u, nil
}

const userColumns = `id, username, password_hash, status, created_at, updated_at`

// GetUserByID looks up a user by id.
func (s *Store) GetUserByID(id string) (*User, error) {
	u, err := scanUser(s.db.QueryRow(`SELECT `+userColumns+` FROM users WHERE id = ?`, id).Scan)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	return u, err
}

// GetUserByUsername looks up a user by username for login.
func (s *Store) GetUserByUsername(username string) (*User, error) {
	u, err := scanUser(s.db.QueryRow(`SELECT `+userColumns+` FROM users WHERE username = ?`, username).Scan)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	return u, err
}

// ListUsers returns all users ordered by creation time.
func (s *Store) ListUsers() ([]*User, error) {
	rows, err := s.db.Query(`SELECT ` + userColumns + ` FROM users ORDER BY created_at`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []*User
	for rows.Next() {
		u, err := scanUser(rows.Scan)
		if err != nil {
			return nil, err
		}
		out = append(out, u)
	}
	return out, rows.Err()
}

// CountUsers returns the number of registered users.
func (s *Store) CountUsers() (int, error) {
	var n int
	err := s.db.QueryRow(`SELECT COUNT(*) FROM users`).Scan(&n)
	return n, err
}

// UpdateUsername renames a user.
func (s *Store) UpdateUsername(id, username string) error {
	_, err := s.db.Exec(`UPDATE users SET username = ?, updated_at = ? WHERE id = ?`, username, s.nowUnix(), id)
	return err
}

// SetUserStatus enables or disables a user.
func (s *Store) SetUserStatus(id string, status protocol.UserStatus) error {
	_, err := s.db.Exec(`UPDATE users SET status = ?, updated_at = ? WHERE id = ?`, string(status), s.nowUnix(), id)
	return err
}

// UpdateUserPassword sets a new password hash for a user.
func (s *Store) UpdateUserPassword(id, passwordHash string) error {
	_, err := s.db.Exec(`UPDATE users SET password_hash = ?, updated_at = ? WHERE id = ?`, passwordHash, s.nowUnix(), id)
	return err
}

package store

import (
	"database/sql"
	"errors"
)

// CreateAdmin inserts the single administrator account.
func (s *Store) CreateAdmin(username, passwordHash string) (*Admin, error) {
	now := s.nowUnix()
	a := &Admin{ID: newID(), Username: username, PasswordHash: passwordHash, CreatedAt: now, UpdatedAt: now}
	_, err := s.db.Exec(
		`INSERT INTO admins(id, username, password_hash, created_at, updated_at) VALUES (?,?,?,?,?)`,
		a.ID, a.Username, a.PasswordHash, a.CreatedAt, a.UpdatedAt,
	)
	if err != nil {
		return nil, err
	}
	return a, nil
}

// CountAdmins returns the number of administrator rows (used by bootstrap).
func (s *Store) CountAdmins() (int, error) {
	var n int
	err := s.db.QueryRow(`SELECT COUNT(*) FROM admins`).Scan(&n)
	return n, err
}

// GetAdmin returns the single admin, or ErrNotFound if none exists.
func (s *Store) GetAdmin() (*Admin, error) {
	a := &Admin{}
	err := s.db.QueryRow(
		`SELECT id, username, password_hash, created_at, updated_at FROM admins LIMIT 1`,
	).Scan(&a.ID, &a.Username, &a.PasswordHash, &a.CreatedAt, &a.UpdatedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	return a, nil
}

// GetAdminByUsername looks up the admin by username for login.
func (s *Store) GetAdminByUsername(username string) (*Admin, error) {
	a := &Admin{}
	err := s.db.QueryRow(
		`SELECT id, username, password_hash, created_at, updated_at FROM admins WHERE username = ?`, username,
	).Scan(&a.ID, &a.Username, &a.PasswordHash, &a.CreatedAt, &a.UpdatedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	return a, nil
}

// UpdateAdminUsername renames the administrator.
func (s *Store) UpdateAdminUsername(id, username string) error {
	_, err := s.db.Exec(`UPDATE admins SET username = ?, updated_at = ? WHERE id = ?`, username, s.nowUnix(), id)
	return err
}

// UpdateAdminPassword sets a new password hash for the administrator.
func (s *Store) UpdateAdminPassword(id, passwordHash string) error {
	_, err := s.db.Exec(`UPDATE admins SET password_hash = ?, updated_at = ? WHERE id = ?`, passwordHash, s.nowUnix(), id)
	return err
}

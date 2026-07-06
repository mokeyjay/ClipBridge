package store

import (
	"database/sql"
	"errors"

	"github.com/mokeyjay/clipbridge/shared/protocol"
)

// CreateWebSession stores a session keyed by the irreversible hash of the cookie
// token. ttlSeconds sets the lifetime from now.
func (s *Store) CreateWebSession(subjectType protocol.SubjectType, subjectID, tokenHash string, ttlSeconds int64) (*WebSession, error) {
	now := s.nowUnix()
	sess := &WebSession{
		ID:          newID(),
		SubjectType: subjectType,
		SubjectID:   subjectID,
		TokenHash:   tokenHash,
		ExpiresAt:   now + ttlSeconds,
		CreatedAt:   now,
		LastSeenAt:  now,
	}
	_, err := s.db.Exec(
		`INSERT INTO web_sessions(id, subject_type, subject_id, token_hash, expires_at, created_at, last_seen_at)
		 VALUES (?,?,?,?,?,?,?)`,
		sess.ID, string(sess.SubjectType), sess.SubjectID, sess.TokenHash, sess.ExpiresAt, sess.CreatedAt, sess.LastSeenAt,
	)
	if err != nil {
		return nil, err
	}
	return sess, nil
}

// GetWebSessionByTokenHash returns a non-expired session for tokenHash and
// touches its last_seen_at. Expired or missing sessions return ErrNotFound.
func (s *Store) GetWebSessionByTokenHash(tokenHash string) (*WebSession, error) {
	sess := &WebSession{}
	var subjectType string
	err := s.db.QueryRow(
		`SELECT id, subject_type, subject_id, token_hash, expires_at, created_at, last_seen_at
		 FROM web_sessions WHERE token_hash = ?`, tokenHash,
	).Scan(&sess.ID, &subjectType, &sess.SubjectID, &sess.TokenHash, &sess.ExpiresAt, &sess.CreatedAt, &sess.LastSeenAt)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	if sess.ExpiresAt <= s.nowUnix() {
		return nil, ErrNotFound
	}
	sess.SubjectType = protocol.SubjectType(subjectType)
	return sess, nil
}

// TouchWebSession updates last_seen_at to now for the given session id.
func (s *Store) TouchWebSession(id string) error {
	_, err := s.db.Exec(`UPDATE web_sessions SET last_seen_at = ? WHERE id = ?`, s.nowUnix(), id)
	return err
}

// DeleteWebSessionByTokenHash removes a session at logout.
func (s *Store) DeleteWebSessionByTokenHash(tokenHash string) error {
	_, err := s.db.Exec(`DELETE FROM web_sessions WHERE token_hash = ?`, tokenHash)
	return err
}

// DeleteSessionsForSubject removes every session of a principal (e.g. on disable
// or password change), forcing re-login.
func (s *Store) DeleteSessionsForSubject(subjectType protocol.SubjectType, subjectID string) error {
	_, err := s.db.Exec(`DELETE FROM web_sessions WHERE subject_type = ? AND subject_id = ?`, string(subjectType), subjectID)
	return err
}

// DeleteExpiredWebSessions purges expired sessions (called by the cleanup worker).
func (s *Store) DeleteExpiredWebSessions() (int64, error) {
	res, err := s.db.Exec(`DELETE FROM web_sessions WHERE expires_at <= ?`, s.nowUnix())
	if err != nil {
		return 0, err
	}
	return res.RowsAffected()
}

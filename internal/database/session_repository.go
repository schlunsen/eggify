// Package database provides data access methods for session management.
// This repository handles all operations related to user sessions,
// including creation, retrieval, deletion, and cleanup of expired sessions.
package database

import (
	"database/sql"
	"time"
)

// SessionRepository provides data access methods for session operations
type SessionRepository struct {
	db *Database
}

// NewSessionRepository creates a new session repository instance
func NewSessionRepository(db *Database) *SessionRepository {
	return &SessionRepository{db: db}
}

// ============================================
// Session Management Operations
// ============================================

const sessionColumns = `token, username, expires_at, created_at, session_id, device_name, device_model, created_via, last_used_at, minted_by_session`

func scanSession(scan func(dest ...any) error) (*DBSession, error) {
	s := &DBSession{}
	var lastUsed sql.NullTime
	if err := scan(&s.Token, &s.Username, &s.ExpiresAt, &s.CreatedAt, &s.SessionID,
		&s.DeviceName, &s.DeviceModel, &s.CreatedVia, &lastUsed, &s.MintedBySession); err != nil {
		return nil, err
	}
	if lastUsed.Valid {
		t := lastUsed.Time
		s.LastUsedAt = &t
	}
	return s, nil
}

// CreateSession creates a new session in the database
func (r *SessionRepository) CreateSession(session *DBSession) error {
	createdVia := session.CreatedVia
	if createdVia == "" {
		createdVia = "password"
	}
	query := `
		INSERT INTO user_sessions (token, username, expires_at, created_at, session_id, device_name, device_model, created_via, last_used_at, minted_by_session)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
	`
	_, err := r.db.db.Exec(query, session.Token, session.Username, session.ExpiresAt, session.CreatedAt,
		session.SessionID, session.DeviceName, session.DeviceModel, createdVia, session.LastUsedAt, session.MintedBySession)
	return err
}

// GetSession retrieves a session by token
func (r *SessionRepository) GetSession(token string) (*DBSession, error) {
	query := `SELECT ` + sessionColumns + ` FROM user_sessions WHERE token = ?`
	session, err := scanSession(r.db.db.QueryRow(query, token).Scan)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return session, nil
}

// ListValidSessions retrieves all non-expired sessions from the database
func (r *SessionRepository) ListValidSessions() ([]*DBSession, error) {
	query := `SELECT ` + sessionColumns + ` FROM user_sessions WHERE expires_at > datetime('now') ORDER BY created_at DESC`

	rows, err := r.db.db.Query(query)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var sessions []*DBSession
	for rows.Next() {
		session, err := scanSession(rows.Scan)
		if err != nil {
			return nil, err
		}
		sessions = append(sessions, session)
	}

	if err := rows.Err(); err != nil {
		return nil, err
	}

	return sessions, nil
}

// UpdateSessionDevice records the device a session belongs to.
func (r *SessionRepository) UpdateSessionDevice(token, deviceName, deviceModel string) error {
	_, err := r.db.db.Exec(`UPDATE user_sessions SET device_name = ?, device_model = ? WHERE token = ?`,
		deviceName, deviceModel, token)
	return err
}

// TouchSession records when a session was last used.
func (r *SessionRepository) TouchSession(token string, at time.Time) error {
	_, err := r.db.db.Exec(`UPDATE user_sessions SET last_used_at = ? WHERE token = ?`, at, token)
	return err
}

// DeleteSession removes a session from the database
func (r *SessionRepository) DeleteSession(token string) error {
	query := `DELETE FROM user_sessions WHERE token = ?`
	_, err := r.db.db.Exec(query, token)
	return err
}

// CleanupExpiredSessions removes all expired sessions
func (r *SessionRepository) CleanupExpiredSessions() error {
	query := `DELETE FROM user_sessions WHERE expires_at < datetime('now')`
	_, err := r.db.db.Exec(query)
	return err
}

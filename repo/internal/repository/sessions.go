package repository

import (
	"database/sql"
	"time"

	"w2t86/internal/models"
)

// SessionRepository provides database operations for the sessions table.
type SessionRepository struct {
	db *sql.DB
}

// NewSessionRepository returns a SessionRepository backed by the given database.
func NewSessionRepository(db *sql.DB) *SessionRepository {
	return &SessionRepository{db: db}
}

// Create inserts a new session and returns the populated model.
func (r *SessionRepository) Create(userID int64, tokenHash string, expiresAt time.Time) (*models.Session, error) {
	const q = `
		INSERT INTO sessions (user_id, token_hash, expires_at)
		VALUES (?, ?, ?)
		RETURNING id, user_id, token_hash, expires_at, created_at`

	row := r.db.QueryRow(q, userID, tokenHash, expiresAt.UTC().Format(time.RFC3339))
	s := &models.Session{}
	err := row.Scan(&s.ID, &s.UserID, &s.TokenHash, &s.ExpiresAt, &s.CreatedAt)
	if err != nil {
		return nil, err
	}
	return s, nil
}

// GetByTokenHash returns the session matching the given token hash.
func (r *SessionRepository) GetByTokenHash(tokenHash string) (*models.Session, error) {
	const q = `
		SELECT id, user_id, token_hash, expires_at, created_at
		FROM   sessions
		WHERE  token_hash = ?`

	row := r.db.QueryRow(q, tokenHash)
	s := &models.Session{}
	err := row.Scan(&s.ID, &s.UserID, &s.TokenHash, &s.ExpiresAt, &s.CreatedAt)
	if err != nil {
		return nil, err
	}
	return s, nil
}

// Delete removes the session with the given token hash.
func (r *SessionRepository) Delete(tokenHash string) error {
	const q = `DELETE FROM sessions WHERE token_hash = ?`
	_, err := r.db.Exec(q, tokenHash)
	return err
}

// DeleteExpired removes all sessions whose expires_at is in the past.
func (r *SessionRepository) DeleteExpired() error {
	const q = `DELETE FROM sessions WHERE expires_at < datetime('now')`
	_, err := r.db.Exec(q)
	return err
}

// DeleteByUserID removes all sessions belonging to the given user.
func (r *SessionRepository) DeleteByUserID(userID int64) error {
	const q = `DELETE FROM sessions WHERE user_id = ?`
	_, err := r.db.Exec(q, userID)
	return err
}

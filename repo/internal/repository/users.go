package repository

import (
	"database/sql"
	"fmt"
	"strings"
	"time"

	"w2t86/internal/models"
)

// UserRepository provides database operations for the users table.
type UserRepository struct {
	db *sql.DB
}

// NewUserRepository returns a UserRepository backed by the given database.
func NewUserRepository(db *sql.DB) *UserRepository {
	return &UserRepository{db: db}
}

// userCols is the canonical ordered column list returned by every user SELECT.
// All scan helpers must match this exact order.
const userCols = `id, username, email, password_hash, role,
	       failed_attempts, locked_until, date_of_birth, full_name, external_id,
	       created_at, updated_at, deleted_at`

// Create inserts a new user and returns the populated model.
func (r *UserRepository) Create(username, email, passwordHash, role string) (*models.User, error) {
	q := `INSERT INTO users (username, email, password_hash, role)
		VALUES (?, ?, ?, ?)
		RETURNING ` + userCols

	row := r.db.QueryRow(q, username, email, passwordHash, role)
	return scanUser(row)
}

// GetByID returns the user with the given id, or an error if not found.
func (r *UserRepository) GetByID(id int64) (*models.User, error) {
	q := `SELECT ` + userCols + ` FROM users WHERE id = ? AND deleted_at IS NULL`
	row := r.db.QueryRow(q, id)
	return scanUser(row)
}

// GetByUsername returns the user with the given username, or an error if not found.
func (r *UserRepository) GetByUsername(username string) (*models.User, error) {
	q := `SELECT ` + userCols + ` FROM users WHERE username = ? AND deleted_at IS NULL`
	row := r.db.QueryRow(q, username)
	return scanUser(row)
}

// IncrementFailedAttempts adds one to failed_attempts for the given user.
func (r *UserRepository) IncrementFailedAttempts(id int64) error {
	const q = `UPDATE users SET failed_attempts = failed_attempts + 1, updated_at = datetime('now') WHERE id = ?`
	_, err := r.db.Exec(q, id)
	return err
}

// LockUntil sets locked_until to the given time for the given user.
func (r *UserRepository) LockUntil(id int64, until time.Time) error {
	const q = `UPDATE users SET locked_until = ?, updated_at = datetime('now') WHERE id = ?`
	_, err := r.db.Exec(q, until.UTC().Format(time.RFC3339), id)
	return err
}

// ResetFailedAttempts zeroes failed_attempts and clears locked_until.
func (r *UserRepository) ResetFailedAttempts(id int64) error {
	const q = `UPDATE users SET failed_attempts = 0, locked_until = NULL, updated_at = datetime('now') WHERE id = ?`
	_, err := r.db.Exec(q, id)
	return err
}

// List returns up to limit users starting at offset, excluding soft-deleted rows.
func (r *UserRepository) List(limit, offset int) ([]models.User, error) {
	q := `SELECT ` + userCols + `
		FROM   users
		WHERE  deleted_at IS NULL
		ORDER  BY id
		LIMIT  ? OFFSET ?`

	rows, err := r.db.Query(q, limit, offset)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var users []models.User
	for rows.Next() {
		u, err := scanUserRow(rows)
		if err != nil {
			return nil, err
		}
		users = append(users, *u)
	}
	return users, rows.Err()
}

// Update applies the given field map to the user identified by id.
// Only columns present in the map are changed; updated_at is always refreshed.
// Allowed keys: username, email, password_hash, role.
func (r *UserRepository) Update(id int64, fields map[string]interface{}) error {
	if len(fields) == 0 {
		return nil
	}

	allowed := map[string]bool{
		"username":      true,
		"email":         true,
		"password_hash": true,
		"role":          true,
	}

	setClauses := make([]string, 0, len(fields)+1)
	args := make([]interface{}, 0, len(fields)+2)

	for col, val := range fields {
		if !allowed[col] {
			return fmt.Errorf("repository: Update: unknown or disallowed column %q", col)
		}
		setClauses = append(setClauses, col+" = ?")
		args = append(args, val)
	}
	setClauses = append(setClauses, "updated_at = datetime('now')")
	args = append(args, id)

	q := "UPDATE users SET " + strings.Join(setClauses, ", ") + " WHERE id = ? AND deleted_at IS NULL"
	_, err := r.db.Exec(q, args...)
	return err
}

// SetFullName sets the full_name field on a user record.
// This is the primary name signal used for fuzzy duplicate-user detection.
func (r *UserRepository) SetFullName(userID int64, fullName string) error {
	const q = `UPDATE users SET full_name = ?, updated_at = datetime('now') WHERE id = ? AND deleted_at IS NULL`
	_, err := r.db.Exec(q, fullName, userID)
	return err
}

// SetExternalID assigns the institution-issued external identifier (e.g. student
// number, employee ID) to a user record.  external_id is the dedicated "exact ID"
// signal in duplicate-user detection: two accounts sharing the same non-null
// external_id are definitively the same person (score = 1.0).
func (r *UserRepository) SetExternalID(userID int64, externalID string) error {
	const q = `UPDATE users SET external_id = ?, updated_at = datetime('now') WHERE id = ? AND deleted_at IS NULL`
	_, err := r.db.Exec(q, externalID, userID)
	return err
}

// SoftDelete sets deleted_at on the user row.
func (r *UserRepository) SoftDelete(id int64) error {
	const q = `UPDATE users SET deleted_at = datetime('now'), updated_at = datetime('now') WHERE id = ? AND deleted_at IS NULL`
	_, err := r.db.Exec(q, id)
	return err
}

// ---------------------------------------------------------------
// helpers
// ---------------------------------------------------------------

type scanner interface {
	Scan(dest ...interface{}) error
}

// scanUser scans a single-row result into a User.
// Column order must match userCols exactly.
func scanUser(s scanner) (*models.User, error) {
	u := &models.User{}
	err := s.Scan(
		&u.ID, &u.Username, &u.Email, &u.PasswordHash, &u.Role,
		&u.FailedAttempts, &u.LockedUntil, &u.DateOfBirth, &u.FullName, &u.ExternalID,
		&u.CreatedAt, &u.UpdatedAt, &u.DeletedAt,
	)
	if err != nil {
		return nil, err
	}
	return u, nil
}

// scanUserRow scans the current row from a *sql.Rows result set into a User.
// Column order must match userCols exactly.
func scanUserRow(rows *sql.Rows) (*models.User, error) {
	u := &models.User{}
	err := rows.Scan(
		&u.ID, &u.Username, &u.Email, &u.PasswordHash, &u.Role,
		&u.FailedAttempts, &u.LockedUntil, &u.DateOfBirth, &u.FullName, &u.ExternalID,
		&u.CreatedAt, &u.UpdatedAt, &u.DeletedAt,
	)
	if err != nil {
		return nil, err
	}
	return u, nil
}

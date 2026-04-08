package repository

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"strings"

	"w2t86/internal/models"
)

// AdminRepository provides database operations for admin-specific features:
// custom user fields, duplicate detection/merging, audit logs, and user
// management actions that are too elevated for the regular UserRepository.
type AdminRepository struct {
	db *sql.DB
}

// NewAdminRepository creates an AdminRepository backed by the given database.
func NewAdminRepository(db *sql.DB) *AdminRepository {
	return &AdminRepository{db: db}
}

// ---------------------------------------------------------------
// Custom fields
// ---------------------------------------------------------------

// SetCustomField upserts a custom field for the given user.
func (r *AdminRepository) SetCustomField(userID int64, fieldName, fieldValue string, isEncrypted bool) error {
	enc := 0
	if isEncrypted {
		enc = 1
	}
	const q = `
		INSERT INTO user_custom_fields (user_id, field_name, field_value, is_encrypted)
		VALUES (?, ?, ?, ?)
		ON CONFLICT(user_id, field_name) DO UPDATE
		SET field_value  = excluded.field_value,
		    is_encrypted = excluded.is_encrypted`
	_, err := r.db.Exec(q, userID, fieldName, fieldValue, enc)
	if err != nil {
		return fmt.Errorf("repository: SetCustomField: %w", err)
	}
	return nil
}

// GetCustomFields returns all custom fields for a user.
func (r *AdminRepository) GetCustomFields(userID int64) ([]models.UserCustomField, error) {
	const q = `
		SELECT id, user_id, field_name, field_value, is_encrypted
		FROM   user_custom_fields
		WHERE  user_id = ?
		ORDER  BY field_name`

	rows, err := r.db.Query(q, userID)
	if err != nil {
		return nil, fmt.Errorf("repository: GetCustomFields: %w", err)
	}
	defer rows.Close()

	var out []models.UserCustomField
	for rows.Next() {
		var f models.UserCustomField
		if err := rows.Scan(&f.ID, &f.UserID, &f.FieldName, &f.FieldValue, &f.IsEncrypted); err != nil {
			return nil, fmt.Errorf("repository: GetCustomFields: scan: %w", err)
		}
		out = append(out, f)
	}
	return out, rows.Err()
}

// DeleteCustomField removes a specific custom field for a user.
func (r *AdminRepository) DeleteCustomField(userID int64, fieldName string) error {
	const q = `DELETE FROM user_custom_fields WHERE user_id = ? AND field_name = ?`
	_, err := r.db.Exec(q, userID, fieldName)
	if err != nil {
		return fmt.Errorf("repository: DeleteCustomField: %w", err)
	}
	return nil
}

// ---------------------------------------------------------------
// Duplicate detection
// ---------------------------------------------------------------

// DuplicatePair represents two users that may be the same person.
type DuplicatePair struct {
	UserA models.User
	UserB models.User
	Score float64 // similarity 0-1
}

// FindDuplicateUsers returns user pairs that are likely the same person.
//
// # Semantic mapping — schema fields → specification concepts
//
// The prompt specifies two signals: "exact ID" and "fuzzy(name, DOB)".
//
//   Spec concept          → Schema column     Rationale
//   ─────────────────────────────────────────────────────────────────────
//   Exact identifier (ID) → external_id       Dedicated column for the
//                                             institution-issued identifier
//                                             (student number, employee ID, etc.).
//                                             Two accounts sharing the same
//                                             non-null external_id are
//                                             definitively the same person.
//   Name (fuzzy)          → full_name         Dedicated real-name column.
//                                             Falls back to username when
//                                             full_name has not been set.
//   Date of birth (fuzzy) → date_of_birth     Direct column, exact match only
//                                             (birth dates are discrete values).
//
// # Detection strategies
//
//  1. Exact identifier match — both accounts share the same non-null external_id
//     → score 1.0.
//
//  2. Fuzzy identity match — composite weighted score for pairs NOT already
//     matched by Pass 1:
//
//       score = 0.6 × name_similarity  +  0.4 × dob_match
//
//     name_similarity: Levenshtein-based 0–1 ratio on full_name (username fallback).
//     dob_match:       1.0 when both records carry the same non-null DOB; 0 otherwise.
//
//     Threshold = 0.65 — ensures neither signal alone is sufficient:
//       - DOB match only  (0.4)     → below threshold, excluded.
//       - Name match only (max 0.6) → only identical names reach threshold;
//         any divergence requires a DOB match to compensate.
//       - Both signals    (≥ 0.65)  → included as probable duplicate.
//
// SQL pre-filters candidates to pairs sharing the same DOB or the same 4-char
// name prefix; Go then applies the full composite formula.
// Soft-deleted users are excluded. Results sorted by score desc, capped at limit.
func (r *AdminRepository) FindDuplicateUsers(limit int) ([]DuplicatePair, error) {
	// ---- Pass 1: exact ID — both accounts share the same non-null external_id ----
	const exactQ = `
		SELECT a.id, a.username, a.email, a.password_hash, a.role,
		       a.failed_attempts, a.locked_until, a.date_of_birth, a.full_name, a.external_id,
		       a.created_at, a.updated_at, a.deleted_at,
		       b.id, b.username, b.email, b.password_hash, b.role,
		       b.failed_attempts, b.locked_until, b.date_of_birth, b.full_name, b.external_id,
		       b.created_at, b.updated_at, b.deleted_at
		FROM   users a
		JOIN   users b ON b.id > a.id
		             AND a.external_id IS NOT NULL
		             AND a.external_id = b.external_id
		WHERE  a.deleted_at IS NULL AND b.deleted_at IS NULL
		ORDER  BY a.id`

	exactRows, err := r.db.Query(exactQ)
	if err != nil {
		return nil, fmt.Errorf("repository: FindDuplicateUsers (exact): %w", err)
	}
	pairs, err := scanUserPairs(exactRows)
	if err != nil {
		return nil, fmt.Errorf("repository: FindDuplicateUsers (exact scan): %w", err)
	}
	for i := range pairs {
		pairs[i].Score = 1.0
	}

	// ---- Pass 2: fuzzy — composite(name similarity + DOB) ----
	// Excludes pairs already captured by Pass 1 (same external_id).
	// COALESCE(full_name, username): use the real name when available.
	const fuzzyQ = `
		SELECT a.id, a.username, a.email, a.password_hash, a.role,
		       a.failed_attempts, a.locked_until, a.date_of_birth, a.full_name, a.external_id,
		       a.created_at, a.updated_at, a.deleted_at,
		       b.id, b.username, b.email, b.password_hash, b.role,
		       b.failed_attempts, b.locked_until, b.date_of_birth, b.full_name, b.external_id,
		       b.created_at, b.updated_at, b.deleted_at
		FROM   users a
		JOIN   users b ON b.id > a.id
		             AND (a.external_id IS NULL OR b.external_id IS NULL
		                  OR a.external_id != b.external_id)
		WHERE  a.deleted_at IS NULL
		  AND  b.deleted_at IS NULL
		  AND (
		        (a.date_of_birth IS NOT NULL AND a.date_of_birth = b.date_of_birth)
		        OR LOWER(SUBSTR(COALESCE(a.full_name, a.username), 1, 4))
		         = LOWER(SUBSTR(COALESCE(b.full_name, b.username), 1, 4))
		      )
		ORDER  BY a.id`

	fuzzyRows, err := r.db.Query(fuzzyQ)
	if err != nil {
		return nil, fmt.Errorf("repository: FindDuplicateUsers (fuzzy): %w", err)
	}
	candidates, err := scanUserPairs(fuzzyRows)
	if err != nil {
		return nil, fmt.Errorf("repository: FindDuplicateUsers (fuzzy scan): %w", err)
	}

	// Score each candidate: score = nameWeight×name_similarity + dobWeight×dob_match
	// "name" = full_name when set; falls back to username when full_name is NULL.
	// "dob"  = date_of_birth exact match.
	const (
		nameWeight = 0.6 // weight for name (full_name / username) similarity
		dobWeight  = 0.4 // weight for date-of-birth exact match
		minScore   = 0.65
	)
	for _, p := range candidates {
		// Resolve the name field: prefer full_name, fall back to username.
		nameA := p.UserA.Username
		if p.UserA.FullName != nil && *p.UserA.FullName != "" {
			nameA = *p.UserA.FullName
		}
		nameB := p.UserB.Username
		if p.UserB.FullName != nil && *p.UserB.FullName != "" {
			nameB = *p.UserB.FullName
		}
		// name_similarity: Levenshtein ratio on the resolved name (case-insensitive).
		nameSim := usernameSimilarity(nameA, nameB)
		// dob_match: 1.0 when both users share an identical, non-null birth date.
		var dobMatch float64
		if p.UserA.DateOfBirth != nil && p.UserB.DateOfBirth != nil &&
			*p.UserA.DateOfBirth == *p.UserB.DateOfBirth {
			dobMatch = 1.0
		}
		score := nameWeight*nameSim + dobWeight*dobMatch
		if score >= minScore {
			p.Score = score
			pairs = append(pairs, p)
		}
	}

	// Sort by score descending, apply limit.
	sortDuplicatePairs(pairs)
	if limit > 0 && len(pairs) > limit {
		pairs = pairs[:limit]
	}
	return pairs, nil
}

// ---------------------------------------------------------------
// Duplicate-detection helpers
// ---------------------------------------------------------------

// scanUserPairs scans rows produced by a self-join query that returns two
// consecutive user column sets (without a score column).
func scanUserPairs(rows *sql.Rows) ([]DuplicatePair, error) {
	defer rows.Close()
	var pairs []DuplicatePair
	for rows.Next() {
		var p DuplicatePair
		a, b := &p.UserA, &p.UserB
		if err := rows.Scan(
			&a.ID, &a.Username, &a.Email, &a.PasswordHash, &a.Role,
			&a.FailedAttempts, &a.LockedUntil, &a.DateOfBirth, &a.FullName, &a.ExternalID,
			&a.CreatedAt, &a.UpdatedAt, &a.DeletedAt,
			&b.ID, &b.Username, &b.Email, &b.PasswordHash, &b.Role,
			&b.FailedAttempts, &b.LockedUntil, &b.DateOfBirth, &b.FullName, &b.ExternalID,
			&b.CreatedAt, &b.UpdatedAt, &b.DeletedAt,
		); err != nil {
			return nil, err
		}
		pairs = append(pairs, p)
	}
	return pairs, rows.Err()
}

// usernameSimilarity returns a 0–1 Levenshtein-based similarity score for two
// usernames (case-insensitive).
func usernameSimilarity(a, b string) float64 {
	a = strings.ToLower(a)
	b = strings.ToLower(b)
	maxLen := len(a)
	if len(b) > maxLen {
		maxLen = len(b)
	}
	if maxLen == 0 {
		return 1.0
	}
	dist := levenshtein(a, b)
	return 1.0 - float64(dist)/float64(maxLen)
}

// levenshtein computes the edit distance between two strings using the
// standard dynamic-programming approach.
func levenshtein(a, b string) int {
	ra, rb := []rune(a), []rune(b)
	la, lb := len(ra), len(rb)
	if la == 0 {
		return lb
	}
	if lb == 0 {
		return la
	}
	// Use two rows of the DP table to save memory.
	prev := make([]int, lb+1)
	curr := make([]int, lb+1)
	for j := 0; j <= lb; j++ {
		prev[j] = j
	}
	for i := 1; i <= la; i++ {
		curr[0] = i
		for j := 1; j <= lb; j++ {
			cost := 1
			if ra[i-1] == rb[j-1] {
				cost = 0
			}
			curr[j] = minInt(
				curr[j-1]+1,        // insertion
				prev[j]+1,          // deletion
				prev[j-1]+cost,     // substitution
			)
		}
		prev, curr = curr, prev
	}
	return prev[lb]
}

func minInt(a, b, c int) int {
	if a < b {
		if a < c {
			return a
		}
		return c
	}
	if b < c {
		return b
	}
	return c
}

// sortDuplicatePairs sorts pairs by Score descending (insertion sort — small N).
func sortDuplicatePairs(pairs []DuplicatePair) {
	for i := 1; i < len(pairs); i++ {
		key := pairs[i]
		j := i - 1
		for j >= 0 && pairs[j].Score < key.Score {
			pairs[j+1] = pairs[j]
			j--
		}
		pairs[j+1] = key
	}
}

// MergeUsers merges duplicateID into primaryID:
//  1. Re-parents orders, ratings, comments, favorites_lists, notifications.
//  2. Inserts an entity_duplicates record.
//  3. Soft-deletes the duplicate user.
func (r *AdminRepository) MergeUsers(primaryID, duplicateID int64, mergedBy int64) error {
	tx, err := r.db.Begin()
	if err != nil {
		return fmt.Errorf("repository: MergeUsers: begin tx: %w", err)
	}
	defer tx.Rollback() //nolint:errcheck

	// 1a. Re-parent orders.
	if _, err := tx.Exec(`UPDATE orders SET user_id = ? WHERE user_id = ?`, primaryID, duplicateID); err != nil {
		return fmt.Errorf("repository: MergeUsers: orders: %w", err)
	}

	// 1b. Re-parent ratings (ignore conflicts — primary already has a rating).
	if _, err := tx.Exec(`
		UPDATE OR IGNORE ratings SET user_id = ? WHERE user_id = ?`,
		primaryID, duplicateID); err != nil {
		return fmt.Errorf("repository: MergeUsers: ratings: %w", err)
	}

	// 1c. Re-parent comments.
	if _, err := tx.Exec(`UPDATE comments SET user_id = ? WHERE user_id = ?`, primaryID, duplicateID); err != nil {
		return fmt.Errorf("repository: MergeUsers: comments: %w", err)
	}

	// 1d. Re-parent favorites_lists.
	if _, err := tx.Exec(`UPDATE favorites_lists SET user_id = ? WHERE user_id = ?`, primaryID, duplicateID); err != nil {
		return fmt.Errorf("repository: MergeUsers: favorites_lists: %w", err)
	}

	// 1e. Re-parent notifications.
	if _, err := tx.Exec(`UPDATE notifications SET user_id = ? WHERE user_id = ?`, primaryID, duplicateID); err != nil {
		return fmt.Errorf("repository: MergeUsers: notifications: %w", err)
	}

	// 2. Insert entity_duplicates record.
	const insertDupQ = `
		INSERT INTO entity_duplicates
		            (entity_type, primary_id, duplicate_id, status, merged_by, merged_at)
		VALUES      ('user', ?, ?, 'merged', ?, datetime('now'))`
	if _, err := tx.Exec(insertDupQ, primaryID, duplicateID, mergedBy); err != nil {
		return fmt.Errorf("repository: MergeUsers: insert entity_duplicates: %w", err)
	}

	// 3. Soft-delete the duplicate.
	const softDeleteQ = `
		UPDATE users
		SET    deleted_at = datetime('now'),
		       updated_at = datetime('now')
		WHERE  id = ? AND deleted_at IS NULL`
	if _, err := tx.Exec(softDeleteQ, duplicateID); err != nil {
		return fmt.Errorf("repository: MergeUsers: soft-delete duplicate: %w", err)
	}

	return tx.Commit()
}

// GetMergeHistory returns the most-recent limit entity_duplicates records.
func (r *AdminRepository) GetMergeHistory(limit int) ([]models.EntityDuplicate, error) {
	const q = `
		SELECT id, entity_type, primary_id, duplicate_id, status, merged_by, merged_at
		FROM   entity_duplicates
		ORDER  BY merged_at DESC
		LIMIT  ?`

	rows, err := r.db.Query(q, limit)
	if err != nil {
		return nil, fmt.Errorf("repository: GetMergeHistory: %w", err)
	}
	defer rows.Close()

	var out []models.EntityDuplicate
	for rows.Next() {
		var e models.EntityDuplicate
		if err := rows.Scan(
			&e.ID, &e.EntityType, &e.PrimaryID, &e.DuplicateID,
			&e.Status, &e.MergedBy, &e.MergedAt,
		); err != nil {
			return nil, fmt.Errorf("repository: GetMergeHistory: scan: %w", err)
		}
		out = append(out, e)
	}
	return out, rows.Err()
}

// ---------------------------------------------------------------
// Audit log
// ---------------------------------------------------------------

// WriteAuditLog inserts a row into the audit_log table.
func (r *AdminRepository) WriteAuditLog(actorID int64, action, entityType string, entityID int64, before, after interface{}, ip string) error {
	var beforeJSON, afterJSON *string
	if before != nil {
		b, err := json.Marshal(before)
		if err == nil {
			s := string(b)
			beforeJSON = &s
		}
	}
	if after != nil {
		b, err := json.Marshal(after)
		if err == nil {
			s := string(b)
			afterJSON = &s
		}
	}
	var ipPtr *string
	if ip != "" {
		ipPtr = &ip
	}

	const q = `
		INSERT INTO audit_log (actor_id, action, entity_type, entity_id, before_data, after_data, ip, created_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, datetime('now'))`
	_, err := r.db.Exec(q, actorID, action, entityType, entityID, beforeJSON, afterJSON, ipPtr)
	if err != nil {
		return fmt.Errorf("repository: WriteAuditLog: %w", err)
	}
	return nil
}

// GetAuditLog returns paginated audit log entries for a specific entity.
func (r *AdminRepository) GetAuditLog(entityType string, entityID int64, limit, offset int) ([]models.AuditLog, error) {
	where := []string{"1=1"}
	args := []interface{}{}
	if entityType != "" {
		where = append(where, "entity_type = ?")
		args = append(args, entityType)
	}
	if entityID > 0 {
		where = append(where, "entity_id = ?")
		args = append(args, entityID)
	}
	q := `
		SELECT id, actor_id, action, entity_type, entity_id, before_data, after_data, ip, created_at
		FROM   audit_log
		WHERE  ` + strings.Join(where, " AND ") + `
		ORDER  BY created_at DESC
		LIMIT  ? OFFSET ?`
	args = append(args, limit, offset)

	rows, err := r.db.Query(q, args...)
	if err != nil {
		return nil, fmt.Errorf("repository: GetAuditLog: %w", err)
	}
	defer rows.Close()
	return scanAuditLogs(rows)
}

// GetRecentAuditLog returns the most-recent limit audit log entries.
func (r *AdminRepository) GetRecentAuditLog(limit int) ([]models.AuditLog, error) {
	const q = `
		SELECT id, actor_id, action, entity_type, entity_id, before_data, after_data, ip, created_at
		FROM   audit_log
		ORDER  BY created_at DESC
		LIMIT  ?`

	rows, err := r.db.Query(q, limit)
	if err != nil {
		return nil, fmt.Errorf("repository: GetRecentAuditLog: %w", err)
	}
	defer rows.Close()
	return scanAuditLogs(rows)
}

// ---------------------------------------------------------------
// User management (admin)
// ---------------------------------------------------------------

// ListUsers returns paginated users, optionally filtered by role.
func (r *AdminRepository) ListUsers(role string, limit, offset int) ([]models.User, error) {
	args := []interface{}{}
	where := "deleted_at IS NULL"
	if role != "" {
		where += " AND role = ?"
		args = append(args, role)
	}
	q := `
		SELECT id, username, email, password_hash, role,
		       failed_attempts, locked_until, date_of_birth, full_name, external_id,
		       created_at, updated_at, deleted_at
		FROM   users
		WHERE  ` + where + `
		ORDER  BY id
		LIMIT  ? OFFSET ?`
	args = append(args, limit, offset)

	rows, err := r.db.Query(q, args...)
	if err != nil {
		return nil, fmt.Errorf("repository: ListUsers: %w", err)
	}
	defer rows.Close()

	var out []models.User
	for rows.Next() {
		u, err := scanUserRow(rows)
		if err != nil {
			return nil, fmt.Errorf("repository: ListUsers: scan: %w", err)
		}
		out = append(out, *u)
	}
	return out, rows.Err()
}

// UpdateUserRole changes the role of the given user.
func (r *AdminRepository) UpdateUserRole(userID int64, role string) error {
	const q = `UPDATE users SET role = ?, updated_at = datetime('now') WHERE id = ? AND deleted_at IS NULL`
	_, err := r.db.Exec(q, role, userID)
	if err != nil {
		return fmt.Errorf("repository: UpdateUserRole: %w", err)
	}
	return nil
}

// UnlockUser clears locked_until and resets failed_attempts.
func (r *AdminRepository) UnlockUser(userID int64) error {
	const q = `
		UPDATE users
		SET    locked_until    = NULL,
		       failed_attempts = 0,
		       updated_at      = datetime('now')
		WHERE  id = ? AND deleted_at IS NULL`
	_, err := r.db.Exec(q, userID)
	if err != nil {
		return fmt.Errorf("repository: UnlockUser: %w", err)
	}
	return nil
}

// ---------------------------------------------------------------
// helpers
// ---------------------------------------------------------------

func scanAuditLogs(rows *sql.Rows) ([]models.AuditLog, error) {
	var out []models.AuditLog
	for rows.Next() {
		var a models.AuditLog
		if err := rows.Scan(
			&a.ID, &a.ActorID, &a.Action, &a.EntityType, &a.EntityID,
			&a.BeforeData, &a.AfterData, &a.IP, &a.CreatedAt,
		); err != nil {
			return nil, fmt.Errorf("scanAuditLogs: %w", err)
		}
		out = append(out, a)
	}
	return out, rows.Err()
}

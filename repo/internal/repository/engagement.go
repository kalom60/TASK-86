package repository

import (
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"errors"
	"fmt"
	"time"

	"w2t86/internal/models"
)

// ErrShareExpired is returned when a share token exists but has passed its expiry time.
var ErrShareExpired = errors.New("share link has expired")

// EngagementRepository provides database operations for browse history, ratings,
// comments, comment reports, and favorites lists/items.
type EngagementRepository struct {
	db *sql.DB
}

// NewEngagementRepository returns an EngagementRepository backed by the given database.
func NewEngagementRepository(db *sql.DB) *EngagementRepository {
	return &EngagementRepository{db: db}
}

// ---------------------------------------------------------------
// Browse history
// ---------------------------------------------------------------

// RecordVisit inserts (or replaces) a browse history row for the given user + material.
func (r *EngagementRepository) RecordVisit(userID, materialID int64) error {
	const q = `
		INSERT INTO browse_history (user_id, material_id, visited_at)
		VALUES (?, ?, datetime('now'))`
	_, err := r.db.Exec(q, userID, materialID)
	return err
}

// GetHistory returns the most-recently visited materials for a user.
func (r *EngagementRepository) GetHistory(userID int64, limit int) ([]models.BrowseHistory, error) {
	const q = `
		SELECT id, user_id, material_id, visited_at
		FROM   browse_history
		WHERE  user_id = ?
		ORDER  BY visited_at DESC
		LIMIT  ?`

	rows, err := r.db.Query(q, userID, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var items []models.BrowseHistory
	for rows.Next() {
		var bh models.BrowseHistory
		if err := rows.Scan(&bh.ID, &bh.UserID, &bh.MaterialID, &bh.VisitedAt); err != nil {
			return nil, err
		}
		items = append(items, bh)
	}
	return items, rows.Err()
}

// GetHistoryItems returns browse history joined with material titles for a user.
func (r *EngagementRepository) GetHistoryItems(userID int64, limit int) ([]models.HistoryItem, error) {
	const q = `
		SELECT bh.material_id, m.title, bh.visited_at
		FROM   browse_history bh
		JOIN   materials m ON m.id = bh.material_id
		WHERE  bh.user_id = ?
		ORDER  BY bh.visited_at DESC
		LIMIT  ?`

	rows, err := r.db.Query(q, userID, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var items []models.HistoryItem
	for rows.Next() {
		var hi models.HistoryItem
		if err := rows.Scan(&hi.MaterialID, &hi.MaterialTitle, &hi.VisitedAt); err != nil {
			return nil, err
		}
		items = append(items, hi)
	}
	return items, rows.Err()
}

// ---------------------------------------------------------------
// Ratings
// ---------------------------------------------------------------

// UpsertRating inserts or updates the star rating for a material/user pair.
func (r *EngagementRepository) UpsertRating(materialID, userID int64, stars int) error {
	const q = `
		INSERT INTO ratings (material_id, user_id, stars, created_at)
		VALUES (?, ?, ?, datetime('now'))
		ON CONFLICT(material_id, user_id) DO UPDATE SET stars = excluded.stars`
	_, err := r.db.Exec(q, materialID, userID, stars)
	return err
}

// GetRating returns the rating a specific user gave to a material, or nil if none.
func (r *EngagementRepository) GetRating(materialID, userID int64) (*models.Rating, error) {
	const q = `
		SELECT id, material_id, user_id, stars, created_at
		FROM   ratings
		WHERE  material_id = ? AND user_id = ?`

	row := r.db.QueryRow(q, materialID, userID)
	rt := &models.Rating{}
	err := row.Scan(&rt.ID, &rt.MaterialID, &rt.UserID, &rt.Stars, &rt.CreatedAt)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return rt, nil
}

// GetAverageRating returns the average star rating and total count for a material.
func (r *EngagementRepository) GetAverageRating(materialID int64) (float64, int, error) {
	const q = `
		SELECT COALESCE(AVG(CAST(stars AS REAL)), 0.0), COUNT(*)
		FROM   ratings
		WHERE  material_id = ?`

	var avg float64
	var count int
	err := r.db.QueryRow(q, materialID).Scan(&avg, &count)
	return avg, count, err
}

// ---------------------------------------------------------------
// Comments
// ---------------------------------------------------------------

// CreateComment inserts a new comment and returns the populated model.
func (r *EngagementRepository) CreateComment(materialID, userID int64, body string, linkCount int) (*models.Comment, error) {
	const q = `
		INSERT INTO comments (material_id, user_id, body, link_count, status, report_count)
		VALUES (?, ?, ?, ?, 'visible', 0)
		RETURNING id, material_id, user_id, body, link_count, status, report_count, created_at, updated_at`

	row := r.db.QueryRow(q, materialID, userID, body, linkCount)
	c := &models.Comment{}
	err := row.Scan(&c.ID, &c.MaterialID, &c.UserID, &c.Body, &c.LinkCount,
		&c.Status, &c.ReportCount, &c.CreatedAt, &c.UpdatedAt)
	if err != nil {
		return nil, err
	}
	return c, nil
}

// GetComments returns comments for a material, optionally including collapsed ones.
func (r *EngagementRepository) GetComments(materialID int64, includeCollapsed bool, limit, offset int) ([]models.Comment, error) {
	var q string
	if includeCollapsed {
		q = `
			SELECT id, material_id, user_id, body, link_count, status, report_count, created_at, updated_at
			FROM   comments
			WHERE  material_id = ?
			ORDER  BY created_at DESC
			LIMIT  ? OFFSET ?`
	} else {
		q = `
			SELECT id, material_id, user_id, body, link_count, status, report_count, created_at, updated_at
			FROM   comments
			WHERE  material_id = ? AND status != 'removed'
			ORDER  BY created_at DESC
			LIMIT  ? OFFSET ?`
	}

	rows, err := r.db.Query(q, materialID, limit, offset)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var comments []models.Comment
	for rows.Next() {
		var c models.Comment
		if err := rows.Scan(&c.ID, &c.MaterialID, &c.UserID, &c.Body, &c.LinkCount,
			&c.Status, &c.ReportCount, &c.CreatedAt, &c.UpdatedAt); err != nil {
			return nil, err
		}
		comments = append(comments, c)
	}
	return comments, rows.Err()
}

// ReportComment records a report for a comment.
// After inserting the report, it counts unique reporters; if >= 3, the comment
// status is set to 'collapsed'.
func (r *EngagementRepository) ReportComment(commentID, reportedBy int64, reason string) error {
	tx, err := r.db.Begin()
	if err != nil {
		return err
	}
	defer func() {
		if err != nil {
			_ = tx.Rollback()
		}
	}()

	const insertReport = `
		INSERT OR IGNORE INTO comment_reports (comment_id, reported_by, reason, created_at)
		VALUES (?, ?, ?, datetime('now'))`
	_, err = tx.Exec(insertReport, commentID, reportedBy, reason)
	if err != nil {
		return err
	}

	var uniqueReporters int
	const countQ = `SELECT COUNT(DISTINCT reported_by) FROM comment_reports WHERE comment_id = ?`
	err = tx.QueryRow(countQ, commentID).Scan(&uniqueReporters)
	if err != nil {
		return err
	}

	if uniqueReporters >= 3 {
		const collapseQ = `UPDATE comments SET status = 'collapsed', updated_at = datetime('now') WHERE id = ? AND status = 'visible'`
		if _, err = tx.Exec(collapseQ, commentID); err != nil {
			return err
		}
	}

	return tx.Commit()
}

// CountRecentComments returns the number of comments a user has posted since the given time.
func (r *EngagementRepository) CountRecentComments(userID int64, since time.Time) (int, error) {
	const q = `SELECT COUNT(*) FROM comments WHERE user_id = ? AND created_at >= ?`
	var count int
	err := r.db.QueryRow(q, userID, since.UTC().Format(time.RFC3339)).Scan(&count)
	return count, err
}

// ---------------------------------------------------------------
// Favorites
// ---------------------------------------------------------------

// CreateList creates a new favorites list for a user.
func (r *EngagementRepository) CreateList(userID int64, name, visibility string) (*models.FavoritesList, error) {
	const q = `
		INSERT INTO favorites_lists (user_id, name, visibility)
		VALUES (?, ?, ?)
		RETURNING id, user_id, name, visibility, share_token, share_expires_at, created_at`

	row := r.db.QueryRow(q, userID, name, visibility)
	fl := &models.FavoritesList{}
	err := row.Scan(&fl.ID, &fl.UserID, &fl.Name, &fl.Visibility,
		&fl.ShareToken, &fl.ShareExpiresAt, &fl.CreatedAt)
	if err != nil {
		return nil, err
	}
	return fl, nil
}

// GetLists returns all favorites lists for a user.
func (r *EngagementRepository) GetLists(userID int64) ([]models.FavoritesList, error) {
	const q = `
		SELECT id, user_id, name, visibility, share_token, share_expires_at, created_at
		FROM   favorites_lists
		WHERE  user_id = ?
		ORDER  BY created_at DESC`

	rows, err := r.db.Query(q, userID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var lists []models.FavoritesList
	for rows.Next() {
		var fl models.FavoritesList
		if err := rows.Scan(&fl.ID, &fl.UserID, &fl.Name, &fl.Visibility,
			&fl.ShareToken, &fl.ShareExpiresAt, &fl.CreatedAt); err != nil {
			return nil, err
		}
		lists = append(lists, fl)
	}
	return lists, rows.Err()
}

// GetListByID returns a single favorites list by its primary key.
func (r *EngagementRepository) GetListByID(id int64) (*models.FavoritesList, error) {
	const q = `
		SELECT id, user_id, name, visibility, share_token, share_expires_at, created_at
		FROM   favorites_lists
		WHERE  id = ?`

	row := r.db.QueryRow(q, id)
	fl := &models.FavoritesList{}
	err := row.Scan(&fl.ID, &fl.UserID, &fl.Name, &fl.Visibility,
		&fl.ShareToken, &fl.ShareExpiresAt, &fl.CreatedAt)
	if err != nil {
		return nil, err
	}
	return fl, nil
}

// GetListByShareToken returns the favorites list matching the given share token.
// Returns ErrShareExpired if the token exists but has expired, sql.ErrNoRows if
// no such token exists at all.
func (r *EngagementRepository) GetListByShareToken(token string) (*models.FavoritesList, error) {
	const q = `
		SELECT id, user_id, name, visibility, share_token, share_expires_at, created_at
		FROM   favorites_lists
		WHERE  share_token = ?`

	row := r.db.QueryRow(q, token)
	fl := &models.FavoritesList{}
	err := row.Scan(&fl.ID, &fl.UserID, &fl.Name, &fl.Visibility,
		&fl.ShareToken, &fl.ShareExpiresAt, &fl.CreatedAt)
	if err != nil {
		return nil, err
	}
	// Check expiry in Go so callers can distinguish expired vs not-found.
	if fl.ShareExpiresAt != nil {
		if exp, parseErr := time.Parse(time.RFC3339, *fl.ShareExpiresAt); parseErr == nil {
			if time.Now().After(exp) {
				return nil, ErrShareExpired
			}
		}
	}
	return fl, nil
}

// AddToList inserts a material into a favorites list (ignores duplicates).
func (r *EngagementRepository) AddToList(listID, materialID int64) error {
	const q = `
		INSERT OR IGNORE INTO favorites_items (list_id, material_id, added_at)
		VALUES (?, ?, datetime('now'))`
	_, err := r.db.Exec(q, listID, materialID)
	return err
}

// RemoveFromList deletes a material from a favorites list.
func (r *EngagementRepository) RemoveFromList(listID, materialID int64) error {
	const q = `DELETE FROM favorites_items WHERE list_id = ? AND material_id = ?`
	_, err := r.db.Exec(q, listID, materialID)
	return err
}

// GetListItems returns all items in a favorites list.
func (r *EngagementRepository) GetListItems(listID int64) ([]models.FavoritesItem, error) {
	const q = `
		SELECT id, list_id, material_id, added_at
		FROM   favorites_items
		WHERE  list_id = ?
		ORDER  BY added_at DESC`

	rows, err := r.db.Query(q, listID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var items []models.FavoritesItem
	for rows.Next() {
		var fi models.FavoritesItem
		if err := rows.Scan(&fi.ID, &fi.ListID, &fi.MaterialID, &fi.AddedAt); err != nil {
			return nil, err
		}
		items = append(items, fi)
	}
	return items, rows.Err()
}

// GenerateShareToken creates a crypto-random share token, stores it on the
// favorites list, and returns the raw token string.
func (r *EngagementRepository) GenerateShareToken(listID int64, expiresAt time.Time) (string, error) {
	b := make([]byte, 24)
	if _, err := rand.Read(b); err != nil {
		return "", fmt.Errorf("repository: GenerateShareToken: rand: %w", err)
	}
	token := hex.EncodeToString(b)

	const q = `
		UPDATE favorites_lists
		SET    share_token = ?, share_expires_at = ?
		WHERE  id = ?`
	_, err := r.db.Exec(q, token, expiresAt.UTC().Format(time.RFC3339), listID)
	if err != nil {
		return "", err
	}
	return token, nil
}

// UpdateListVisibility sets the visibility field on a favorites list.
func (r *EngagementRepository) UpdateListVisibility(listID int64, visibility string) error {
	const q = `UPDATE favorites_lists SET visibility = ? WHERE id = ?`
	_, err := r.db.Exec(q, visibility, listID)
	return err
}

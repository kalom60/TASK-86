package repository

import (
	"database/sql"
	"errors"
	"fmt"

	"w2t86/internal/models"
)

// ErrCommentNotReviewable is returned when an approve/remove action targets a
// comment that does not exist or is not in the 'collapsed' state.
var ErrCommentNotReviewable = errors.New("comment not found or not in reviewable state")

// ModerationItem bundles a collapsed comment with its contextual data for the
// moderation queue.
type ModerationItem struct {
	Comment       models.Comment
	ReportCount   int
	Reporters     []string // usernames of reporters
	MaterialTitle string
	AuthorName    string
}

// ModerationRepository provides database operations for the moderation queue.
type ModerationRepository struct {
	db *sql.DB
}

// NewModerationRepository returns a ModerationRepository backed by the given database.
func NewModerationRepository(db *sql.DB) *ModerationRepository {
	return &ModerationRepository{db: db}
}

// GetPendingReview returns paginated comments whose status is 'collapsed',
// enriched with report counts, reporter usernames, material title, and author name.
func (r *ModerationRepository) GetPendingReview(limit, offset int) ([]ModerationItem, error) {
	// Fetch collapsed comments with their material title and author name.
	const q = `
		SELECT c.id, c.material_id, c.user_id, c.body, c.link_count,
		       c.status, c.report_count, c.created_at, c.updated_at,
		       m.title      AS material_title,
		       u.username   AS author_name
		FROM   comments c
		JOIN   materials m ON m.id = c.material_id
		JOIN   users     u ON u.id = c.user_id
		WHERE  c.status = 'collapsed'
		ORDER  BY c.report_count DESC, c.created_at ASC
		LIMIT  ? OFFSET ?`

	rows, err := r.db.Query(q, limit, offset)
	if err != nil {
		return nil, fmt.Errorf("repository: ModerationRepository.GetPendingReview: %w", err)
	}
	defer rows.Close()

	type rawRow struct {
		comment       models.Comment
		materialTitle string
		authorName    string
	}
	var raws []rawRow

	for rows.Next() {
		var rw rawRow
		c := &rw.comment
		if err := rows.Scan(
			&c.ID, &c.MaterialID, &c.UserID, &c.Body, &c.LinkCount,
			&c.Status, &c.ReportCount, &c.CreatedAt, &c.UpdatedAt,
			&rw.materialTitle, &rw.authorName,
		); err != nil {
			return nil, fmt.Errorf("repository: ModerationRepository.GetPendingReview: scan: %w", err)
		}
		raws = append(raws, rw)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("repository: ModerationRepository.GetPendingReview: rows: %w", err)
	}

	// For each comment, fetch the reporter usernames.
	const reportersQ = `
		SELECT u.username
		FROM   comment_reports cr
		JOIN   users u ON u.id = cr.reported_by
		WHERE  cr.comment_id = ?
		ORDER  BY cr.created_at ASC`

	items := make([]ModerationItem, 0, len(raws))
	for _, rw := range raws {
		rptRows, err := r.db.Query(reportersQ, rw.comment.ID)
		if err != nil {
			return nil, fmt.Errorf("repository: ModerationRepository.GetPendingReview: reporters query: %w", err)
		}
		var reporters []string
		for rptRows.Next() {
			var uname string
			if err := rptRows.Scan(&uname); err != nil {
				rptRows.Close()
				return nil, fmt.Errorf("repository: ModerationRepository.GetPendingReview: reporters scan: %w", err)
			}
			reporters = append(reporters, uname)
		}
		rptRows.Close()
		if err := rptRows.Err(); err != nil {
			return nil, fmt.Errorf("repository: ModerationRepository.GetPendingReview: reporters rows: %w", err)
		}

		items = append(items, ModerationItem{
			Comment:       rw.comment,
			ReportCount:   rw.comment.ReportCount,
			Reporters:     reporters,
			MaterialTitle: rw.materialTitle,
			AuthorName:    rw.authorName,
		})
	}
	return items, nil
}

// ApproveComment sets a comment's status to 'active'. Reports are kept for audit.
func (r *ModerationRepository) ApproveComment(commentID, moderatorID int64) error {
	const q = `
		UPDATE comments
		SET    status     = 'active',
		       updated_at = datetime('now')
		WHERE  id = ? AND status = 'collapsed'`

	res, err := r.db.Exec(q, commentID)
	if err != nil {
		return fmt.Errorf("repository: ModerationRepository.ApproveComment: %w", err)
	}
	n, err := res.RowsAffected()
	if err != nil {
		return fmt.Errorf("repository: ModerationRepository.ApproveComment: rows affected: %w", err)
	}
	if n == 0 {
		return fmt.Errorf("repository: ModerationRepository.ApproveComment: comment %d: %w", commentID, ErrCommentNotReviewable)
	}
	return nil
}

// RemoveComment sets a comment's status to 'removed'.
func (r *ModerationRepository) RemoveComment(commentID, moderatorID int64) error {
	const q = `
		UPDATE comments
		SET    status     = 'removed',
		       updated_at = datetime('now')
		WHERE  id = ? AND status = 'collapsed'`

	res, err := r.db.Exec(q, commentID)
	if err != nil {
		return fmt.Errorf("repository: ModerationRepository.RemoveComment: %w", err)
	}
	n, err := res.RowsAffected()
	if err != nil {
		return fmt.Errorf("repository: ModerationRepository.RemoveComment: rows affected: %w", err)
	}
	if n == 0 {
		return fmt.Errorf("repository: ModerationRepository.RemoveComment: comment %d: %w", commentID, ErrCommentNotReviewable)
	}
	return nil
}

// CountPending returns the number of comments awaiting moderation review.
func (r *ModerationRepository) CountPending() (int, error) {
	const q = `SELECT COUNT(*) FROM comments WHERE status = 'collapsed'`

	var n int
	if err := r.db.QueryRow(q).Scan(&n); err != nil {
		return 0, fmt.Errorf("repository: ModerationRepository.CountPending: %w", err)
	}
	return n, nil
}

package repository

import (
	"database/sql"
	"fmt"
	"time"

	"w2t86/internal/models"
)

// MessagingRepository provides database operations for notifications,
// DND settings, and topic subscriptions.
type MessagingRepository struct {
	db *sql.DB
}

// NewMessagingRepository returns a MessagingRepository backed by the given database.
func NewMessagingRepository(db *sql.DB) *MessagingRepository {
	return &MessagingRepository{db: db}
}

// ---------------------------------------------------------------
// Notifications
// ---------------------------------------------------------------

// Create inserts a new notification row and returns it populated.
func (r *MessagingRepository) Create(n *models.Notification) (*models.Notification, error) {
	const q = `
		INSERT INTO notifications (user_id, type, title, body, ref_id, ref_type, delivered_at)
		VALUES (?, ?, ?, ?, ?, ?, ?)
		RETURNING id, user_id, type, title, body, ref_id, ref_type, read_at, delivered_at, created_at`

	row := r.db.QueryRow(q,
		n.UserID, n.Type, n.Title, n.Body,
		n.RefID, n.RefType, n.DeliveredAt,
	)
	out := &models.Notification{}
	if err := row.Scan(
		&out.ID, &out.UserID, &out.Type, &out.Title, &out.Body,
		&out.RefID, &out.RefType, &out.ReadAt, &out.DeliveredAt, &out.CreatedAt,
	); err != nil {
		return nil, fmt.Errorf("repository: MessagingRepository.Create: %w", err)
	}
	return out, nil
}

// GetForUser returns paginated notifications for a user, newest first.
func (r *MessagingRepository) GetForUser(userID int64, limit, offset int) ([]models.Notification, error) {
	const q = `
		SELECT id, user_id, type, title, body, ref_id, ref_type, read_at, delivered_at, created_at
		FROM   notifications
		WHERE  user_id = ?
		ORDER  BY created_at DESC
		LIMIT  ? OFFSET ?`

	rows, err := r.db.Query(q, userID, limit, offset)
	if err != nil {
		return nil, fmt.Errorf("repository: MessagingRepository.GetForUser: %w", err)
	}
	defer rows.Close()

	var out []models.Notification
	for rows.Next() {
		var n models.Notification
		if err := rows.Scan(
			&n.ID, &n.UserID, &n.Type, &n.Title, &n.Body,
			&n.RefID, &n.RefType, &n.ReadAt, &n.DeliveredAt, &n.CreatedAt,
		); err != nil {
			return nil, fmt.Errorf("repository: MessagingRepository.GetForUser: scan: %w", err)
		}
		out = append(out, n)
	}
	return out, rows.Err()
}

// MarkRead sets read_at = now() for the notification with the given id,
// only if it belongs to userID.
func (r *MessagingRepository) MarkRead(id, userID int64) error {
	const q = `
		UPDATE notifications
		SET    read_at = datetime('now')
		WHERE  id = ? AND user_id = ? AND read_at IS NULL`

	if _, err := r.db.Exec(q, id, userID); err != nil {
		return fmt.Errorf("repository: MessagingRepository.MarkRead: %w", err)
	}
	return nil
}

// MarkAllRead sets read_at = now() for all unread notifications belonging to userID.
func (r *MessagingRepository) MarkAllRead(userID int64) error {
	const q = `
		UPDATE notifications
		SET    read_at = datetime('now')
		WHERE  user_id = ? AND read_at IS NULL`

	if _, err := r.db.Exec(q, userID); err != nil {
		return fmt.Errorf("repository: MessagingRepository.MarkAllRead: %w", err)
	}
	return nil
}

// CountUnread returns the number of unread notifications for userID.
func (r *MessagingRepository) CountUnread(userID int64) (int, error) {
	const q = `SELECT COUNT(*) FROM notifications WHERE user_id = ? AND read_at IS NULL`

	var n int
	if err := r.db.QueryRow(q, userID).Scan(&n); err != nil {
		return 0, fmt.Errorf("repository: MessagingRepository.CountUnread: %w", err)
	}
	return n, nil
}

// MarkDelivered sets delivered_at = now() for the notification with the given id.
func (r *MessagingRepository) MarkDelivered(id int64) error {
	const q = `
		UPDATE notifications
		SET    delivered_at = datetime('now')
		WHERE  id = ? AND delivered_at IS NULL`

	if _, err := r.db.Exec(q, id); err != nil {
		return fmt.Errorf("repository: MessagingRepository.MarkDelivered: %w", err)
	}
	return nil
}

// ---------------------------------------------------------------
// DND Settings
// ---------------------------------------------------------------

// GetDND returns the DND setting for userID, or nil if none exists.
func (r *MessagingRepository) GetDND(userID int64) (*models.DNDSetting, error) {
	const q = `
		SELECT id, user_id, start_hour, end_hour, updated_at
		FROM   dnd_settings
		WHERE  user_id = ?`

	row := r.db.QueryRow(q, userID)
	d := &models.DNDSetting{}
	if err := row.Scan(&d.ID, &d.UserID, &d.StartHour, &d.EndHour, &d.UpdatedAt); err != nil {
		if err == sql.ErrNoRows {
			return nil, nil
		}
		return nil, fmt.Errorf("repository: MessagingRepository.GetDND: %w", err)
	}
	return d, nil
}

// SetDND upserts the DND window (start_hour..end_hour in UTC) for userID.
func (r *MessagingRepository) SetDND(userID int64, startHour, endHour int) error {
	const q = `
		INSERT INTO dnd_settings (user_id, start_hour, end_hour, updated_at)
		VALUES (?, ?, ?, datetime('now'))
		ON CONFLICT(user_id) DO UPDATE
		SET start_hour = excluded.start_hour,
		    end_hour   = excluded.end_hour,
		    updated_at = excluded.updated_at`

	if _, err := r.db.Exec(q, userID, startHour, endHour); err != nil {
		return fmt.Errorf("repository: MessagingRepository.SetDND: %w", err)
	}
	return nil
}

// IsInDND reports whether the current UTC hour falls within the user's DND window.
// Handles the wrap-around case (e.g. start=21, end=7 means 21:00–06:59 next day).
func (r *MessagingRepository) IsInDND(userID int64) (bool, error) {
	dnd, err := r.GetDND(userID)
	if err != nil {
		return false, err
	}
	if dnd == nil {
		return false, nil
	}

	currentHour := time.Now().UTC().Hour()
	start := dnd.StartHour
	end := dnd.EndHour

	var inWindow bool
	if start < end {
		// Normal window, e.g. 9AM–5PM: current >= start AND current < end
		inWindow = currentHour >= start && currentHour < end
	} else {
		// Wrap-around window, e.g. 9PM–7AM: current >= start OR current < end
		inWindow = currentHour >= start || currentHour < end
	}
	return inWindow, nil
}

// ---------------------------------------------------------------
// Subscriptions
// ---------------------------------------------------------------

// GetSubscriptions returns all active subscription rows for userID.
func (r *MessagingRepository) GetSubscriptions(userID int64) ([]models.Subscription, error) {
	const q = `
		SELECT id, user_id, topic, active, created_at
		FROM   subscriptions
		WHERE  user_id = ?
		ORDER  BY topic`

	rows, err := r.db.Query(q, userID)
	if err != nil {
		return nil, fmt.Errorf("repository: MessagingRepository.GetSubscriptions: %w", err)
	}
	defer rows.Close()

	var out []models.Subscription
	for rows.Next() {
		var s models.Subscription
		if err := rows.Scan(&s.ID, &s.UserID, &s.Topic, &s.Active, &s.CreatedAt); err != nil {
			return nil, fmt.Errorf("repository: MessagingRepository.GetSubscriptions: scan: %w", err)
		}
		out = append(out, s)
	}
	return out, rows.Err()
}

// Subscribe upserts a subscription row with active=1.
func (r *MessagingRepository) Subscribe(userID int64, topic string) error {
	const q = `
		INSERT INTO subscriptions (user_id, topic, active)
		VALUES (?, ?, 1)
		ON CONFLICT(user_id, topic) DO UPDATE
		SET active = 1`

	if _, err := r.db.Exec(q, userID, topic); err != nil {
		return fmt.Errorf("repository: MessagingRepository.Subscribe: %w", err)
	}
	return nil
}

// Unsubscribe sets active=0 for the given user+topic subscription.
func (r *MessagingRepository) Unsubscribe(userID int64, topic string) error {
	const q = `
		UPDATE subscriptions
		SET    active = 0
		WHERE  user_id = ? AND topic = ?`

	if _, err := r.db.Exec(q, userID, topic); err != nil {
		return fmt.Errorf("repository: MessagingRepository.Unsubscribe: %w", err)
	}
	return nil
}

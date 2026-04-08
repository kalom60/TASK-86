package services

import (
	"database/sql"
	"fmt"
	"time"

	"w2t86/internal/models"
	"w2t86/internal/repository"
)

// Topic constants for notification subscriptions.
const (
	TopicOrders        = "orders"
	TopicReturns       = "returns"
	TopicDistribution  = "distribution"
	TopicAnnouncements = "announcements"
	TopicModeration    = "moderation"
)

// MessagingService orchestrates notification delivery with DND awareness,
// inbox management, and subscription control.
type MessagingService struct {
	msgRepo *repository.MessagingRepository
}

// NewMessagingService creates a MessagingService wired to the given repository.
func NewMessagingService(mr *repository.MessagingRepository) *MessagingService {
	return &MessagingService{msgRepo: mr}
}

// ---------------------------------------------------------------
// Sending
// ---------------------------------------------------------------

// Send creates a notification for userID. If the user is currently in their
// DND window the notification is persisted but delivered_at is left NULL so
// that it will be pushed later. Outside DND the delivered_at is set to now.
func (s *MessagingService) Send(userID int64, notifType, title, body string, refID *int64, refType *string) error {
	inDND, err := s.msgRepo.IsInDND(userID)
	if err != nil {
		return fmt.Errorf("service: MessagingService.Send: check DND: %w", err)
	}

	var deliveredAt *string
	if !inDND {
		now := time.Now().UTC().Format(time.RFC3339)
		deliveredAt = &now
	}

	var bodyPtr *string
	if body != "" {
		bodyPtr = &body
	}

	n := &models.Notification{
		UserID:      userID,
		Type:        notifType,
		Title:       title,
		Body:        bodyPtr,
		RefID:       refID,
		RefType:     refType,
		DeliveredAt: deliveredAt,
	}

	if _, err := s.msgRepo.Create(n); err != nil {
		return fmt.Errorf("service: MessagingService.Send: %w", err)
	}
	return nil
}

// SendToRole sends a notification to every non-deleted user that has the
// given role. db is the raw *sql.DB used to enumerate users.
func (s *MessagingService) SendToRole(db *sql.DB, role, notifType, title, body string) error {
	const q = `SELECT id FROM users WHERE role = ? AND deleted_at IS NULL`
	rows, err := db.Query(q, role)
	if err != nil {
		return fmt.Errorf("service: MessagingService.SendToRole: query users: %w", err)
	}
	defer rows.Close()

	var userIDs []int64
	for rows.Next() {
		var id int64
		if err := rows.Scan(&id); err != nil {
			return fmt.Errorf("service: MessagingService.SendToRole: scan: %w", err)
		}
		userIDs = append(userIDs, id)
	}
	if err := rows.Err(); err != nil {
		return fmt.Errorf("service: MessagingService.SendToRole: rows: %w", err)
	}

	for _, uid := range userIDs {
		if err := s.Send(uid, notifType, title, body, nil, nil); err != nil {
			// Continue on individual failure — best-effort broadcast.
			continue
		}
	}
	return nil
}

// ---------------------------------------------------------------
// Inbox
// ---------------------------------------------------------------

// GetInbox returns paginated notifications for the user, newest first.
func (s *MessagingService) GetInbox(userID int64, limit, offset int) ([]models.Notification, error) {
	ns, err := s.msgRepo.GetForUser(userID, limit, offset)
	if err != nil {
		return nil, fmt.Errorf("service: MessagingService.GetInbox: %w", err)
	}
	return ns, nil
}

// MarkRead marks a single notification as read (only if it belongs to userID).
func (s *MessagingService) MarkRead(notifID, userID int64) error {
	if err := s.msgRepo.MarkRead(notifID, userID); err != nil {
		return fmt.Errorf("service: MessagingService.MarkRead: %w", err)
	}
	return nil
}

// MarkAllRead marks every unread notification for userID as read.
func (s *MessagingService) MarkAllRead(userID int64) error {
	if err := s.msgRepo.MarkAllRead(userID); err != nil {
		return fmt.Errorf("service: MessagingService.MarkAllRead: %w", err)
	}
	return nil
}

// CountUnread returns the number of unread notifications for userID.
func (s *MessagingService) CountUnread(userID int64) (int, error) {
	n, err := s.msgRepo.CountUnread(userID)
	if err != nil {
		return 0, fmt.Errorf("service: MessagingService.CountUnread: %w", err)
	}
	return n, nil
}

// ---------------------------------------------------------------
// DND management
// ---------------------------------------------------------------

// GetDNDSettings returns the DND settings for userID, or nil if none set.
func (s *MessagingService) GetDNDSettings(userID int64) (*models.DNDSetting, error) {
	d, err := s.msgRepo.GetDND(userID)
	if err != nil {
		return nil, fmt.Errorf("service: MessagingService.GetDNDSettings: %w", err)
	}
	return d, nil
}

// UpdateDND saves or replaces the DND window for userID.
// startHour and endHour must be in [0, 23].
func (s *MessagingService) UpdateDND(userID int64, startHour, endHour int) error {
	if startHour < 0 || startHour > 23 || endHour < 0 || endHour > 23 {
		return fmt.Errorf("service: MessagingService.UpdateDND: hours must be in range [0, 23]")
	}
	if err := s.msgRepo.SetDND(userID, startHour, endHour); err != nil {
		return fmt.Errorf("service: MessagingService.UpdateDND: %w", err)
	}
	return nil
}

// ---------------------------------------------------------------
// Subscriptions
// ---------------------------------------------------------------

// GetSubscriptions returns all subscription rows for userID.
func (s *MessagingService) GetSubscriptions(userID int64) ([]models.Subscription, error) {
	subs, err := s.msgRepo.GetSubscriptions(userID)
	if err != nil {
		return nil, fmt.Errorf("service: MessagingService.GetSubscriptions: %w", err)
	}
	return subs, nil
}

// Subscribe activates the subscription for (userID, topic).
func (s *MessagingService) Subscribe(userID int64, topic string) error {
	if err := s.msgRepo.Subscribe(userID, topic); err != nil {
		return fmt.Errorf("service: MessagingService.Subscribe: %w", err)
	}
	return nil
}

// Unsubscribe deactivates the subscription for (userID, topic).
func (s *MessagingService) Unsubscribe(userID int64, topic string) error {
	if err := s.msgRepo.Unsubscribe(userID, topic); err != nil {
		return fmt.Errorf("service: MessagingService.Unsubscribe: %w", err)
	}
	return nil
}

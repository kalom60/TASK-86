package services

import (
	"fmt"

	"w2t86/internal/observability"
	"w2t86/internal/repository"
)

// ModerationService orchestrates comment moderation queue operations.
type ModerationService struct {
	modRepo *repository.ModerationRepository
}

// NewModerationService creates a ModerationService wired to the given repository.
func NewModerationService(mr *repository.ModerationRepository) *ModerationService {
	return &ModerationService{modRepo: mr}
}

// GetQueue returns paginated items from the moderation queue (collapsed comments).
func (s *ModerationService) GetQueue(limit, offset int) ([]repository.ModerationItem, error) {
	items, err := s.modRepo.GetPendingReview(limit, offset)
	if err != nil {
		return nil, fmt.Errorf("service: ModerationService.GetQueue: %w", err)
	}
	return items, nil
}

// CountQueue returns the total number of comments awaiting moderation.
func (s *ModerationService) CountQueue() (int, error) {
	n, err := s.modRepo.CountPending()
	if err != nil {
		return 0, fmt.Errorf("service: ModerationService.CountQueue: %w", err)
	}
	return n, nil
}

// ApproveComment approves a collapsed comment, making it active again.
// moderatorID is recorded for audit purposes.
func (s *ModerationService) ApproveComment(commentID, moderatorID int64) error {
	if err := s.modRepo.ApproveComment(commentID, moderatorID); err != nil {
		return fmt.Errorf("service: ModerationService.ApproveComment: %w", err)
	}
	observability.Moderation.Info("comment approved", "comment_id", commentID, "moderator_id", moderatorID)
	return nil
}

// RemoveComment permanently removes a collapsed comment from public view.
// moderatorID is recorded for audit purposes.
func (s *ModerationService) RemoveComment(commentID, moderatorID int64) error {
	if err := s.modRepo.RemoveComment(commentID, moderatorID); err != nil {
		return fmt.Errorf("service: ModerationService.RemoveComment: %w", err)
	}
	observability.Moderation.Info("comment removed", "comment_id", commentID, "moderator_id", moderatorID)
	return nil
}

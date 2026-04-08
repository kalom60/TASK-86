package services

import (
	"database/sql"
	"errors"
	"fmt"
	"regexp"
	"strings"
	"time"

	"w2t86/internal/models"
	"w2t86/internal/observability"
	"w2t86/internal/repository"
)

const (
	maxCommentLength    = 500
	maxCommentLinks     = 2
	maxCommentsPerHour  = 5
	commentRateWindow   = 10 * time.Minute
	shareTokenDuration  = 7 * 24 * time.Hour
)

// ---------------------------------------------------------------
// WordFilter
// ---------------------------------------------------------------

// WordFilter checks text against a list of prohibited words using compiled regexps.
type WordFilter struct {
	patterns []*regexp.Regexp
	words    []string
}

// NewWordFilter compiles a case-insensitive regexp for each word in the list.
func NewWordFilter(words []string) *WordFilter {
	patterns := make([]*regexp.Regexp, 0, len(words))
	kept := make([]string, 0, len(words))
	for _, w := range words {
		if w == "" {
			continue
		}
		re, err := regexp.Compile(`(?i)\b` + regexp.QuoteMeta(w) + `\b`)
		if err == nil {
			patterns = append(patterns, re)
			kept = append(kept, w)
		}
	}
	return &WordFilter{patterns: patterns, words: kept}
}

// Contains returns true and the first matching word if text contains any
// prohibited word, false and empty string otherwise.
func (wf *WordFilter) Contains(text string) (bool, string) {
	for i, re := range wf.patterns {
		if re.MatchString(text) {
			return true, wf.words[i]
		}
	}
	return false, ""
}

// ---------------------------------------------------------------
// MaterialService
// ---------------------------------------------------------------

// MaterialService orchestrates material catalog operations.
type MaterialService struct {
	materialRepo   *repository.MaterialRepository
	engagementRepo *repository.EngagementRepository
	wordFilter     *WordFilter
}

// NewMaterialService creates a MaterialService with an empty default word filter.
// Call SetWordFilter to provide a custom filter.
func NewMaterialService(mr *repository.MaterialRepository, er *repository.EngagementRepository) *MaterialService {
	return &MaterialService{
		materialRepo:   mr,
		engagementRepo: er,
		wordFilter:     NewWordFilter(nil),
	}
}

// SetWordFilter replaces the word filter. Call after NewMaterialService if you
// need to load prohibited words from configuration.
func (s *MaterialService) SetWordFilter(wf *WordFilter) {
	s.wordFilter = wf
}

// Search returns materials matching the FTS query, falling back to a list with
// filters when query is empty.
func (s *MaterialService) Search(query string, filters map[string]string, limit, offset int) ([]models.Material, error) {
	query = strings.TrimSpace(query)
	if query == "" {
		return s.materialRepo.List(limit, offset, filters)
	}
	return s.materialRepo.Search(query, limit, offset)
}

// GetByID returns a single material by ID.
func (s *MaterialService) GetByID(id int64) (*models.Material, error) {
	return s.materialRepo.GetByID(id)
}

// Create inserts a material and writes an initial version record.
func (s *MaterialService) Create(m *models.Material, actorID int64, db *sql.DB) (*models.Material, error) {
	created, err := s.materialRepo.Create(m)
	if err != nil {
		return nil, fmt.Errorf("service: Create material: %w", err)
	}

	if err := s.materialRepo.WriteVersion(db, created.ID, actorID, map[string]interface{}{
		"action": "create",
		"data":   created,
	}); err != nil {
		// Non-fatal: version history failure should not block the create.
		_ = err
	}
	return created, nil
}

// Update applies field changes to a material and writes a version record.
func (s *MaterialService) Update(id int64, fields map[string]interface{}, actorID int64, db *sql.DB) error {
	if err := s.materialRepo.Update(id, fields); err != nil {
		return fmt.Errorf("service: Update material %d: %w", id, err)
	}

	if err := s.materialRepo.WriteVersion(db, id, actorID, map[string]interface{}{
		"action": "update",
		"fields": fields,
	}); err != nil {
		_ = err
	}
	return nil
}

// AddComment validates and posts a new comment.
// Rules:
//   - body must not exceed 500 characters
//   - at most 2 links (href= occurrences) in body
//   - body must not contain prohibited words
//   - user must not exceed rate limit (5 in the last 10 minutes)
func (s *MaterialService) AddComment(materialID, userID int64, body string) (*models.Comment, error) {
	if len(body) > maxCommentLength {
		return nil, fmt.Errorf("comment body exceeds maximum length of %d characters", maxCommentLength)
	}

	linkCount := strings.Count(strings.ToLower(body), "href=")
	if linkCount > maxCommentLinks {
		return nil, fmt.Errorf("comment may contain at most %d links", maxCommentLinks)
	}

	if found, word := s.wordFilter.Contains(body); found {
		observability.App.Warn("comment blocked: sensitive word", "user_id", userID, "word", word)
		return nil, fmt.Errorf("comment contains prohibited content: %q", word)
	}

	since := time.Now().UTC().Add(-commentRateWindow)
	recent, err := s.engagementRepo.CountRecentComments(userID, since)
	if err != nil {
		return nil, fmt.Errorf("service: AddComment: rate check: %w", err)
	}
	if recent >= maxCommentsPerHour {
		observability.App.Warn("comment rate limit exceeded", "user_id", userID, "recent_count", recent)
		return nil, errors.New("comment rate limit exceeded, please try again later")
	}

	comment, err := s.engagementRepo.CreateComment(materialID, userID, body, linkCount)
	if err != nil {
		return nil, fmt.Errorf("service: AddComment: %w", err)
	}
	return comment, nil
}

// Rate records or updates a star rating (1–5) for a material.
func (s *MaterialService) Rate(materialID, userID int64, stars int) error {
	if stars < 1 || stars > 5 {
		return fmt.Errorf("stars must be between 1 and 5")
	}
	if err := s.engagementRepo.UpsertRating(materialID, userID, stars); err != nil {
		return fmt.Errorf("service: Rate: %w", err)
	}
	return nil
}

// CreateFavoritesList creates a new favorites list for a user.
func (s *MaterialService) CreateFavoritesList(userID int64, name, visibility string) (*models.FavoritesList, error) {
	if name == "" {
		return nil, errors.New("list name cannot be empty")
	}
	if visibility != "private" && visibility != "public" {
		visibility = "private"
	}
	fl, err := s.engagementRepo.CreateList(userID, name, visibility)
	if err != nil {
		return nil, fmt.Errorf("service: CreateFavoritesList: %w", err)
	}
	return fl, nil
}

// AddToFavorites adds a material to one of the user's favorites lists.
// Verifies that the list belongs to the requesting user.
func (s *MaterialService) AddToFavorites(listID, materialID, userID int64) error {
	lists, err := s.engagementRepo.GetLists(userID)
	if err != nil {
		return fmt.Errorf("service: AddToFavorites: fetch lists: %w", err)
	}
	owned := false
	for _, l := range lists {
		if l.ID == listID {
			owned = true
			break
		}
	}
	if !owned {
		return errors.New("list not found or does not belong to user")
	}

	if err := s.engagementRepo.AddToList(listID, materialID); err != nil {
		return fmt.Errorf("service: AddToFavorites: %w", err)
	}
	return nil
}

// GetShareLink generates (or renews) a 7-day share token for a favorites list
// owned by the given user, returning the raw token.
func (s *MaterialService) GetShareLink(listID int64, userID int64) (string, error) {
	lists, err := s.engagementRepo.GetLists(userID)
	if err != nil {
		return "", fmt.Errorf("service: GetShareLink: fetch lists: %w", err)
	}
	owned := false
	for _, l := range lists {
		if l.ID == listID {
			owned = true
			break
		}
	}
	if !owned {
		return "", errors.New("list not found or does not belong to user")
	}

	expiresAt := time.Now().UTC().Add(shareTokenDuration)
	token, err := s.engagementRepo.GenerateShareToken(listID, expiresAt)
	if err != nil {
		return "", fmt.Errorf("service: GetShareLink: %w", err)
	}
	return token, nil
}

// RecordVisit records a browse history entry for a material visit.
func (s *MaterialService) RecordVisit(materialID, userID int64) error {
	return s.engagementRepo.RecordVisit(userID, materialID)
}

// GetBrowseHistory returns the most-recently visited materials for a user.
func (s *MaterialService) GetBrowseHistory(userID int64, limit int) ([]models.BrowseHistory, error) {
	return s.engagementRepo.GetHistory(userID, limit)
}

// GetBrowseHistoryItems returns browse history joined with material titles for a user.
func (s *MaterialService) GetBrowseHistoryItems(userID int64, limit int) ([]models.HistoryItem, error) {
	return s.engagementRepo.GetHistoryItems(userID, limit)
}

// GetAverageRating returns the average star rating and count for a material.
func (s *MaterialService) GetAverageRating(materialID int64) (float64, int, error) {
	return s.engagementRepo.GetAverageRating(materialID)
}

// GetUserRating returns the star rating a user gave to a material (0 if none).
func (s *MaterialService) GetUserRating(materialID, userID int64) (int, error) {
	rt, err := s.engagementRepo.GetRating(materialID, userID)
	if err != nil {
		return 0, err
	}
	if rt == nil {
		return 0, nil
	}
	return rt.Stars, nil
}

// GetComments returns comments for a material.
func (s *MaterialService) GetComments(materialID int64, includeCollapsed bool, limit, offset int) ([]models.Comment, error) {
	return s.engagementRepo.GetComments(materialID, includeCollapsed, limit, offset)
}

// ReportComment records a report against a comment.
func (s *MaterialService) ReportComment(commentID, reportedBy int64, reason string) error {
	return s.engagementRepo.ReportComment(commentID, reportedBy, reason)
}

// CreateMaterial is a convenience wrapper for handlers that don't hold a *sql.DB directly.
// It passes the underlying db reference from the material repository.
func (s *MaterialService) CreateMaterial(m *models.Material, actorID int64) (*models.Material, error) {
	created, err := s.materialRepo.Create(m)
	if err != nil {
		return nil, fmt.Errorf("service: CreateMaterial: %w", err)
	}
	if err := s.materialRepo.WriteVersion(s.materialRepo.DB(), created.ID, actorID, map[string]interface{}{
		"action": "create",
		"data":   created,
	}); err != nil {
		_ = err
	}
	return created, nil
}

// UpdateMaterial is a convenience wrapper for handlers.
func (s *MaterialService) UpdateMaterial(id int64, fields map[string]interface{}, actorID int64) error {
	if err := s.materialRepo.Update(id, fields); err != nil {
		return fmt.Errorf("service: UpdateMaterial %d: %w", id, err)
	}
	if err := s.materialRepo.WriteVersion(s.materialRepo.DB(), id, actorID, map[string]interface{}{
		"action": "update",
		"fields": fields,
	}); err != nil {
		_ = err
	}
	return nil
}

// SoftDelete soft-deletes a material.
func (s *MaterialService) SoftDelete(id int64) error {
	return s.materialRepo.SoftDelete(id)
}

// GetFavoritesLists returns all favorites lists for a user.
func (s *MaterialService) GetFavoritesLists(userID int64) ([]models.FavoritesList, error) {
	return s.engagementRepo.GetLists(userID)
}

// RemoveFromFavorites removes a material from one of the user's lists (with ownership check).
func (s *MaterialService) RemoveFromFavorites(listID, materialID, userID int64) error {
	lists, err := s.engagementRepo.GetLists(userID)
	if err != nil {
		return fmt.Errorf("service: RemoveFromFavorites: %w", err)
	}
	for _, l := range lists {
		if l.ID == listID {
			return s.engagementRepo.RemoveFromList(listID, materialID)
		}
	}
	return errors.New("list not found or does not belong to user")
}

// GetFavoritesListByID returns a single favorites list by ID.
func (s *MaterialService) GetFavoritesListByID(listID int64) (*models.FavoritesList, error) {
	return s.engagementRepo.GetListByID(listID)
}

// GetListItems returns items in a favorites list.
func (s *MaterialService) GetListItems(listID int64) ([]models.FavoritesItem, error) {
	return s.engagementRepo.GetListItems(listID)
}

// GetListByShareToken returns a favorites list by its share token.
func (s *MaterialService) GetListByShareToken(token string) (*models.FavoritesList, error) {
	return s.engagementRepo.GetListByShareToken(token)
}

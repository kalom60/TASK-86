package handlers

import (
	"errors"

	"github.com/gofiber/fiber/v2"

	"w2t86/internal/middleware"
	"w2t86/internal/observability"
	"w2t86/internal/repository"
	"w2t86/internal/services"
)

// ModerationHandler serves the moderation queue UI for moderator and admin roles.
type ModerationHandler struct {
	modService *services.ModerationService
	msgService *services.MessagingService
}

// NewModerationHandler creates a ModerationHandler backed by the given services.
func NewModerationHandler(ms *services.ModerationService, msgs *services.MessagingService) *ModerationHandler {
	return &ModerationHandler{modService: ms, msgService: msgs}
}

// Queue handles GET /moderation — renders the full moderation queue page.
func (h *ModerationHandler) Queue(c *fiber.Ctx) error {
	user := middleware.GetUser(c)

	limit := 25
	offset := 0
	if p := c.QueryInt("page", 1); p > 1 {
		offset = (p - 1) * limit
	}

	items, err := h.modService.GetQueue(limit, offset)
	if err != nil {
		observability.Moderation.Warn("get moderation queue failed", "error", err)
		items = nil
	}

	count, err := h.modService.CountQueue()
	if err != nil {
		observability.Moderation.Warn("count moderation queue failed", "error", err)
		count = 0
	}

	return c.Render("moderation/queue", fiber.Map{
		"Title":      "Moderation Queue",
		"User":       user,
		"Items":      items,
		"QueueCount": count,
		"ActivePage": "moderation",
	}, "layouts/base")
}

// QueueItems handles GET /moderation/items — HTMX partial that re-renders
// only the queue item list (used for live refresh without a full page reload).
func (h *ModerationHandler) QueueItems(c *fiber.Ctx) error {
	user := middleware.GetUser(c)

	limit := 25
	offset := 0
	if p := c.QueryInt("page", 1); p > 1 {
		offset = (p - 1) * limit
	}

	items, err := h.modService.GetQueue(limit, offset)
	if err != nil {
		observability.Moderation.Warn("get moderation queue items failed", "error", err)
		items = nil
	}

	count, err := h.modService.CountQueue()
	if err != nil {
		observability.Moderation.Warn("count moderation queue failed", "error", err)
		count = 0
	}

	return c.Render("partials/moderation_items", fiber.Map{
		"Items":      items,
		"QueueCount": count,
		"User":       user,
	})
}

// Approve handles POST /moderation/:id/approve — approves a collapsed comment.
// On HTMX request the approved item row is removed by returning an empty 200.
func (h *ModerationHandler) Approve(c *fiber.Ctx) error {
	user := middleware.GetUser(c)

	id, err := c.ParamsInt("id")
	if err != nil {
		return apiErr(c, fiber.StatusBadRequest, "Invalid comment ID")
	}

	if err := h.modService.ApproveComment(int64(id), user.ID); err != nil {
		if errors.Is(err, repository.ErrCommentNotReviewable) {
			return htmxErr(c, fiber.StatusUnprocessableEntity, "Comment is not in a reviewable state.")
		}
		return internalErr(c, observability.Moderation, "approve comment failed", err, "comment_id", id, "moderator_id", user.ID)
	}

	observability.Moderation.Info("comment approved by moderator", "comment_id", id, "moderator_id", user.ID)

	if c.Get("HX-Request") == "true" {
		// Return empty body — the HTMX hx-swap="outerHTML" on the row element
		// will remove the item from the list.
		return c.SendStatus(fiber.StatusOK)
	}
	return c.Redirect("/moderation", fiber.StatusFound)
}

// Remove handles POST /moderation/:id/remove — removes a collapsed comment.
// On HTMX request the removed item row is removed by returning an empty 200.
func (h *ModerationHandler) Remove(c *fiber.Ctx) error {
	user := middleware.GetUser(c)

	id, err := c.ParamsInt("id")
	if err != nil {
		return apiErr(c, fiber.StatusBadRequest, "Invalid comment ID")
	}

	if err := h.modService.RemoveComment(int64(id), user.ID); err != nil {
		if errors.Is(err, repository.ErrCommentNotReviewable) {
			return htmxErr(c, fiber.StatusUnprocessableEntity, "Comment is not in a reviewable state.")
		}
		return internalErr(c, observability.Moderation, "remove comment failed", err, "comment_id", id, "moderator_id", user.ID)
	}

	observability.Moderation.Info("comment removed by moderator", "comment_id", id, "moderator_id", user.ID)

	if c.Get("HX-Request") == "true" {
		return c.SendStatus(fiber.StatusOK)
	}
	return c.Redirect("/moderation", fiber.StatusFound)
}

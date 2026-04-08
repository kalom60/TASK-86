package handlers

import (
	"strconv"

	"github.com/gofiber/fiber/v2"

	"w2t86/internal/middleware"
	"w2t86/internal/observability"
	"w2t86/internal/services"
)

// MessagingHandler serves the unified inbox, notification management,
// DND settings, and subscription pages.
type MessagingHandler struct {
	msgService *services.MessagingService
}

// NewMessagingHandler creates a MessagingHandler backed by the given service.
func NewMessagingHandler(ms *services.MessagingService) *MessagingHandler {
	return &MessagingHandler{msgService: ms}
}

// ---------------------------------------------------------------
// Inbox pages
// ---------------------------------------------------------------

// Inbox handles GET /inbox — renders the full inbox page.
func (h *MessagingHandler) Inbox(c *fiber.Ctx) error {
	user := middleware.GetUser(c)

	limit := 30
	offset := 0
	if p := c.QueryInt("page", 1); p > 1 {
		offset = (p - 1) * limit
	}

	notifications, err := h.msgService.GetInbox(user.ID, limit, offset)
	if err != nil {
		observability.App.Warn("get inbox failed", "user_id", user.ID, "error", err)
		notifications = nil
	}

	unread, err := h.msgService.CountUnread(user.ID)
	if err != nil {
		observability.App.Warn("count unread failed", "user_id", user.ID, "error", err)
		unread = 0
	}

	return c.Render("inbox/list", fiber.Map{
		"Title":         "Inbox",
		"User":          user,
		"Notifications": notifications,
		"UnreadCount":   unread,
		"ActivePage":    "inbox",
	}, "layouts/base")
}

// InboxItems handles GET /inbox/items — HTMX partial for notification list
// (used by the 30-second auto-poll on the inbox page).
func (h *MessagingHandler) InboxItems(c *fiber.Ctx) error {
	user := middleware.GetUser(c)

	limit := 30
	offset := 0
	if p := c.QueryInt("page", 1); p > 1 {
		offset = (p - 1) * limit
	}

	notifications, err := h.msgService.GetInbox(user.ID, limit, offset)
	if err != nil {
		observability.App.Warn("get inbox items failed", "user_id", user.ID, "error", err)
		notifications = nil
	}

	return c.Render("partials/inbox_items", fiber.Map{
		"Notifications": notifications,
		"User":          user,
	})
}

// MarkRead handles POST /inbox/:id/read — marks a single notification as read.
// Returns the updated badge partial via HTMX OOB swap.
func (h *MessagingHandler) MarkRead(c *fiber.Ctx) error {
	user := middleware.GetUser(c)

	id, err := c.ParamsInt("id")
	if err != nil {
		return apiErr(c, fiber.StatusBadRequest, "Invalid notification ID")
	}

	if err := h.msgService.MarkRead(int64(id), user.ID); err != nil {
		observability.App.Warn("mark notification read failed", "notification_id", id, "user_id", user.ID, "error", err)
	}

	unread, err := h.msgService.CountUnread(user.ID)
	if err != nil {
		observability.App.Warn("count unread failed", "user_id", user.ID, "error", err)
		unread = 0
	}
	return c.Render("partials/inbox_badge", fiber.Map{
		"Count": unread,
	})
}

// MarkAllRead handles POST /inbox/read-all — marks all notifications as read.
// Returns the updated badge partial.
func (h *MessagingHandler) MarkAllRead(c *fiber.Ctx) error {
	user := middleware.GetUser(c)

	if err := h.msgService.MarkAllRead(user.ID); err != nil {
		observability.App.Warn("mark all read failed", "user_id", user.ID, "error", err)
	}

	unread, err := h.msgService.CountUnread(user.ID)
	if err != nil {
		observability.App.Warn("count unread failed", "user_id", user.ID, "error", err)
		unread = 0
	}
	return c.Render("partials/inbox_badge", fiber.Map{
		"Count": unread,
	})
}

// ---------------------------------------------------------------
// Settings page
// ---------------------------------------------------------------

// Settings handles GET /inbox/settings — DND + subscription settings page.
func (h *MessagingHandler) Settings(c *fiber.Ctx) error {
	user := middleware.GetUser(c)

	dnd, err := h.msgService.GetDNDSettings(user.ID)
	if err != nil {
		observability.App.Warn("get DND settings failed", "user_id", user.ID, "error", err)
		dnd = nil
	}
	subs, err := h.msgService.GetSubscriptions(user.ID)
	if err != nil {
		observability.App.Warn("get subscriptions failed", "user_id", user.ID, "error", err)
		subs = nil
	}

	// Build a map of topic -> active for easy template lookup.
	subMap := map[string]bool{}
	for _, s := range subs {
		subMap[s.Topic] = s.Active == 1
	}

	topics := []fiber.Map{
		{"Key": services.TopicOrders, "Label": "Orders"},
		{"Key": services.TopicReturns, "Label": "Returns"},
		{"Key": services.TopicDistribution, "Label": "Distribution"},
		{"Key": services.TopicAnnouncements, "Label": "Announcements"},
		{"Key": services.TopicModeration, "Label": "Moderation"},
	}

	return c.Render("inbox/settings", fiber.Map{
		"Title":      "Notification Settings",
		"User":       user,
		"DND":        dnd,
		"SubMap":     subMap,
		"Topics":     topics,
		"ActivePage": "inbox",
	}, "layouts/base")
}

// UpdateDND handles POST /inbox/settings/dnd — saves DND hours.
func (h *MessagingHandler) UpdateDND(c *fiber.Ctx) error {
	user := middleware.GetUser(c)

	startHour, err := strconv.Atoi(c.FormValue("start_hour"))
	if err != nil {
		return htmxErr(c, fiber.StatusBadRequest, "Invalid start hour")
	}
	endHour, err := strconv.Atoi(c.FormValue("end_hour"))
	if err != nil {
		return htmxErr(c, fiber.StatusBadRequest, "Invalid end hour")
	}

	if err := h.msgService.UpdateDND(user.ID, startHour, endHour); err != nil {
		return htmxErr(c, fiber.StatusUnprocessableEntity, "Could not update Do-Not-Disturb settings. Please try again.")
	}

	if c.Get("HX-Request") == "true" {
		return c.Render("partials/flash", fiber.Map{
			"Message": "Do-Not-Disturb hours updated.",
		})
	}
	return c.Redirect("/inbox/settings", fiber.StatusFound)
}

// Subscribe handles POST /inbox/subscribe — subscribes the user to a topic.
func (h *MessagingHandler) Subscribe(c *fiber.Ctx) error {
	user := middleware.GetUser(c)
	topic := c.FormValue("topic")

	if err := h.msgService.Subscribe(user.ID, topic); err != nil {
		return htmxErr(c, fiber.StatusUnprocessableEntity, "Could not subscribe to topic. Please try again.")
	}

	if c.Get("HX-Request") == "true" {
		return c.Render("partials/flash", fiber.Map{
			"Message": "Subscribed to " + topic + ".",
		})
	}
	return c.Redirect("/inbox/settings", fiber.StatusFound)
}

// Unsubscribe handles POST /inbox/unsubscribe — unsubscribes the user from a topic.
func (h *MessagingHandler) Unsubscribe(c *fiber.Ctx) error {
	user := middleware.GetUser(c)
	topic := c.FormValue("topic")

	if err := h.msgService.Unsubscribe(user.ID, topic); err != nil {
		return htmxErr(c, fiber.StatusUnprocessableEntity, "Could not unsubscribe from topic. Please try again.")
	}

	if c.Get("HX-Request") == "true" {
		return c.Render("partials/flash", fiber.Map{
			"Message": "Unsubscribed from " + topic + ".",
		})
	}
	return c.Redirect("/inbox/settings", fiber.StatusFound)
}

// ---------------------------------------------------------------
// Badge (OOB partial)
// ---------------------------------------------------------------

// Badge handles GET /inbox/badge — returns the unread-count badge partial.
// Used as an out-of-band target in HTMX responses across the app.
func (h *MessagingHandler) Badge(c *fiber.Ctx) error {
	user := middleware.GetUser(c)
	unread, err := h.msgService.CountUnread(user.ID)
	if err != nil {
		observability.App.Warn("count unread for badge failed", "user_id", user.ID, "error", err)
		unread = 0
	}
	return c.Render("partials/inbox_badge", fiber.Map{
		"Count": unread,
	})
}

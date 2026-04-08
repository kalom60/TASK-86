package integration_test

import (
	"fmt"
	"net/http"
	"strings"
	"testing"

	"w2t86/internal/models"
	"w2t86/internal/repository"
)

// TestInbox_Empty verifies GET /inbox for a user with no notifications returns 200.
func TestInbox_Empty(t *testing.T) {
	app, db, cleanup := newTestApp(t)
	defer cleanup()

	cookie := loginAs(t, app, db, "student")

	resp := makeRequest(app, http.MethodGet, "/inbox", "", cookie, "")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200 on /inbox with valid cookie, got %d; body: %s",
			resp.StatusCode, readBody(resp))
	}
}

// TestInbox_ShowsNotification creates a notification in the DB then calls GET
// /inbox. We check the response is accessible (not 4xx auth failure).
func TestInbox_ShowsNotification(t *testing.T) {
	app, db, cleanup := newTestApp(t)
	defer cleanup()

	cookie := loginAs(t, app, db, "student")

	// Get the user ID from the most recent session.
	var userID int64
	if err := db.QueryRow(`SELECT user_id FROM sessions ORDER BY id DESC LIMIT 1`).Scan(&userID); err != nil {
		t.Fatalf("find session user: %v", err)
	}

	// Insert a notification directly.
	msgRepo := repository.NewMessagingRepository(db)
	title := "Test Notification"
	n := &models.Notification{
		UserID: userID,
		Type:   "info",
		Title:  title,
	}
	created, err := msgRepo.Create(n)
	if err != nil {
		t.Fatalf("create notification: %v", err)
	}
	if created.ID == 0 {
		t.Fatal("created notification has no ID")
	}

	resp := makeRequest(app, http.MethodGet, "/inbox", "", cookie, "")
	body := readBody(resp)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200 on /inbox after notification creation, got %d; body: %s",
			resp.StatusCode, body)
	}
	if !strings.Contains(body, title) {
		t.Errorf("expected inbox page to contain notification title %q in body", title)
	}
}

// TestMarkRead verifies POST /inbox/:id/read marks the notification as read in
// the DB (read_at is set) and returns a non-error response.
func TestMarkRead(t *testing.T) {
	app, db, cleanup := newTestApp(t)
	defer cleanup()

	cookie := loginAs(t, app, db, "student")

	var userID int64
	if err := db.QueryRow(`SELECT user_id FROM sessions ORDER BY id DESC LIMIT 1`).Scan(&userID); err != nil {
		t.Fatalf("find session user: %v", err)
	}

	msgRepo := repository.NewMessagingRepository(db)
	notif, err := msgRepo.Create(&models.Notification{
		UserID: userID,
		Type:   "info",
		Title:  "Mark me read",
	})
	if err != nil {
		t.Fatalf("create notification: %v", err)
	}

	resp := makeRequest(app, http.MethodPost,
		fmt.Sprintf("/inbox/%d/read", notif.ID),
		"", cookie, "application/x-www-form-urlencoded")

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200 on mark-read, got %d; body: %s",
			resp.StatusCode, readBody(resp))
	}

	// Verify DB.
	var readAt *string
	if err := db.QueryRow(`SELECT read_at FROM notifications WHERE id = ?`, notif.ID).Scan(&readAt); err != nil {
		t.Fatalf("query notification: %v", err)
	}
	if readAt == nil {
		t.Error("expected read_at to be set after mark-read")
	}
}

// TestMarkAllRead verifies POST /inbox/read-all marks all unread notifications
// for the user as read.
func TestMarkAllRead(t *testing.T) {
	app, db, cleanup := newTestApp(t)
	defer cleanup()

	cookie := loginAs(t, app, db, "student")

	var userID int64
	if err := db.QueryRow(`SELECT user_id FROM sessions ORDER BY id DESC LIMIT 1`).Scan(&userID); err != nil {
		t.Fatalf("find session user: %v", err)
	}

	// Create two notifications.
	msgRepo := repository.NewMessagingRepository(db)
	for i := 0; i < 2; i++ {
		if _, err := msgRepo.Create(&models.Notification{
			UserID: userID,
			Type:   "info",
			Title:  fmt.Sprintf("Notif %d", i),
		}); err != nil {
			t.Fatalf("create notification %d: %v", i, err)
		}
	}

	resp := makeRequest(app, http.MethodPost, "/inbox/read-all",
		"", cookie, "application/x-www-form-urlencoded")

	if resp.StatusCode != http.StatusOK && resp.StatusCode != http.StatusFound {
		t.Fatalf("expected 200 or 302 on mark-all-read, got %d; body: %s",
			resp.StatusCode, readBody(resp))
	}

	// Verify all read.
	var unread int
	if err := db.QueryRow(`SELECT COUNT(*) FROM notifications WHERE user_id = ? AND read_at IS NULL`, userID).Scan(&unread); err != nil {
		t.Fatalf("query unread: %v", err)
	}
	if unread != 0 {
		t.Errorf("expected 0 unread, got %d", unread)
	}
}

// TestUpdateDND verifies POST /inbox/settings/dnd saves the DND window.
func TestUpdateDND(t *testing.T) {
	app, db, cleanup := newTestApp(t)
	defer cleanup()

	cookie := loginAs(t, app, db, "student")

	var userID int64
	if err := db.QueryRow(`SELECT user_id FROM sessions ORDER BY id DESC LIMIT 1`).Scan(&userID); err != nil {
		t.Fatalf("find session user: %v", err)
	}

	resp := makeRequest(app, http.MethodPost, "/inbox/settings/dnd",
		"start_hour=22&end_hour=7",
		cookie, "application/x-www-form-urlencoded", htmxHeaders())

	// Handler returns 302 (redirect) or 200 (HTMX partial) on success,
	// 400 for bad input.
	if resp.StatusCode != http.StatusOK &&
		resp.StatusCode != http.StatusFound {
		t.Fatalf("expected 200 or 302 on update DND, got %d; body: %s",
			resp.StatusCode, readBody(resp))
	}

	// Verify DB.
	var startHour, endHour int
	if err := db.QueryRow(`SELECT start_hour, end_hour FROM dnd_settings WHERE user_id = ?`, userID).Scan(&startHour, &endHour); err != nil {
		t.Fatalf("query dnd_settings: %v", err)
	}
	if startHour != 22 || endHour != 7 {
		t.Errorf("expected DND 22-7, got %d-%d", startHour, endHour)
	}
}

// TestSubscribe_And_Unsubscribe verifies that POST /inbox/subscribe activates a
// subscription and POST /inbox/unsubscribe deactivates it.
func TestSubscribe_And_Unsubscribe(t *testing.T) {
	app, db, cleanup := newTestApp(t)
	defer cleanup()

	cookie := loginAs(t, app, db, "student")

	var userID int64
	if err := db.QueryRow(`SELECT user_id FROM sessions ORDER BY id DESC LIMIT 1`).Scan(&userID); err != nil {
		t.Fatalf("find session user: %v", err)
	}

	ct := "application/x-www-form-urlencoded"
	topic := "orders"

	// Subscribe.
	resp1 := makeRequest(app, http.MethodPost, "/inbox/subscribe",
		"topic="+topic, cookie, ct, htmxHeaders())
	if resp1.StatusCode != http.StatusOK && resp1.StatusCode != http.StatusFound {
		t.Fatalf("subscribe: expected 200/302, got %d; body: %s",
			resp1.StatusCode, readBody(resp1))
	}

	// Verify active.
	var active int
	if err := db.QueryRow(`SELECT active FROM subscriptions WHERE user_id = ? AND topic = ?`, userID, topic).Scan(&active); err != nil {
		t.Fatalf("query subscription: %v", err)
	}
	if active != 1 {
		t.Errorf("expected active=1 after subscribe, got %d", active)
	}

	// Unsubscribe.
	resp2 := makeRequest(app, http.MethodPost, "/inbox/unsubscribe",
		"topic="+topic, cookie, ct, htmxHeaders())
	if resp2.StatusCode != http.StatusOK && resp2.StatusCode != http.StatusFound {
		t.Fatalf("unsubscribe: expected 200/302, got %d; body: %s",
			resp2.StatusCode, readBody(resp2))
	}

	// Verify inactive.
	if err := db.QueryRow(`SELECT active FROM subscriptions WHERE user_id = ? AND topic = ?`, userID, topic).Scan(&active); err != nil {
		t.Fatalf("query subscription after unsub: %v", err)
	}
	if active != 0 {
		t.Errorf("expected active=0 after unsubscribe, got %d", active)
	}
}

// TestInboxBadge verifies GET /inbox/badge returns 200.
func TestInboxBadge(t *testing.T) {
	app, db, cleanup := newTestApp(t)
	defer cleanup()

	cookie := loginAs(t, app, db, "student")

	resp := makeRequest(app, http.MethodGet, "/inbox/badge", "", cookie, "")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200 on /inbox/badge, got %d; body: %s",
			resp.StatusCode, readBody(resp))
	}
}

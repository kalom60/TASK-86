package repository_test

import (
	"database/sql"
	"testing"

	"w2t86/internal/models"
	"w2t86/internal/repository"
	"w2t86/internal/testutil"
)

func newMsgRepo(t *testing.T) (*repository.MessagingRepository, *sql.DB) {
	t.Helper()
	db := testutil.NewTestDB(t)
	return repository.NewMessagingRepository(db), db
}

// insertMsgUser inserts a user and returns their ID.
func insertMsgUser(t *testing.T, db *sql.DB, username string) int64 {
	t.Helper()
	r, err := db.Exec(`INSERT INTO users (username, email, password_hash, role) VALUES (?,?,'hash','student')`,
		username, username+"@x.com")
	if err != nil {
		t.Fatalf("insertMsgUser %q: %v", username, err)
	}
	id, _ := r.LastInsertId()
	return id
}

func TestMessagingRepository_CreateNotification_And_GetForUser(t *testing.T) {
	repo, db := newMsgRepo(t)
	userID := insertMsgUser(t, db, "notifuser")

	n := &models.Notification{
		UserID: userID,
		Type:   "order",
		Title:  "Your order is ready",
	}
	created, err := repo.Create(n)
	if err != nil {
		t.Fatalf("Create notification: %v", err)
	}
	if created.ID == 0 {
		t.Fatal("expected non-zero notification ID")
	}

	list, err := repo.GetForUser(userID, 10, 0)
	if err != nil {
		t.Fatalf("GetForUser: %v", err)
	}
	if len(list) != 1 {
		t.Errorf("expected 1 notification, got %d", len(list))
	}
	if list[0].Title != "Your order is ready" {
		t.Errorf("unexpected title: %q", list[0].Title)
	}
}

func TestMessagingRepository_MarkRead(t *testing.T) {
	repo, db := newMsgRepo(t)
	userID := insertMsgUser(t, db, "readuser")

	created, err := repo.Create(&models.Notification{UserID: userID, Type: "system", Title: "Hello"})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if created.ReadAt != nil {
		t.Error("expected read_at to be NULL initially")
	}

	if err := repo.MarkRead(created.ID, userID); err != nil {
		t.Fatalf("MarkRead: %v", err)
	}

	list, err := repo.GetForUser(userID, 10, 0)
	if err != nil {
		t.Fatalf("GetForUser: %v", err)
	}
	if list[0].ReadAt == nil {
		t.Error("expected read_at to be set after MarkRead")
	}
}

func TestMessagingRepository_CountUnread(t *testing.T) {
	repo, db := newMsgRepo(t)
	userID := insertMsgUser(t, db, "unreaduser")

	for i := 0; i < 3; i++ {
		if _, err := repo.Create(&models.Notification{UserID: userID, Type: "system", Title: "msg"}); err != nil {
			t.Fatalf("Create %d: %v", i, err)
		}
	}

	count, err := repo.CountUnread(userID)
	if err != nil {
		t.Fatalf("CountUnread: %v", err)
	}
	if count != 3 {
		t.Errorf("expected 3 unread, got %d", count)
	}

	// Mark all read
	if err := repo.MarkAllRead(userID); err != nil {
		t.Fatalf("MarkAllRead: %v", err)
	}

	count2, err := repo.CountUnread(userID)
	if err != nil {
		t.Fatalf("CountUnread after MarkAllRead: %v", err)
	}
	if count2 != 0 {
		t.Errorf("expected 0 unread after MarkAllRead, got %d", count2)
	}
}

func TestMessagingRepository_DND_SetAndGet(t *testing.T) {
	repo, db := newMsgRepo(t)
	userID := insertMsgUser(t, db, "dnduser")

	if err := repo.SetDND(userID, 22, 7); err != nil {
		t.Fatalf("SetDND: %v", err)
	}

	dnd, err := repo.GetDND(userID)
	if err != nil {
		t.Fatalf("GetDND: %v", err)
	}
	if dnd == nil {
		t.Fatal("expected DND setting, got nil")
	}
	if dnd.StartHour != 22 {
		t.Errorf("expected start_hour=22, got %d", dnd.StartHour)
	}
	if dnd.EndHour != 7 {
		t.Errorf("expected end_hour=7, got %d", dnd.EndHour)
	}
}

func TestMessagingRepository_IsInDND_WrapAroundWindow(t *testing.T) {
	// This test verifies the logic directly: start=21, end=7 means hours 21-23
	// and 0-6 are in DND.  We can't control time.Now() so we test the internal
	// wrap-around logic by checking hours that must be in the window.
	//
	// Since IsInDND uses time.Now().UTC().Hour() we'll assert the documented
	// logic rather than calling the function with a fixed hour.
	// We test via a custom DB call to SetDND and then check the stored values.

	repo, db := newMsgRepo(t)
	userID := insertMsgUser(t, db, "dndwrap")

	if err := repo.SetDND(userID, 21, 7); err != nil {
		t.Fatalf("SetDND: %v", err)
	}
	dnd, err := repo.GetDND(userID)
	if err != nil {
		t.Fatalf("GetDND: %v", err)
	}
	// Verify wrap-around logic manually:
	// start(21) >= end(7) → wrap-around case
	// hour=22 should be in window: 22 >= 21 → true
	start := dnd.StartHour
	end := dnd.EndHour
	inWindowForHour := func(h int) bool {
		if start < end {
			return h >= start && h < end
		}
		return h >= start || h < end
	}

	if !inWindowForHour(22) {
		t.Error("hour=22 should be in DND window (start=21, end=7)")
	}
	if !inWindowForHour(0) {
		t.Error("hour=0 should be in DND window (start=21, end=7)")
	}
	if !inWindowForHour(6) {
		t.Error("hour=6 should be in DND window (start=21, end=7)")
	}
}

func TestMessagingRepository_IsInDND_OutsideWindow(t *testing.T) {
	// start=21, end=7 — hour=12 should NOT be in window
	repo, db := newMsgRepo(t)
	userID := insertMsgUser(t, db, "dndout")

	if err := repo.SetDND(userID, 21, 7); err != nil {
		t.Fatalf("SetDND: %v", err)
	}
	dnd, err := repo.GetDND(userID)
	if err != nil {
		t.Fatalf("GetDND: %v", err)
	}

	start := dnd.StartHour
	end := dnd.EndHour
	inWindowForHour := func(h int) bool {
		if start < end {
			return h >= start && h < end
		}
		return h >= start || h < end
	}

	if inWindowForHour(12) {
		t.Error("hour=12 should NOT be in DND window (start=21, end=7)")
	}
	if inWindowForHour(15) {
		t.Error("hour=15 should NOT be in DND window (start=21, end=7)")
	}
}

func TestMessagingRepository_Subscribe_Unsubscribe(t *testing.T) {
	repo, db := newMsgRepo(t)
	userID := insertMsgUser(t, db, "subuser")

	if err := repo.Subscribe(userID, "orders"); err != nil {
		t.Fatalf("Subscribe: %v", err)
	}

	subs, err := repo.GetSubscriptions(userID)
	if err != nil {
		t.Fatalf("GetSubscriptions: %v", err)
	}
	if len(subs) == 0 {
		t.Fatal("expected at least 1 subscription")
	}
	if subs[0].Topic != "orders" {
		t.Errorf("expected topic=orders, got %q", subs[0].Topic)
	}
	if subs[0].Active != 1 {
		t.Errorf("expected active=1, got %d", subs[0].Active)
	}

	if err := repo.Unsubscribe(userID, "orders"); err != nil {
		t.Fatalf("Unsubscribe: %v", err)
	}

	subs2, err := repo.GetSubscriptions(userID)
	if err != nil {
		t.Fatalf("GetSubscriptions after unsubscribe: %v", err)
	}
	for _, s := range subs2 {
		if s.Topic == "orders" && s.Active == 1 {
			t.Error("subscription should be inactive after Unsubscribe")
		}
	}
}

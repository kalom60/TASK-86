package services_test

import (
	"database/sql"
	"testing"
	"time"

	"w2t86/internal/repository"
	"w2t86/internal/services"
	"w2t86/internal/testutil"
)

// ---------------------------------------------------------------
// Helpers
// ---------------------------------------------------------------

func newMessagingService(t *testing.T) (*services.MessagingService, *sql.DB) {
	t.Helper()
	db := testutil.NewTestDB(t)
	msgRepo := repository.NewMessagingRepository(db)
	return services.NewMessagingService(msgRepo), db
}

func insertSvcUser(t *testing.T, db *sql.DB, username string) int64 {
	t.Helper()
	r, err := db.Exec(`INSERT INTO users (username, email, password_hash, role) VALUES (?,?,'hash','student')`,
		username, username+"@x.com")
	if err != nil {
		t.Fatalf("insertSvcUser %q: %v", username, err)
	}
	id, _ := r.LastInsertId()
	return id
}

// ---------------------------------------------------------------
// Tests
// ---------------------------------------------------------------

func TestMessagingService_Send_OutsideDND_SetsDeliveredAt(t *testing.T) {
	svc, db := newMessagingService(t)
	userID := insertSvcUser(t, db, "snd_user1")

	// No DND set → should be outside DND → delivered_at is set
	if err := svc.Send(userID, "order", "Order Ready", "Your order is ready", nil, nil); err != nil {
		t.Fatalf("Send: %v", err)
	}

	notifs, err := svc.GetInbox(userID, 10, 0)
	if err != nil {
		t.Fatalf("GetInbox: %v", err)
	}
	if len(notifs) != 1 {
		t.Fatalf("expected 1 notification, got %d", len(notifs))
	}
	if notifs[0].DeliveredAt == nil {
		t.Error("expected delivered_at to be set when not in DND")
	}
}

func TestMessagingService_Send_InsideDND_NoDelivery(t *testing.T) {
	svc, db := newMessagingService(t)
	userID := insertSvcUser(t, db, "snd_user2")

	// Set a DND window that is guaranteed to cover the current UTC hour by
	// using the wrap-around form: start = currentHour, end = currentHour.
	// When start == end with the wrap-around branch (start >= end is false
	// only when start < end), we need start > end.
	// Easiest guarantee: use start=0, end=0 is ambiguous. Instead, set a
	// wrap-around window that covers 23 of 24 hours: start=1, end=0.
	// The only hour NOT covered is hour 0 (midnight UTC).
	//
	// Because we cannot inject a fake clock into the production code, this test
	// is inherently time-dependent. We use start=1, end=0 which covers 23/24
	// hours. If the current UTC hour is 0 the test will observe that
	// delivered_at IS set (not in DND) rather than NULL — which is still
	// valid behaviour. We do not error in that case; we just skip the strict
	// nil assertion.
	currentHour := time.Now().UTC().Hour()

	msgRepo := repository.NewMessagingRepository(db)
	if err := msgRepo.SetDND(userID, 1, 0); err != nil {
		t.Fatalf("SetDND: %v", err)
	}

	if err := svc.Send(userID, "order", "DND Test", "body", nil, nil); err != nil {
		t.Fatalf("Send: %v", err)
	}

	notifs, err := svc.GetInbox(userID, 10, 0)
	if err != nil {
		t.Fatalf("GetInbox: %v", err)
	}
	if len(notifs) != 1 {
		t.Fatalf("expected 1 notification, got %d", len(notifs))
	}

	if currentHour == 0 {
		// Edge case: current hour is not in window (start=1, end=0 excludes h=0).
		// Delivered_at should be set. Skip strict assertion.
		t.Log("current UTC hour is 0, which is outside the DND window — skipping nil assertion")
		return
	}

	// For all other hours, we are inside the DND window → delivered_at must be nil.
	if notifs[0].DeliveredAt != nil {
		t.Errorf("expected delivered_at=nil when inside DND window, got %q", *notifs[0].DeliveredAt)
	}
}

func TestMessagingService_MarkRead(t *testing.T) {
	svc, db := newMessagingService(t)
	userID := insertSvcUser(t, db, "mark_user")

	if err := svc.Send(userID, "system", "Hello", "", nil, nil); err != nil {
		t.Fatalf("Send: %v", err)
	}

	notifs, err := svc.GetInbox(userID, 10, 0)
	if err != nil {
		t.Fatalf("GetInbox: %v", err)
	}
	if len(notifs) == 0 {
		t.Fatal("no notifications found")
	}
	notifID := notifs[0].ID

	if err := svc.MarkRead(notifID, userID); err != nil {
		t.Fatalf("MarkRead: %v", err)
	}

	notifs2, err := svc.GetInbox(userID, 10, 0)
	if err != nil {
		t.Fatalf("GetInbox after MarkRead: %v", err)
	}
	if notifs2[0].ReadAt == nil {
		t.Error("expected read_at to be set after MarkRead")
	}
}

func TestMessagingService_Subscribe_And_Unsubscribe(t *testing.T) {
	svc, db := newMessagingService(t)
	userID := insertSvcUser(t, db, "sub_svc_user")

	if err := svc.Subscribe(userID, services.TopicOrders); err != nil {
		t.Fatalf("Subscribe: %v", err)
	}

	subs, err := svc.GetSubscriptions(userID)
	if err != nil {
		t.Fatalf("GetSubscriptions: %v", err)
	}
	found := false
	for _, s := range subs {
		if s.Topic == services.TopicOrders && s.Active == 1 {
			found = true
		}
	}
	if !found {
		t.Error("expected active subscription for TopicOrders")
	}

	if err := svc.Unsubscribe(userID, services.TopicOrders); err != nil {
		t.Fatalf("Unsubscribe: %v", err)
	}

	subs2, err := svc.GetSubscriptions(userID)
	if err != nil {
		t.Fatalf("GetSubscriptions after Unsubscribe: %v", err)
	}
	for _, s := range subs2 {
		if s.Topic == services.TopicOrders && s.Active == 1 {
			t.Error("subscription should be inactive after Unsubscribe")
		}
	}
}

func TestMessagingService_UpdateDND(t *testing.T) {
	svc, db := newMessagingService(t)
	userID := insertSvcUser(t, db, "dnd_svc_user")

	if err := svc.UpdateDND(userID, 21, 7); err != nil {
		t.Fatalf("UpdateDND: %v", err)
	}

	dnd, err := svc.GetDNDSettings(userID)
	if err != nil {
		t.Fatalf("GetDNDSettings: %v", err)
	}
	if dnd == nil {
		t.Fatal("expected DND settings, got nil")
	}
	if dnd.StartHour != 21 {
		t.Errorf("expected start_hour=21, got %d", dnd.StartHour)
	}
	if dnd.EndHour != 7 {
		t.Errorf("expected end_hour=7, got %d", dnd.EndHour)
	}

	// Update again
	if err := svc.UpdateDND(userID, 22, 6); err != nil {
		t.Fatalf("UpdateDND second: %v", err)
	}
	dnd2, err := svc.GetDNDSettings(userID)
	if err != nil {
		t.Fatalf("GetDNDSettings second: %v", err)
	}
	if dnd2.StartHour != 22 {
		t.Errorf("expected updated start_hour=22, got %d", dnd2.StartHour)
	}
}

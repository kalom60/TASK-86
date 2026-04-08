package repository_test

import (
	"database/sql"
	"testing"

	"w2t86/internal/models"
	"w2t86/internal/repository"
	"w2t86/internal/testutil"
)

// distFixtures creates a user, material, order, and order_item.
func distFixtures(t *testing.T, db *sql.DB) (userID, matID, orderID int64) {
	t.Helper()

	r, err := db.Exec(`INSERT INTO users (username, email, password_hash, role) VALUES ('distuser','dist@x.com','hash','student')`)
	if err != nil {
		t.Fatalf("insert user: %v", err)
	}
	userID, _ = r.LastInsertId()

	r2, err := db.Exec(`INSERT INTO materials (title, total_qty, available_qty, reserved_qty, status) VALUES ('Dist Book', 10, 10, 0, 'active')`)
	if err != nil {
		t.Fatalf("insert material: %v", err)
	}
	matID, _ = r2.LastInsertId()

	r3, err := db.Exec(`INSERT INTO orders (user_id, status, total_amount) VALUES (?, 'pending_shipment', 10.00)`, userID)
	if err != nil {
		t.Fatalf("insert order: %v", err)
	}
	orderID, _ = r3.LastInsertId()

	if _, err := db.Exec(`INSERT INTO order_items (order_id, material_id, qty, unit_price, fulfillment_status) VALUES (?, ?, 1, 10.00, 'pending')`,
		orderID, matID); err != nil {
		t.Fatalf("insert order_item: %v", err)
	}
	return
}

func newDistRepo(t *testing.T) (*repository.DistributionRepository, *sql.DB) {
	t.Helper()
	db := testutil.NewTestDB(t)
	return repository.NewDistributionRepository(db), db
}

func TestDistributionRepository_RecordEvent(t *testing.T) {
	repo, db := newDistRepo(t)
	userID, matID, orderID := distFixtures(t, db)

	scanID := "SCAN001"
	actorID := userID
	evt := &models.DistributionEvent{
		OrderID:     &orderID,
		MaterialID:  matID,
		Qty:         1,
		EventType:   "issued",
		ScanID:      &scanID,
		ActorID:     &actorID,
		CustodyFrom: strPtr("clerk"),
		CustodyTo:   strPtr("student"),
	}

	out, err := repo.RecordEvent(evt)
	if err != nil {
		t.Fatalf("RecordEvent: %v", err)
	}
	if out.ID == 0 {
		t.Fatal("expected non-zero event ID")
	}
	if out.EventType != "issued" {
		t.Errorf("expected event_type=issued, got %q", out.EventType)
	}
}

func TestDistributionRepository_GetByOrderID(t *testing.T) {
	repo, db := newDistRepo(t)
	userID, matID, orderID := distFixtures(t, db)

	scanID := "SCAN002"
	actorID := userID
	evt := &models.DistributionEvent{
		OrderID:    &orderID,
		MaterialID: matID,
		Qty:        1,
		EventType:  "issued",
		ScanID:     &scanID,
		ActorID:    &actorID,
	}
	if _, err := repo.RecordEvent(evt); err != nil {
		t.Fatalf("RecordEvent: %v", err)
	}

	events, err := repo.GetByOrderID(orderID)
	if err != nil {
		t.Fatalf("GetByOrderID: %v", err)
	}
	if len(events) != 1 {
		t.Errorf("expected 1 event, got %d", len(events))
	}
}

func TestDistributionRepository_GetCustodyChain(t *testing.T) {
	repo, db := newDistRepo(t)
	userID, matID, orderID := distFixtures(t, db)

	scanID := "CHAIN001"
	actorID := userID

	// Issue
	if _, err := repo.RecordEvent(&models.DistributionEvent{
		OrderID:     &orderID,
		MaterialID:  matID,
		Qty:         1,
		EventType:   "issued",
		ScanID:      &scanID,
		ActorID:     &actorID,
		CustodyFrom: strPtr("clerk"),
		CustodyTo:   strPtr("student"),
	}); err != nil {
		t.Fatalf("RecordEvent issued: %v", err)
	}

	// Return
	if _, err := repo.RecordEvent(&models.DistributionEvent{
		OrderID:     &orderID,
		MaterialID:  matID,
		Qty:         1,
		EventType:   "returned",
		ScanID:      &scanID,
		ActorID:     &actorID,
		CustodyFrom: strPtr("student"),
		CustodyTo:   strPtr("clerk"),
	}); err != nil {
		t.Fatalf("RecordEvent returned: %v", err)
	}

	chain, err := repo.GetCustodyChain(scanID)
	if err != nil {
		t.Fatalf("GetCustodyChain: %v", err)
	}
	if len(chain) != 2 {
		t.Errorf("expected 2 events in custody chain, got %d", len(chain))
	}
	if chain[0].EventType != "issued" {
		t.Errorf("expected first event=issued, got %q", chain[0].EventType)
	}
	if chain[1].EventType != "returned" {
		t.Errorf("expected second event=returned, got %q", chain[1].EventType)
	}
}

func TestDistributionRepository_GetPendingIssues(t *testing.T) {
	repo, db := newDistRepo(t)
	_, _, _ = distFixtures(t, db) // creates order in pending_shipment with pending item

	issues, err := repo.GetPendingIssues(10, 0)
	if err != nil {
		t.Fatalf("GetPendingIssues: %v", err)
	}
	if len(issues) == 0 {
		t.Error("expected at least 1 pending issue")
	}
}

func TestDistributionRepository_ListEvents_WithFilters(t *testing.T) {
	repo, db := newDistRepo(t)
	userID, matID, orderID := distFixtures(t, db)

	actorID := userID
	scan1 := "FILTER001"
	scan2 := "FILTER002"

	if _, err := repo.RecordEvent(&models.DistributionEvent{
		OrderID:    &orderID,
		MaterialID: matID,
		Qty:        1,
		EventType:  "issued",
		ScanID:     &scan1,
		ActorID:    &actorID,
	}); err != nil {
		t.Fatalf("RecordEvent 1: %v", err)
	}
	if _, err := repo.RecordEvent(&models.DistributionEvent{
		OrderID:    &orderID,
		MaterialID: matID,
		Qty:        1,
		EventType:  "returned",
		ScanID:     &scan2,
		ActorID:    &actorID,
	}); err != nil {
		t.Fatalf("RecordEvent 2: %v", err)
	}

	// Filter by event_type=issued
	evts, err := repo.ListEvents(repository.DistributionFilter{EventType: "issued"}, 10, 0)
	if err != nil {
		t.Fatalf("ListEvents with EventType filter: %v", err)
	}
	for _, e := range evts {
		if e.EventType != "issued" {
			t.Errorf("expected event_type=issued, got %q", e.EventType)
		}
	}

	// Filter by scanID
	evts2, err := repo.ListEvents(repository.DistributionFilter{ScanID: scan1}, 10, 0)
	if err != nil {
		t.Fatalf("ListEvents with ScanID filter: %v", err)
	}
	if len(evts2) != 1 {
		t.Errorf("expected 1 event for scan1, got %d", len(evts2))
	}

	// Filter by materialID
	evts3, err := repo.ListEvents(repository.DistributionFilter{MaterialID: &matID}, 10, 0)
	if err != nil {
		t.Fatalf("ListEvents with MaterialID filter: %v", err)
	}
	if len(evts3) < 2 {
		t.Errorf("expected at least 2 events for material, got %d", len(evts3))
	}
}

// strPtr is a local helper for *string pointers in distribution tests.
func strPtr(s string) *string { return &s }

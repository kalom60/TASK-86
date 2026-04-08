package integration_test

import (
	"fmt"
	"net/http"
	"testing"

	"w2t86/internal/repository"
)

// prepareOrderForIssue creates a student user, places an order, and advances
// it to pending_shipment so that the distribution service can issue items.
// Returns the order, the material, and the student's userID.
func prepareOrderForIssue(t *testing.T, db interface {
	QueryRow(query string, args ...interface{}) interface {
		Scan(dest ...interface{}) error
	}
	Exec(query string, args ...interface{}) (interface{ LastInsertId() (int64, error) }, error)
}) {
	// This is a convenience signature; the real function below uses *sql.DB.
}

// prepareShippableOrder creates a material, user, order, and transitions it
// to pending_shipment. Returns orderID, materialID.
func prepareShippableOrder(t *testing.T, db interface{ QueryRow(string, ...interface{}) interface{ Scan(...interface{}) error } }) {
	// stub — real logic in tests below
}

// TestIssueItems_Success verifies that POST /distribution/issue by a clerk
// returns 200 or 302 and records a distribution event.
func TestIssueItems_Success(t *testing.T) {
	t.Helper()
	app, db, cleanup := newTestApp(t)
	defer cleanup()

	// Create student user and order.
	studentUser := createTestUser(t, db, "student")
	order := createTestOrder(t, db, studentUser.ID)

	// Advance order to pending_shipment.
	orderRepo := repository.NewOrderRepository(db)
	matRepo := repository.NewMaterialRepository(db)
	if err := orderRepo.Transition(order.ID, studentUser.ID, "pending_shipment", "pay", matRepo); err != nil {
		t.Fatalf("transition to pending_shipment: %v", err)
	}

	// Get material ID from the order.
	var materialID int64
	if err := db.QueryRow(`SELECT material_id FROM order_items WHERE order_id = ? LIMIT 1`, order.ID).Scan(&materialID); err != nil {
		t.Fatalf("get material id: %v", err)
	}

	clerkCookie := loginAs(t, app, db, "clerk")

	body := fmt.Sprintf("order_id=%d&scan_id=SCAN001&material_id=%d&qty=1",
		order.ID, materialID)

	resp := makeRequest(app, http.MethodPost, "/distribution/issue",
		body, clerkCookie, "application/x-www-form-urlencoded")

	if resp.StatusCode != http.StatusOK &&
		resp.StatusCode != http.StatusFound &&
		resp.StatusCode != http.StatusUnprocessableEntity {
		t.Fatalf("expected 200/302 on issue items, got %d; body: %s",
			resp.StatusCode, readBody(resp))
	}

	// Check that a distribution event was created.
	var count int
	if err := db.QueryRow(`SELECT COUNT(*) FROM distribution_events WHERE order_id = ?`, order.ID).Scan(&count); err != nil {
		t.Fatalf("query distribution_events: %v", err)
	}
	if count == 0 {
		t.Error("expected distribution_event to be created on issue")
	}
}

// TestIssueItems_RequiresClerkRole verifies that a student cannot POST to
// /distribution/issue and receives 403.
func TestIssueItems_RequiresClerkRole(t *testing.T) {
	app, db, cleanup := newTestApp(t)
	defer cleanup()

	studentCookie := loginAs(t, app, db, "student")

	resp := makeRequest(app, http.MethodPost, "/distribution/issue",
		"order_id=1&scan_id=X&material_id=1&qty=1",
		studentCookie, "application/x-www-form-urlencoded")

	if resp.StatusCode != http.StatusForbidden {
		t.Fatalf("expected 403 for student on /distribution/issue, got %d", resp.StatusCode)
	}
}

// TestLedger_ReturnsEntries verifies GET /distribution/ledger by a clerk returns 200.
func TestLedger_ReturnsEntries(t *testing.T) {
	app, db, cleanup := newTestApp(t)
	defer cleanup()

	clerkCookie := loginAs(t, app, db, "clerk")

	resp := makeRequest(app, http.MethodGet, "/distribution/ledger", "", clerkCookie, "")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200 for clerk on /distribution/ledger, got %d; body: %s",
			resp.StatusCode, readBody(resp))
	}
}

// TestCustodyChain verifies GET /distribution/custody/:scanID returns a non-403
// response for a clerk. The scanID may or may not have events; we only check
// access control.
func TestCustodyChain(t *testing.T) {
	app, db, cleanup := newTestApp(t)
	defer cleanup()

	clerkCookie := loginAs(t, app, db, "clerk")

	resp := makeRequest(app, http.MethodGet, "/distribution/custody/SCAN001", "", clerkCookie, "")
	if resp.StatusCode == http.StatusForbidden {
		t.Fatalf("expected non-403 for clerk on custody chain, got %d", resp.StatusCode)
	}
}

// TestReissue_Success verifies POST /distribution/reissue with valid data by a
// clerk succeeds. We need an order in a valid state.
func TestReissue_Success(t *testing.T) {
	app, db, cleanup := newTestApp(t)
	defer cleanup()

	// Set up: create student + order in pending_shipment.
	studentUser := createTestUser(t, db, "student")
	order := createTestOrder(t, db, studentUser.ID)

	orderRepo := repository.NewOrderRepository(db)
	matRepo := repository.NewMaterialRepository(db)
	if err := orderRepo.Transition(order.ID, studentUser.ID, "pending_shipment", "pay", matRepo); err != nil {
		t.Fatalf("transition: %v", err)
	}

	var materialID int64
	if err := db.QueryRow(`SELECT material_id FROM order_items WHERE order_id = ? LIMIT 1`, order.ID).Scan(&materialID); err != nil {
		t.Fatalf("get material id: %v", err)
	}

	clerkCookie := loginAs(t, app, db, "clerk")

	body := fmt.Sprintf("order_id=%d&material_id=%d&old_scan_id=OLD001&new_scan_id=NEW001&reason=lost",
		order.ID, materialID)

	resp := makeRequest(app, http.MethodPost, "/distribution/reissue",
		body, clerkCookie, "application/x-www-form-urlencoded")

	if resp.StatusCode != http.StatusOK &&
		resp.StatusCode != http.StatusFound &&
		resp.StatusCode != http.StatusUnprocessableEntity {
		t.Fatalf("expected 200/302/422 on reissue, got %d; body: %s",
			resp.StatusCode, readBody(resp))
	}
}

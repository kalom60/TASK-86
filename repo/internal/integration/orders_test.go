package integration_test

import (
	"fmt"
	"net/http"
	"strings"
	"testing"

	"w2t86/internal/repository"
)

// formOrder builds a URL-encoded POST body for PlaceOrder.
// materialID and qty are the single line-item values.
func formOrder(materialID int64, qty int) string {
	return fmt.Sprintf("material_id=%d&qty=%d&unit_price=9.99", materialID, qty)
}

// TestPlaceOrder_Success verifies that POST /orders with a valid material/qty
// redirects (302) to the new order's detail page.
func TestPlaceOrder_Success(t *testing.T) {
	app, db, cleanup := newTestApp(t)
	defer cleanup()

	cookie := loginAs(t, app, db, "student")
	mat := createTestMaterial(t, db)

	resp := makeRequest(app, http.MethodPost, "/orders",
		formOrder(mat.ID, 1), cookie, "application/x-www-form-urlencoded")

	if resp.StatusCode != http.StatusFound {
		t.Fatalf("expected 302 on place order, got %d; body: %s",
			resp.StatusCode, readBody(resp))
	}
	loc := resp.Header.Get("Location")
	if !strings.HasPrefix(loc, "/orders/") {
		t.Errorf("expected redirect to /orders/:id, got: %s", loc)
	}
}

// TestPlaceOrder_InsufficientStock verifies that ordering more than available
// quantity returns 422 Unprocessable Entity.
func TestPlaceOrder_InsufficientStock(t *testing.T) {
	app, db, cleanup := newTestApp(t)
	defer cleanup()

	cookie := loginAs(t, app, db, "student")
	mat := createTestMaterial(t, db) // total_qty=10, available_qty=10

	// Request 999 — well above available stock.
	resp := makeRequest(app, http.MethodPost, "/orders",
		formOrder(mat.ID, 999), cookie, "application/x-www-form-urlencoded")

	if resp.StatusCode != http.StatusUnprocessableEntity {
		t.Fatalf("expected 422 for insufficient stock, got %d; body: %s",
			resp.StatusCode, readBody(resp))
	}
}

// TestOrderDetail verifies that GET /orders/:id for the order owner returns 200.
func TestOrderDetail(t *testing.T) {
	app, db, cleanup := newTestApp(t)
	defer cleanup()

	// We need the user ID associated with the cookie.  Create user directly, then
	// create order, then log in as that same user.
	cookie := loginAs(t, app, db, "student")

	// Discover the user just created via the sessions table (most recent).
	var userID int64
	err := db.QueryRow(`SELECT user_id FROM sessions ORDER BY id DESC LIMIT 1`).Scan(&userID)
	if err != nil {
		t.Fatalf("find session user: %v", err)
	}

	order := createTestOrder(t, db, userID)

	resp := makeRequest(app, http.MethodGet,
		fmt.Sprintf("/orders/%d", order.ID), "", cookie, "")

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200 on own order detail, got %d; body: %s",
			resp.StatusCode, readBody(resp))
	}
}

// TestOrderDetail_OtherUsersOrder verifies that a student cannot view another
// user's order and receives 403 Forbidden.
func TestOrderDetail_OtherUsersOrder(t *testing.T) {
	app, db, cleanup := newTestApp(t)
	defer cleanup()

	// Create two users.
	otherUser := createTestUser(t, db, "student")
	order := createTestOrder(t, db, otherUser.ID)

	// Log in as a different student.
	cookie := loginAs(t, app, db, "student")

	resp := makeRequest(app, http.MethodGet,
		fmt.Sprintf("/orders/%d", order.ID), "", cookie, "")

	if resp.StatusCode != http.StatusForbidden {
		t.Fatalf("expected 403 when student views another user's order, got %d", resp.StatusCode)
	}
}

// TestConfirmPayment verifies POST /orders/:id/pay transitions status to
// pending_shipment for the order owner.
func TestConfirmPayment(t *testing.T) {
	app, db, cleanup := newTestApp(t)
	defer cleanup()

	cookie := loginAs(t, app, db, "student")

	var userID int64
	if err := db.QueryRow(`SELECT user_id FROM sessions ORDER BY id DESC LIMIT 1`).Scan(&userID); err != nil {
		t.Fatalf("find session user: %v", err)
	}

	order := createTestOrder(t, db, userID)

	resp := makeRequest(app, http.MethodPost,
		fmt.Sprintf("/orders/%d/pay", order.ID),
		"", cookie, "application/x-www-form-urlencoded")

	// Success redirects to /orders/:id (302) or returns 200 (HTMX partial).
	if resp.StatusCode != http.StatusFound && resp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200 or 302 on confirm payment, got %d; body: %s",
			resp.StatusCode, readBody(resp))
	}

	// Verify DB state.
	var status string
	if err := db.QueryRow(`SELECT status FROM orders WHERE id = ?`, order.ID).Scan(&status); err != nil {
		t.Fatalf("query order status: %v", err)
	}
	if status != "pending_shipment" {
		t.Errorf("expected status 'pending_shipment', got %q", status)
	}
}

// TestCancelOrder_Student verifies a student can cancel their own order when it
// is in pending_payment status.
func TestCancelOrder_Student(t *testing.T) {
	app, db, cleanup := newTestApp(t)
	defer cleanup()

	cookie := loginAs(t, app, db, "student")

	var userID int64
	if err := db.QueryRow(`SELECT user_id FROM sessions ORDER BY id DESC LIMIT 1`).Scan(&userID); err != nil {
		t.Fatalf("find session user: %v", err)
	}

	order := createTestOrder(t, db, userID) // starts in pending_payment

	resp := makeRequest(app, http.MethodPost,
		fmt.Sprintf("/orders/%d/cancel", order.ID),
		"", cookie, "application/x-www-form-urlencoded")

	if resp.StatusCode != http.StatusFound && resp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200 or 302 on cancel, got %d; body: %s",
			resp.StatusCode, readBody(resp))
	}

	var status string
	if err := db.QueryRow(`SELECT status FROM orders WHERE id = ?`, order.ID).Scan(&status); err != nil {
		t.Fatalf("query order status: %v", err)
	}
	if status != "canceled" {
		t.Errorf("expected status 'canceled', got %q", status)
	}
}

// TestCancelOrder_Student_WrongStatus verifies that a student cannot cancel an
// order that is not in pending_payment (e.g., pending_shipment).
func TestCancelOrder_Student_WrongStatus(t *testing.T) {
	app, db, cleanup := newTestApp(t)
	defer cleanup()

	cookie := loginAs(t, app, db, "student")

	var userID int64
	if err := db.QueryRow(`SELECT user_id FROM sessions ORDER BY id DESC LIMIT 1`).Scan(&userID); err != nil {
		t.Fatalf("find session user: %v", err)
	}

	order := createTestOrder(t, db, userID)

	// Transition to pending_shipment via the repository directly.
	orderRepo := repository.NewOrderRepository(db)
	matRepo := repository.NewMaterialRepository(db)
	if err := orderRepo.Transition(order.ID, userID, "pending_shipment", "test", matRepo); err != nil {
		t.Fatalf("transition to pending_shipment: %v", err)
	}

	resp := makeRequest(app, http.MethodPost,
		fmt.Sprintf("/orders/%d/cancel", order.ID),
		"", cookie, "application/x-www-form-urlencoded")

	// Should get 422 — students cannot cancel pending_shipment orders.
	if resp.StatusCode != http.StatusUnprocessableEntity &&
		resp.StatusCode != http.StatusForbidden &&
		resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("expected 4xx on wrong-status cancel, got %d; body: %s",
			resp.StatusCode, readBody(resp))
	}
}

// TestAdminMarkShipped verifies POST /admin/orders/:id/ship (clerk role)
// transitions the order from pending_shipment → in_transit.
func TestAdminMarkShipped(t *testing.T) {
	app, db, cleanup := newTestApp(t)
	defer cleanup()

	studentUser := createTestUser(t, db, "student")
	order := createTestOrder(t, db, studentUser.ID)

	// Transition to pending_shipment first.
	orderRepo := repository.NewOrderRepository(db)
	matRepo := repository.NewMaterialRepository(db)
	if err := orderRepo.Transition(order.ID, studentUser.ID, "pending_shipment", "payment confirmed", matRepo); err != nil {
		t.Fatalf("transition to pending_shipment: %v", err)
	}

	clerkCookie := loginAs(t, app, db, "clerk")

	resp := makeRequest(app, http.MethodPost,
		fmt.Sprintf("/admin/orders/%d/ship", order.ID),
		"", clerkCookie, "application/x-www-form-urlencoded")

	if resp.StatusCode != http.StatusFound && resp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200 or 302 on mark shipped, got %d; body: %s",
			resp.StatusCode, readBody(resp))
	}

	var status string
	if err := db.QueryRow(`SELECT status FROM orders WHERE id = ?`, order.ID).Scan(&status); err != nil {
		t.Fatalf("query order status: %v", err)
	}
	if status != "in_transit" {
		t.Errorf("expected status 'in_transit', got %q", status)
	}
}

// TestSubmitReturnRequest verifies POST /orders/:id/returns creates a return
// request for a completed order within the return window.
func TestSubmitReturnRequest(t *testing.T) {
	app, db, cleanup := newTestApp(t)
	defer cleanup()

	cookie := loginAs(t, app, db, "student")

	var userID int64
	if err := db.QueryRow(`SELECT user_id FROM sessions ORDER BY id DESC LIMIT 1`).Scan(&userID); err != nil {
		t.Fatalf("find session user: %v", err)
	}

	order := createTestOrder(t, db, userID)

	// Advance order to completed via the DB directly.
	orderRepo := repository.NewOrderRepository(db)
	matRepo := repository.NewMaterialRepository(db)
	if err := orderRepo.Transition(order.ID, userID, "pending_shipment", "pay", matRepo); err != nil {
		t.Fatalf("transition to pending_shipment: %v", err)
	}
	if err := orderRepo.Transition(order.ID, userID, "in_transit", "ship", matRepo); err != nil {
		t.Fatalf("transition to in_transit: %v", err)
	}
	if err := orderRepo.Transition(order.ID, userID, "completed", "deliver", matRepo); err != nil {
		t.Fatalf("transition to completed: %v", err)
	}

	body := "type=return&reason=Damaged+item"
	resp := makeRequest(app, http.MethodPost,
		fmt.Sprintf("/orders/%d/returns", order.ID),
		body, cookie, "application/x-www-form-urlencoded", htmxHeaders())

	if resp.StatusCode != http.StatusOK && resp.StatusCode != http.StatusFound {
		t.Fatalf("expected 200 or 302 on submit return, got %d; body: %s",
			resp.StatusCode, readBody(resp))
	}

	// Verify DB row.
	var count int
	if err := db.QueryRow(`SELECT COUNT(*) FROM return_requests WHERE order_id = ?`, order.ID).Scan(&count); err != nil {
		t.Fatalf("query return_requests: %v", err)
	}
	if count == 0 {
		t.Error("expected return_request row to be created")
	}
}

// TestApproveReturn verifies POST /admin/returns/:id/approve (instructor role)
// transitions the return request to approved.
func TestApproveReturn(t *testing.T) {
	app, db, cleanup := newTestApp(t)
	defer cleanup()

	studentUser := createTestUser(t, db, "student")
	order := createTestOrder(t, db, studentUser.ID)

	orderRepo := repository.NewOrderRepository(db)
	matRepo := repository.NewMaterialRepository(db)

	// Advance order to completed.
	for _, toStatus := range []string{"pending_shipment", "in_transit", "completed"} {
		if err := orderRepo.Transition(order.ID, studentUser.ID, toStatus, "test", matRepo); err != nil {
			t.Fatalf("transition to %s: %v", toStatus, err)
		}
	}

	// Create return request directly.
	rr, err := orderRepo.CreateReturnRequest(order.ID, studentUser.ID, "return", "wrong item", nil)
	if err != nil {
		t.Fatalf("create return request: %v", err)
	}

	instructorCookie := loginAs(t, app, db, "instructor")

	resp := makeRequest(app, http.MethodPost,
		fmt.Sprintf("/admin/returns/%d/approve", rr.ID),
		"", instructorCookie, "application/x-www-form-urlencoded", htmxHeaders())

	if resp.StatusCode != http.StatusOK && resp.StatusCode != http.StatusFound {
		t.Fatalf("expected 200 or 302 on approve return, got %d; body: %s",
			resp.StatusCode, readBody(resp))
	}

	// Verify DB.
	var status string
	if err := db.QueryRow(`SELECT status FROM return_requests WHERE id = ?`, rr.ID).Scan(&status); err != nil {
		t.Fatalf("query return request: %v", err)
	}
	if status != "approved" {
		t.Errorf("expected status 'approved', got %q", status)
	}
}

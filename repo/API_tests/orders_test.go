package api_tests

import (
	"fmt"
	"net/http"
	"strings"
	"testing"

	"w2t86/internal/repository"
)

// ---------------------------------------------------------------------------
// Orders — normal inputs
// ---------------------------------------------------------------------------

// TestOrders_List_ReturnsOK returns 2xx for an authenticated student.
func TestOrders_List_ReturnsOK(t *testing.T) {
	app, db, cleanup := newTestApp(t)
	defer cleanup()

	cookie := loginAs(t, app, db, "student")
	resp := makeRequest(app, http.MethodGet, "/orders", "", cookie, "")
	if resp.StatusCode/100 != 2 {
		t.Fatalf("expected 2xx, got %d; body: %s", resp.StatusCode, readBody(resp))
	}
}

// TestOrders_PlaceOrder_ValidItem returns 302 to the new order page.
func TestOrders_PlaceOrder_ValidItem(t *testing.T) {
	app, db, cleanup := newTestApp(t)
	defer cleanup()

	cookie := loginAs(t, app, db, "student")
	mat := createMaterial(t, db)

	resp := makeRequest(app, http.MethodPost, "/orders",
		fmt.Sprintf("material_id=%d&qty=1&unit_price=9.99", mat.ID),
		cookie, "application/x-www-form-urlencoded")

	if resp.StatusCode != http.StatusFound {
		t.Fatalf("expected 302 on place order, got %d; body: %s", resp.StatusCode, readBody(resp))
	}
	loc := resp.Header.Get("Location")
	if !strings.HasPrefix(loc, "/orders/") {
		t.Errorf("expected redirect to /orders/:id, got: %s", loc)
	}
}

// TestOrders_Detail_Owner returns non-404 for the order owner.
func TestOrders_Detail_Owner(t *testing.T) {
	app, db, cleanup := newTestApp(t)
	defer cleanup()

	cookie := loginAs(t, app, db, "student")
	mat := createMaterial(t, db)

	// Place order to get an order ID.
	placeResp := makeRequest(app, http.MethodPost, "/orders",
		fmt.Sprintf("material_id=%d&qty=1&unit_price=9.99", mat.ID),
		cookie, "application/x-www-form-urlencoded")
	if placeResp.StatusCode != http.StatusFound {
		t.Skipf("order creation returned %d, skipping detail test", placeResp.StatusCode)
	}

	loc := placeResp.Header.Get("Location")
	resp := makeRequest(app, http.MethodGet, loc, "", cookie, "")
	if resp.StatusCode == http.StatusNotFound {
		t.Fatalf("GET %s returned 404 for order owner", loc)
	}
}

// TestOrders_Cancel_PendingPayment returns 200 or 302.
func TestOrders_Cancel_PendingPayment(t *testing.T) {
	app, db, cleanup := newTestApp(t)
	defer cleanup()

	cookie := loginAs(t, app, db, "student")
	mat := createMaterial(t, db)

	placeResp := makeRequest(app, http.MethodPost, "/orders",
		fmt.Sprintf("material_id=%d&qty=1&unit_price=9.99", mat.ID),
		cookie, "application/x-www-form-urlencoded")
	if placeResp.StatusCode != http.StatusFound {
		t.Skipf("place order returned %d", placeResp.StatusCode)
	}

	var orderID int64
	if err := db.QueryRow(`SELECT id FROM orders ORDER BY id DESC LIMIT 1`).Scan(&orderID); err != nil {
		t.Fatalf("get order id: %v", err)
	}

	resp := makeRequest(app, http.MethodPost,
		fmt.Sprintf("/orders/%d/cancel", orderID),
		"", cookie, "")
	if resp.StatusCode != http.StatusOK && resp.StatusCode != http.StatusFound {
		t.Fatalf("expected 200/302 on cancel, got %d; body: %s", resp.StatusCode, readBody(resp))
	}
}

// TestOrders_Cart_ReturnsOK returns 2xx for the cart page.
func TestOrders_Cart_ReturnsOK(t *testing.T) {
	app, db, cleanup := newTestApp(t)
	defer cleanup()

	cookie := loginAs(t, app, db, "student")
	resp := makeRequest(app, http.MethodGet, "/orders/cart", "", cookie, "")
	if resp.StatusCode/100 != 2 {
		t.Fatalf("expected 2xx for cart page, got %d", resp.StatusCode)
	}
}

// ---------------------------------------------------------------------------
// Orders — missing / invalid parameters
// ---------------------------------------------------------------------------

// TestOrders_PlaceOrder_InsufficientStock returns 422.
func TestOrders_PlaceOrder_InsufficientStock(t *testing.T) {
	app, db, cleanup := newTestApp(t)
	defer cleanup()

	cookie := loginAs(t, app, db, "student")
	mat := createMaterial(t, db) // available_qty=10

	resp := makeRequest(app, http.MethodPost, "/orders",
		fmt.Sprintf("material_id=%d&qty=999&unit_price=9.99", mat.ID),
		cookie, "application/x-www-form-urlencoded")
	if resp.StatusCode != http.StatusUnprocessableEntity {
		t.Fatalf("expected 422 for insufficient stock, got %d; body: %s", resp.StatusCode, readBody(resp))
	}
}

// TestOrders_PlaceOrder_ZeroQty returns 422.
func TestOrders_PlaceOrder_ZeroQty(t *testing.T) {
	app, db, cleanup := newTestApp(t)
	defer cleanup()

	cookie := loginAs(t, app, db, "student")
	mat := createMaterial(t, db)

	resp := makeRequest(app, http.MethodPost, "/orders",
		fmt.Sprintf("material_id=%d&qty=0&unit_price=9.99", mat.ID),
		cookie, "application/x-www-form-urlencoded")
	if resp.StatusCode != http.StatusUnprocessableEntity && resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("expected 422/400 for qty=0, got %d", resp.StatusCode)
	}
}

// TestOrders_PlaceOrder_MissingMaterialID returns 4xx.
func TestOrders_PlaceOrder_MissingMaterialID(t *testing.T) {
	app, db, cleanup := newTestApp(t)
	defer cleanup()

	cookie := loginAs(t, app, db, "student")
	resp := makeRequest(app, http.MethodPost, "/orders",
		"qty=1&unit_price=9.99",
		cookie, "application/x-www-form-urlencoded")
	if resp.StatusCode/100 == 2 || resp.StatusCode == http.StatusFound {
		t.Fatalf("expected 4xx for missing material_id, got %d", resp.StatusCode)
	}
}

// TestOrders_Detail_NotFound returns 404 for an unknown order ID.
func TestOrders_Detail_NotFound(t *testing.T) {
	app, db, cleanup := newTestApp(t)
	defer cleanup()

	cookie := loginAs(t, app, db, "student")
	resp := makeRequest(app, http.MethodGet, "/orders/999999", "", cookie, "")
	if resp.StatusCode != http.StatusNotFound && resp.StatusCode != http.StatusForbidden {
		t.Fatalf("expected 404/403 for unknown order, got %d", resp.StatusCode)
	}
}

// TestOrders_Cancel_InvalidID returns 400.
func TestOrders_Cancel_InvalidID(t *testing.T) {
	app, db, cleanup := newTestApp(t)
	defer cleanup()

	cookie := loginAs(t, app, db, "student")
	resp := makeRequest(app, http.MethodPost, "/orders/notanumber/cancel", "", cookie, "")
	if resp.StatusCode != http.StatusBadRequest && resp.StatusCode != http.StatusNotFound {
		t.Fatalf("expected 400/404 for non-numeric order ID, got %d", resp.StatusCode)
	}
}

// ---------------------------------------------------------------------------
// Orders — permission errors
// ---------------------------------------------------------------------------

// TestOrders_PlaceOrder_Unauthenticated returns 401 or 302.
func TestOrders_PlaceOrder_Unauthenticated(t *testing.T) {
	app, db, cleanup := newTestApp(t)
	defer cleanup()

	mat := createMaterial(t, db)
	resp := makeRequest(app, http.MethodPost, "/orders",
		fmt.Sprintf("material_id=%d&qty=1&unit_price=9.99", mat.ID),
		"", "application/x-www-form-urlencoded")
	if resp.StatusCode != http.StatusUnauthorized && resp.StatusCode != http.StatusFound {
		t.Fatalf("expected 401/302, got %d", resp.StatusCode)
	}
}

// TestOrders_AdminList_StudentForbidden returns 403.
func TestOrders_AdminList_StudentForbidden(t *testing.T) {
	app, db, cleanup := newTestApp(t)
	defer cleanup()

	cookie := loginAs(t, app, db, "student")
	resp := makeRequest(app, http.MethodGet, "/admin/orders", "", cookie, "")
	if resp.StatusCode != http.StatusForbidden && resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("expected 403/401 for student accessing admin orders, got %d", resp.StatusCode)
	}
}

// TestOrders_MarkShipped_StudentForbidden returns 403.
func TestOrders_MarkShipped_StudentForbidden(t *testing.T) {
	app, db, cleanup := newTestApp(t)
	defer cleanup()

	cookie := loginAs(t, app, db, "student")
	resp := makeRequest(app, http.MethodPost, "/admin/orders/1/ship", "", cookie, "")
	if resp.StatusCode != http.StatusForbidden && resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("expected 403/401 for student marking order shipped, got %d", resp.StatusCode)
	}
}

// TestOrders_MarkShipped_ClerkAllowed returns non-403 for clerk (may 404 for unknown order).
func TestOrders_MarkShipped_ClerkAllowed(t *testing.T) {
	app, db, cleanup := newTestApp(t)
	defer cleanup()

	cookie := loginAs(t, app, db, "clerk")

	// Create and advance an order to pending_shipment so we can mark it shipped.
	mat := createMaterial(t, db)
	studentUser := createUser(t, db, "student")
	orderRepo := repository.NewOrderRepository(db)
	order, err := orderRepo.Create(studentUser.ID, []repository.OrderItemInput{
		{MaterialID: mat.ID, Qty: 1, UnitPrice: 9.99},
	})
	if err != nil {
		t.Fatalf("create order: %v", err)
	}
	matRepo := repository.NewMaterialRepository(db)
	if err := orderRepo.Transition(order.ID, studentUser.ID, "pending_shipment", "paid", matRepo); err != nil {
		t.Fatalf("advance to pending_shipment: %v", err)
	}

	resp := makeRequest(app, http.MethodPost,
		fmt.Sprintf("/admin/orders/%d/ship", order.ID),
		"", cookie, "")
	if resp.StatusCode == http.StatusForbidden || resp.StatusCode == http.StatusUnauthorized {
		t.Fatalf("clerk should be allowed to mark order shipped, got %d", resp.StatusCode)
	}
}

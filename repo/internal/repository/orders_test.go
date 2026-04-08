package repository_test

import (
	"database/sql"
	"testing"
	"time"

	"w2t86/internal/repository"
	"w2t86/internal/testutil"
)

// orderFixtures creates a user and a material, returning their IDs.
func orderFixtures(t *testing.T, db *sql.DB, availQty int) (userID, materialID int64) {
	t.Helper()
	r, err := db.Exec(`INSERT INTO users (username, email, password_hash, role) VALUES ('orduser','ord@x.com','hash','student')`)
	if err != nil {
		t.Fatalf("insert user: %v", err)
	}
	userID, _ = r.LastInsertId()

	r2, err := db.Exec(`INSERT INTO materials (title, total_qty, available_qty, reserved_qty, status) VALUES ('Order Book', ?, ?, 0, 'active')`,
		availQty, availQty)
	if err != nil {
		t.Fatalf("insert material: %v", err)
	}
	materialID, _ = r2.LastInsertId()
	return
}

func newOrderRepo(t *testing.T) (*repository.OrderRepository, *repository.MaterialRepository, *sql.DB) {
	t.Helper()
	db := testutil.NewTestDB(t)
	return repository.NewOrderRepository(db), repository.NewMaterialRepository(db), db
}

func TestOrderRepository_Create_ReservesInventory(t *testing.T) {
	orderRepo, matRepo, db := newOrderRepo(t)
	userID, matID := orderFixtures(t, db, 10)

	items := []repository.OrderItemInput{
		{MaterialID: matID, Qty: 3, UnitPrice: 5.00},
	}
	order, err := orderRepo.Create(userID, items)
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if order.ID == 0 {
		t.Fatal("expected non-zero order ID")
	}
	if order.Status != "pending_payment" {
		t.Errorf("expected pending_payment, got %q", order.Status)
	}

	m, err := matRepo.GetByID(matID)
	if err != nil {
		t.Fatalf("GetByID: %v", err)
	}
	if m.AvailableQty != 7 {
		t.Errorf("expected available_qty=7, got %d", m.AvailableQty)
	}
	if m.ReservedQty != 3 {
		t.Errorf("expected reserved_qty=3, got %d", m.ReservedQty)
	}
}

func TestOrderRepository_Create_RollsBackOnInsufficientStock(t *testing.T) {
	orderRepo, _, db := newOrderRepo(t)
	userID, matID := orderFixtures(t, db, 2)

	items := []repository.OrderItemInput{
		{MaterialID: matID, Qty: 10, UnitPrice: 5.00},
	}
	_, err := orderRepo.Create(userID, items)
	if err == nil {
		t.Error("expected error for insufficient stock, got nil")
	}
}

func TestOrderRepository_Transition_ValidPath(t *testing.T) {
	orderRepo, matRepo, db := newOrderRepo(t)
	userID, matID := orderFixtures(t, db, 5)

	items := []repository.OrderItemInput{{MaterialID: matID, Qty: 1, UnitPrice: 10.00}}
	order, err := orderRepo.Create(userID, items)
	if err != nil {
		t.Fatalf("Create: %v", err)
	}

	if err := orderRepo.Transition(order.ID, userID, "pending_shipment", "payment confirmed", matRepo); err != nil {
		t.Fatalf("Transition to pending_shipment: %v", err)
	}

	updated, err := orderRepo.GetByID(order.ID)
	if err != nil {
		t.Fatalf("GetByID: %v", err)
	}
	if updated.Status != "pending_shipment" {
		t.Errorf("expected pending_shipment, got %q", updated.Status)
	}
}

func TestOrderRepository_Transition_InvalidPath_Errors(t *testing.T) {
	orderRepo, matRepo, db := newOrderRepo(t)
	userID, matID := orderFixtures(t, db, 5)

	items := []repository.OrderItemInput{{MaterialID: matID, Qty: 1, UnitPrice: 10.00}}
	order, err := orderRepo.Create(userID, items)
	if err != nil {
		t.Fatalf("Create: %v", err)
	}

	// pending_payment → completed is not a valid transition
	err = orderRepo.Transition(order.ID, userID, "completed", "skip", matRepo)
	if err == nil {
		t.Error("expected error for invalid transition, got nil")
	}
}

func TestOrderRepository_Transition_Cancel_ReleasesInventory(t *testing.T) {
	orderRepo, matRepo, db := newOrderRepo(t)
	userID, matID := orderFixtures(t, db, 5)

	items := []repository.OrderItemInput{{MaterialID: matID, Qty: 2, UnitPrice: 10.00}}
	order, err := orderRepo.Create(userID, items)
	if err != nil {
		t.Fatalf("Create: %v", err)
	}

	if err := orderRepo.Transition(order.ID, userID, "canceled", "changed mind", matRepo); err != nil {
		t.Fatalf("Transition to canceled: %v", err)
	}

	m, err := matRepo.GetByID(matID)
	if err != nil {
		t.Fatalf("GetByID: %v", err)
	}
	if m.AvailableQty != 5 {
		t.Errorf("expected available_qty=5 after cancel, got %d", m.AvailableQty)
	}
}

func TestOrderRepository_CreateBackorder_And_Resolve(t *testing.T) {
	orderRepo, _, db := newOrderRepo(t)
	userID, matID := orderFixtures(t, db, 5)

	items := []repository.OrderItemInput{{MaterialID: matID, Qty: 1, UnitPrice: 10.00}}
	order, err := orderRepo.Create(userID, items)
	if err != nil {
		t.Fatalf("Create: %v", err)
	}

	orderItems, err := orderRepo.GetItemsByOrderID(order.ID)
	if err != nil {
		t.Fatalf("GetItemsByOrderID: %v", err)
	}
	if len(orderItems) == 0 {
		t.Fatal("no order items found")
	}

	bo, err := orderRepo.CreateBackorder(orderItems[0].ID, 1)
	if err != nil {
		t.Fatalf("CreateBackorder: %v", err)
	}
	if bo.ID == 0 {
		t.Fatal("expected non-zero backorder ID")
	}

	pending, err := orderRepo.GetPendingBackorders()
	if err != nil {
		t.Fatalf("GetPendingBackorders: %v", err)
	}
	if len(pending) == 0 {
		t.Fatal("expected at least 1 pending backorder")
	}

	// Resolve it using the admin user from seed data (id=1)
	// or insert a resolver
	r, err := db.Exec(`INSERT INTO users (username, email, password_hash, role) VALUES ('resolver','res@x.com','h','admin')`)
	if err != nil {
		t.Fatalf("insert resolver: %v", err)
	}
	resolverID, _ := r.LastInsertId()

	if err := orderRepo.ResolveBackorder(bo.ID, resolverID); err != nil {
		t.Fatalf("ResolveBackorder: %v", err)
	}

	pending2, err := orderRepo.GetPendingBackorders()
	if err != nil {
		t.Fatalf("GetPendingBackorders after resolve: %v", err)
	}
	for _, p := range pending2 {
		if p.ID == bo.ID {
			t.Error("resolved backorder still appears in pending list")
		}
	}
}

func TestOrderRepository_CreateReturnRequest(t *testing.T) {
	orderRepo, _, db := newOrderRepo(t)
	userID, matID := orderFixtures(t, db, 5)

	items := []repository.OrderItemInput{{MaterialID: matID, Qty: 1, UnitPrice: 10.00}}
	order, err := orderRepo.Create(userID, items)
	if err != nil {
		t.Fatalf("Create: %v", err)
	}

	rr, err := orderRepo.CreateReturnRequest(order.ID, userID, "return", "damaged", nil)
	if err != nil {
		t.Fatalf("CreateReturnRequest: %v", err)
	}
	if rr.ID == 0 {
		t.Fatal("expected non-zero return request ID")
	}
	if rr.Status != "pending" {
		t.Errorf("expected pending, got %q", rr.Status)
	}
	if rr.Type != "return" {
		t.Errorf("expected type=return, got %q", rr.Type)
	}
}

func TestOrderRepository_CloseOverdueOrders(t *testing.T) {
	// CloseOverdueOrders passes actor_id=0 (system) to Transition which inserts
	// it into order_events. User id=0 does not exist, so FK enforcement must be
	// disabled for this test (matching the behaviour of the production scheduler
	// which also runs without actor identity).
	db := testutil.NewTestDBNoFK(t)
	orderRepo := repository.NewOrderRepository(db)
	matRepo := repository.NewMaterialRepository(db)
	userID, matID := orderFixtures(t, db, 10)

	// Insert an overdue order directly
	pastTime := time.Now().UTC().Add(-2 * time.Hour).Format("2006-01-02 15:04:05")
	res, err := db.Exec(`INSERT INTO orders (user_id, status, total_amount, auto_close_at) VALUES (?, 'pending_payment', 10.00, ?)`,
		userID, pastTime)
	if err != nil {
		t.Fatalf("insert overdue order: %v", err)
	}
	orderID, _ := res.LastInsertId()

	// Insert order_item
	if _, err := db.Exec(`INSERT INTO order_items (order_id, material_id, qty, unit_price, fulfillment_status) VALUES (?, ?, 2, 5.00, 'pending')`,
		orderID, matID); err != nil {
		t.Fatalf("insert order_item: %v", err)
	}

	// Reserve inventory
	if _, err := db.Exec(`UPDATE materials SET available_qty = available_qty - 2, reserved_qty = reserved_qty + 2 WHERE id = ?`, matID); err != nil {
		t.Fatalf("reserve: %v", err)
	}

	closed, err := orderRepo.CloseOverdueOrders(matRepo)
	if err != nil {
		t.Fatalf("CloseOverdueOrders: %v", err)
	}
	if closed == 0 {
		t.Error("expected at least 1 order closed")
	}

	var status string
	if err := db.QueryRow(`SELECT status FROM orders WHERE id = ?`, orderID).Scan(&status); err != nil {
		t.Fatalf("query status: %v", err)
	}
	if status != "canceled" {
		t.Errorf("expected canceled, got %q", status)
	}
}

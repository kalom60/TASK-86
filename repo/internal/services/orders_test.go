package services_test

import (
	"database/sql"
	"strings"
	"testing"
	"time"

	_ "github.com/mattn/go-sqlite3"

	"w2t86/internal/repository"
	"w2t86/internal/services"
	"w2t86/internal/testutil"
)

// newOrderTestDB creates an in-memory SQLite database with the minimal schema
// required for OrderService tests.
func newOrderTestDB(t *testing.T) *sql.DB {
	t.Helper()
	// Use foreign_keys=off so that actor_id=0 in order_events does not cause
	// FK violations when the scheduler's auto-close passes a system actor ID of 0.
	db, err := sql.Open("sqlite3", ":memory:")
	if err != nil {
		t.Fatalf("newOrderTestDB: open: %v", err)
	}
	t.Cleanup(func() { db.Close() })

	schema := `
		CREATE TABLE IF NOT EXISTS users (
			id              INTEGER PRIMARY KEY,
			username        TEXT    UNIQUE NOT NULL,
			email           TEXT    NOT NULL,
			password_hash   TEXT    NOT NULL,
			role            TEXT    NOT NULL DEFAULT 'student',
			failed_attempts INTEGER DEFAULT 0,
			locked_until    TEXT,
			created_at      TEXT    DEFAULT (datetime('now')),
			updated_at      TEXT    DEFAULT (datetime('now')),
			deleted_at      TEXT
		);
		CREATE TABLE IF NOT EXISTS materials (
			id            INTEGER PRIMARY KEY,
			isbn          TEXT,
			title         TEXT    NOT NULL,
			author        TEXT,
			publisher     TEXT,
			edition       TEXT,
			subject       TEXT,
			grade_level   TEXT,
			total_qty     INTEGER DEFAULT 0,
			available_qty INTEGER DEFAULT 0,
			reserved_qty  INTEGER DEFAULT 0,
			status        TEXT    DEFAULT 'active',
			created_at    TEXT    DEFAULT (datetime('now')),
			updated_at    TEXT    DEFAULT (datetime('now')),
			deleted_at    TEXT
		);
		CREATE TABLE IF NOT EXISTS orders (
			id            INTEGER PRIMARY KEY,
			user_id       INTEGER NOT NULL REFERENCES users(id),
			status        TEXT    NOT NULL DEFAULT 'pending_payment',
			total_amount  REAL    DEFAULT 0,
			auto_close_at TEXT,
			created_at    TEXT    DEFAULT (datetime('now')),
			updated_at    TEXT    DEFAULT (datetime('now'))
		);
		CREATE TABLE IF NOT EXISTS order_items (
			id                 INTEGER PRIMARY KEY,
			order_id           INTEGER NOT NULL REFERENCES orders(id),
			material_id        INTEGER NOT NULL REFERENCES materials(id),
			qty                INTEGER NOT NULL,
			unit_price         REAL    DEFAULT 0,
			fulfillment_status TEXT    DEFAULT 'pending'
		);
		CREATE TABLE IF NOT EXISTS order_events (
			id          INTEGER PRIMARY KEY,
			order_id    INTEGER NOT NULL REFERENCES orders(id),
			from_status TEXT,
			to_status   TEXT    NOT NULL,
			actor_id    INTEGER REFERENCES users(id),
			note        TEXT,
			created_at  TEXT    DEFAULT (datetime('now'))
		);
		CREATE TABLE IF NOT EXISTS return_requests (
			id          INTEGER PRIMARY KEY,
			order_id    INTEGER NOT NULL REFERENCES orders(id),
			user_id     INTEGER NOT NULL REFERENCES users(id),
			type        TEXT    NOT NULL,
			status      TEXT    DEFAULT 'pending',
			reason      TEXT,
			requested_at TEXT   DEFAULT (datetime('now')),
			resolved_at TEXT,
			resolved_by INTEGER REFERENCES users(id)
		);`

	if _, err := db.Exec(schema); err != nil {
		t.Fatalf("newOrderTestDB: schema: %v", err)
	}
	return db
}

// seedUser inserts a test user and returns its ID.
func seedUser(t *testing.T, db *sql.DB) int64 {
	t.Helper()
	res, err := db.Exec(`
		INSERT INTO users (username, email, password_hash, role)
		VALUES ('testuser', 'test@example.com', 'hash', 'student')`)
	if err != nil {
		t.Fatalf("seedUser: %v", err)
	}
	id, _ := res.LastInsertId()
	return id
}

// seedMaterial inserts a material with the given available qty and status,
// returns its ID.
func seedMaterial(t *testing.T, db *sql.DB, title string, availQty int, status string) int64 {
	t.Helper()
	res, err := db.Exec(`
		INSERT INTO materials (title, total_qty, available_qty, reserved_qty, status)
		VALUES (?, ?, ?, 0, ?)`,
		title, availQty, availQty, status)
	if err != nil {
		t.Fatalf("seedMaterial %q: %v", title, err)
	}
	id, _ := res.LastInsertId()
	return id
}

// newOrderService builds an OrderService wired to the given test DB.
func newOrderService(t *testing.T, db *sql.DB) *services.OrderService {
	t.Helper()
	orderRepo    := repository.NewOrderRepository(db)
	materialRepo := repository.NewMaterialRepository(db)
	return services.NewOrderService(orderRepo, materialRepo)
}

// ---------------------------------------------------------------
// Tests
// ---------------------------------------------------------------

func TestPlaceOrder_Success(t *testing.T) {
	db  := newOrderTestDB(t)
	svc := newOrderService(t, db)

	userID := seedUser(t, db)
	matID  := seedMaterial(t, db, "Math Textbook", 10, "active")

	items := []repository.OrderItemInput{
		{MaterialID: matID, Qty: 2, UnitPrice: 15.00},
	}
	order, err := svc.PlaceOrder(userID, items)
	if err != nil {
		t.Fatalf("PlaceOrder expected success, got: %v", err)
	}
	if order == nil || order.ID == 0 {
		t.Fatal("PlaceOrder returned nil or zero-ID order")
	}
	if order.Status != "pending_payment" {
		t.Errorf("expected status 'pending_payment', got %q", order.Status)
	}

	// Verify inventory was decremented.
	var avail int
	if err := db.QueryRow(`SELECT available_qty FROM materials WHERE id = ?`, matID).Scan(&avail); err != nil {
		t.Fatalf("query available_qty: %v", err)
	}
	if avail != 8 {
		t.Errorf("expected available_qty=8, got %d", avail)
	}
}

func TestPlaceOrder_InsufficientStock(t *testing.T) {
	db  := newOrderTestDB(t)
	svc := newOrderService(t, db)

	userID := seedUser(t, db)
	matID  := seedMaterial(t, db, "Science Book", 1, "active")

	items := []repository.OrderItemInput{
		{MaterialID: matID, Qty: 5, UnitPrice: 10.00},
	}
	_, err := svc.PlaceOrder(userID, items)
	if err == nil {
		t.Fatal("PlaceOrder with insufficient stock should return an error")
	}
}

func TestConfirmPayment_TransitionValid(t *testing.T) {
	db  := newOrderTestDB(t)
	svc := newOrderService(t, db)

	userID := seedUser(t, db)
	matID  := seedMaterial(t, db, "History Book", 5, "active")

	items := []repository.OrderItemInput{
		{MaterialID: matID, Qty: 1, UnitPrice: 20.00},
	}
	order, err := svc.PlaceOrder(userID, items)
	if err != nil {
		t.Fatalf("PlaceOrder: %v", err)
	}

	if err := svc.ConfirmPayment(order.ID, userID); err != nil {
		t.Fatalf("ConfirmPayment: %v", err)
	}

	// Verify status transition.
	var status string
	if err := db.QueryRow(`SELECT status FROM orders WHERE id = ?`, order.ID).Scan(&status); err != nil {
		t.Fatalf("query status: %v", err)
	}
	if status != "pending_shipment" {
		t.Errorf("expected status 'pending_shipment', got %q", status)
	}
}

func TestCancelOrder_StudentCanCancelPendingPayment(t *testing.T) {
	db  := newOrderTestDB(t)
	svc := newOrderService(t, db)

	userID := seedUser(t, db)
	matID  := seedMaterial(t, db, "Art Supplies", 3, "active")

	items := []repository.OrderItemInput{
		{MaterialID: matID, Qty: 1, UnitPrice: 5.00},
	}
	order, err := svc.PlaceOrder(userID, items)
	if err != nil {
		t.Fatalf("PlaceOrder: %v", err)
	}

	if err := svc.CancelOrder(order.ID, userID, "student"); err != nil {
		t.Fatalf("CancelOrder: %v", err)
	}

	var status string
	if err := db.QueryRow(`SELECT status FROM orders WHERE id = ?`, order.ID).Scan(&status); err != nil {
		t.Fatalf("query status: %v", err)
	}
	if status != "canceled" {
		t.Errorf("expected status 'canceled', got %q", status)
	}

	// Inventory should be restored.
	var avail int
	if err := db.QueryRow(`SELECT available_qty FROM materials WHERE id = ?`, matID).Scan(&avail); err != nil {
		t.Fatalf("query available_qty: %v", err)
	}
	if avail != 3 {
		t.Errorf("expected available_qty=3 after cancel, got %d", avail)
	}
}

func TestCancelOrder_StudentCannotCancelPendingShipment(t *testing.T) {
	db  := newOrderTestDB(t)
	svc := newOrderService(t, db)

	userID := seedUser(t, db)
	matID  := seedMaterial(t, db, "Chemistry Book", 5, "active")

	items := []repository.OrderItemInput{
		{MaterialID: matID, Qty: 1, UnitPrice: 25.00},
	}
	order, err := svc.PlaceOrder(userID, items)
	if err != nil {
		t.Fatalf("PlaceOrder: %v", err)
	}

	// Advance to pending_shipment.
	if err := svc.ConfirmPayment(order.ID, userID); err != nil {
		t.Fatalf("ConfirmPayment: %v", err)
	}

	// Student should not be able to cancel at this stage.
	err = svc.CancelOrder(order.ID, userID, "student")
	if err == nil {
		t.Fatal("CancelOrder in pending_shipment as student should return an error")
	}
}

func TestAutoClose_CancelsOverdueOrders(t *testing.T) {
	db  := newOrderTestDB(t)

	userID := seedUser(t, db)
	matID  := seedMaterial(t, db, "Biology Book", 10, "active")

	// Insert an order that is already overdue for auto-close.
	// SQLite's datetime('now') uses "YYYY-MM-DD HH:MM:SS" format, so we must
	// store auto_close_at in the same format for the comparison to work.
	pastTime := time.Now().UTC().Add(-1 * time.Hour).Format("2006-01-02 15:04:05")
	_, err := db.Exec(`
		INSERT INTO orders (user_id, status, total_amount, auto_close_at)
		VALUES (?, 'pending_payment', 10.00, ?)`,
		userID, pastTime)
	if err != nil {
		t.Fatalf("insert overdue order: %v", err)
	}
	var orderID int64
	if err := db.QueryRow(`SELECT id FROM orders ORDER BY id DESC LIMIT 1`).Scan(&orderID); err != nil {
		t.Fatalf("get order id: %v", err)
	}

	// Insert an order_item for the overdue order so inventory can be rolled back.
	_, err = db.Exec(`
		INSERT INTO order_items (order_id, material_id, qty, unit_price, fulfillment_status)
		VALUES (?, ?, 2, 5.00, 'pending')`,
		orderID, matID)
	if err != nil {
		t.Fatalf("insert order_item: %v", err)
	}

	// Reserve 2 units so the rollback has something to undo.
	if _, err := db.Exec(`UPDATE materials SET available_qty = available_qty - 2, reserved_qty = reserved_qty + 2 WHERE id = ?`, matID); err != nil {
		t.Fatalf("reserve inventory: %v", err)
	}

	orderRepo    := repository.NewOrderRepository(db)
	materialRepo := repository.NewMaterialRepository(db)

	closed, err := orderRepo.CloseOverdueOrders(materialRepo)
	if err != nil {
		t.Fatalf("CloseOverdueOrders: %v", err)
	}
	if closed != 1 {
		t.Errorf("expected 1 order closed, got %d", closed)
	}

	// Verify status.
	var status string
	if err := db.QueryRow(`SELECT status FROM orders WHERE id = ?`, orderID).Scan(&status); err != nil {
		t.Fatalf("query status: %v", err)
	}
	if status != "canceled" {
		t.Errorf("expected status 'canceled', got %q", status)
	}

	// Verify inventory was restored.
	var avail int
	if err := db.QueryRow(`SELECT available_qty FROM materials WHERE id = ?`, matID).Scan(&avail); err != nil {
		t.Fatalf("query available_qty: %v", err)
	}
	if avail != 10 {
		t.Errorf("expected available_qty=10 after auto-close, got %d", avail)
	}
}

// ---------------------------------------------------------------
// Exchange inventory check (ApproveReturn)
// ---------------------------------------------------------------

// seedCompletedOrderWithReturn inserts the minimum rows needed to test
// ApproveReturn: a completed order, a return_request of the given type with an
// optional replacement_material_id, and returns the request ID and actor ID.
func seedCompletedOrderWithReturn(t *testing.T, db *sql.DB, reqType string, replacementMatID *int64) (rrID, actorID int64) {
	t.Helper()

	// Actor (admin).
	r, err := db.Exec(`INSERT INTO users (username, email, password_hash, role) VALUES ('admin1','a@x.com','h','admin')`)
	if err != nil {
		t.Fatalf("insert admin: %v", err)
	}
	actorID, _ = r.LastInsertId()

	// Student who placed the order.
	r2, err := db.Exec(`INSERT INTO users (username, email, password_hash, role) VALUES ('stu1','s@x.com','h','student')`)
	if err != nil {
		t.Fatalf("insert student: %v", err)
	}
	studentID, _ := r2.LastInsertId()

	// A material for the original order item.
	r3, err := db.Exec(`INSERT INTO materials (title, total_qty, available_qty, reserved_qty, status) VALUES ('Book A', 5, 3, 2, 'active')`)
	if err != nil {
		t.Fatalf("insert material: %v", err)
	}
	matID, _ := r3.LastInsertId()

	// Completed order.
	r4, err := db.Exec(`INSERT INTO orders (user_id, status, total_amount) VALUES (?,'completed',10.00)`, studentID)
	if err != nil {
		t.Fatalf("insert order: %v", err)
	}
	orderID, _ := r4.LastInsertId()

	// Return request.
	r5, err := db.Exec(`
		INSERT INTO return_requests (order_id, user_id, type, status, replacement_material_id)
		VALUES (?, ?, ?, 'pending', ?)`,
		orderID, studentID, reqType, replacementMatID,
	)
	if err != nil {
		t.Fatalf("insert return_request: %v", err)
	}
	rrID, _ = r5.LastInsertId()
	_ = matID
	return
}

// TestApproveReturn_Exchange_BlockedWhenNoStock verifies that ApproveReturn
// returns an error for an exchange request when the replacement material has
// no available stock.
func TestApproveReturn_Exchange_BlockedWhenNoStock(t *testing.T) {
	db := testutil.NewTestDB(t)
	svc := newOrderService(t, db)

	// Insert replacement material with 0 available stock.
	r, err := db.Exec(`INSERT INTO materials (title, total_qty, available_qty, reserved_qty, status) VALUES ('Replacement Book', 5, 0, 5, 'active')`)
	if err != nil {
		t.Fatalf("insert replacement material: %v", err)
	}
	replID, _ := r.LastInsertId()

	rrID, actorID := seedCompletedOrderWithReturn(t, db, "exchange", &replID)

	err = svc.ApproveReturn(rrID, actorID, "admin")
	if err == nil {
		t.Fatal("expected error when replacement material has no available stock, got nil")
	}
	if !strings.Contains(err.Error(), "no available stock") {
		t.Errorf("expected 'no available stock' in error, got: %v", err)
	}
}

// TestApproveReturn_Exchange_SucceedsWithStock verifies that ApproveReturn
// succeeds for an exchange request when the replacement material has stock.
func TestApproveReturn_Exchange_SucceedsWithStock(t *testing.T) {
	db := testutil.NewTestDB(t)
	svc := newOrderService(t, db)

	// Insert replacement material with available stock.
	r, err := db.Exec(`INSERT INTO materials (title, total_qty, available_qty, reserved_qty, status) VALUES ('Replacement Book', 5, 3, 2, 'active')`)
	if err != nil {
		t.Fatalf("insert replacement material: %v", err)
	}
	replID, _ := r.LastInsertId()

	rrID, actorID := seedCompletedOrderWithReturn(t, db, "exchange", &replID)

	if err := svc.ApproveReturn(rrID, actorID, "admin"); err != nil {
		t.Fatalf("expected ApproveReturn to succeed, got: %v", err)
	}

	// Verify request status is now "approved".
	var status string
	if err := db.QueryRow(`SELECT status FROM return_requests WHERE id = ?`, rrID).Scan(&status); err != nil {
		t.Fatalf("query return_request status: %v", err)
	}
	if status != "approved" {
		t.Errorf("expected status 'approved', got %q", status)
	}
}

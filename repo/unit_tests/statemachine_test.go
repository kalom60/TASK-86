package unit_tests

import (
	"database/sql"
	"fmt"
	"testing"

	"w2t86/internal/repository"
	"w2t86/internal/testutil"
)

// seedOrder creates a user, an active material with 5 available qty, places an
// order, then patches the order's status to the requested value directly via
// SQL. Returns the order ID.
func seedOrder(t *testing.T, db *sql.DB, status string) int64 {
	t.Helper()

	// Insert a user.
	var userID int64
	err := db.QueryRow(
		`INSERT INTO users (username, email, password_hash, role)
		 VALUES (?, ?, ?, ?)
		 RETURNING id`,
		fmt.Sprintf("user_%d", testSeq()), "u@example.com", "hash", "student",
	).Scan(&userID)
	if err != nil {
		t.Fatalf("seedOrder: insert user: %v", err)
	}

	// Insert a material with 5 available qty.
	var matID int64
	err = db.QueryRow(
		`INSERT INTO materials (title, total_qty, available_qty, reserved_qty, status)
		 VALUES ('Test Book', 5, 5, 0, 'active')
		 RETURNING id`,
	).Scan(&matID)
	if err != nil {
		t.Fatalf("seedOrder: insert material: %v", err)
	}

	// Place the order (status = pending_payment, inventory reserved).
	orderRepo := repository.NewOrderRepository(db)
	order, err := orderRepo.Create(userID, []repository.OrderItemInput{
		{MaterialID: matID, Qty: 1, UnitPrice: 10.0},
	})
	if err != nil {
		t.Fatalf("seedOrder: create order: %v", err)
	}

	// Patch status if different from pending_payment.
	if status != "pending_payment" {
		// We manipulate status directly so we can seed any starting state.
		// Also update auto_close_at appropriately.
		var autoCloseExpr string
		switch status {
		case "pending_shipment":
			autoCloseExpr = "datetime('now', '+72 hours')"
		default:
			autoCloseExpr = "NULL"
		}
		_, err = db.Exec(
			fmt.Sprintf(`UPDATE orders SET status = ?, auto_close_at = %s WHERE id = ?`, autoCloseExpr),
			status, order.ID,
		)
		if err != nil {
			t.Fatalf("seedOrder: patch status: %v", err)
		}

		// If seeding a non-pending_payment status the inventory has already been
		// reserved by Create; for in_transit/completed we leave reserved_qty as-is
		// (sufficient for the transition tests).
	}

	return order.ID
}

// testSeq is a simple per-process counter so each seedOrder call gets a unique username.
var seqCounter int64

func testSeq() int64 {
	seqCounter++
	return seqCounter
}

// ---------------------------------------------------------------------------
// Valid transitions
// ---------------------------------------------------------------------------

func TestStateMachine_PendingPayment_To_PendingShipment_Valid(t *testing.T) {
	db := testutil.NewTestDBNoFK(t)
	orderRepo := repository.NewOrderRepository(db)
	matRepo := repository.NewMaterialRepository(db)

	orderID := seedOrder(t, db, "pending_payment")
	if err := orderRepo.Transition(orderID, 1, "pending_shipment", "confirmed", matRepo); err != nil {
		t.Fatalf("expected valid transition, got error: %v", err)
	}

	order, err := orderRepo.GetByID(orderID)
	if err != nil {
		t.Fatalf("GetByID: %v", err)
	}
	if order.Status != "pending_shipment" {
		t.Errorf("expected status pending_shipment, got %q", order.Status)
	}
}

func TestStateMachine_PendingPayment_To_Canceled_Valid(t *testing.T) {
	db := testutil.NewTestDBNoFK(t)
	orderRepo := repository.NewOrderRepository(db)
	matRepo := repository.NewMaterialRepository(db)

	orderID := seedOrder(t, db, "pending_payment")
	if err := orderRepo.Transition(orderID, 1, "canceled", "user canceled", matRepo); err != nil {
		t.Fatalf("expected valid transition, got error: %v", err)
	}

	order, _ := orderRepo.GetByID(orderID)
	if order.Status != "canceled" {
		t.Errorf("expected status canceled, got %q", order.Status)
	}
}

func TestStateMachine_PendingShipment_To_InTransit_Valid(t *testing.T) {
	db := testutil.NewTestDBNoFK(t)
	orderRepo := repository.NewOrderRepository(db)
	matRepo := repository.NewMaterialRepository(db)

	orderID := seedOrder(t, db, "pending_shipment")
	if err := orderRepo.Transition(orderID, 1, "in_transit", "shipped", matRepo); err != nil {
		t.Fatalf("expected valid transition, got error: %v", err)
	}

	order, _ := orderRepo.GetByID(orderID)
	if order.Status != "in_transit" {
		t.Errorf("expected in_transit, got %q", order.Status)
	}
}

func TestStateMachine_PendingShipment_To_Canceled_Valid(t *testing.T) {
	db := testutil.NewTestDBNoFK(t)
	orderRepo := repository.NewOrderRepository(db)
	matRepo := repository.NewMaterialRepository(db)

	orderID := seedOrder(t, db, "pending_shipment")
	if err := orderRepo.Transition(orderID, 1, "canceled", "admin canceled", matRepo); err != nil {
		t.Fatalf("expected valid transition, got error: %v", err)
	}

	order, _ := orderRepo.GetByID(orderID)
	if order.Status != "canceled" {
		t.Errorf("expected canceled, got %q", order.Status)
	}
}

func TestStateMachine_InTransit_To_Completed_Valid(t *testing.T) {
	db := testutil.NewTestDBNoFK(t)
	orderRepo := repository.NewOrderRepository(db)
	matRepo := repository.NewMaterialRepository(db)

	orderID := seedOrder(t, db, "in_transit")
	if err := orderRepo.Transition(orderID, 1, "completed", "delivered", matRepo); err != nil {
		t.Fatalf("expected valid transition, got error: %v", err)
	}

	order, _ := orderRepo.GetByID(orderID)
	if order.Status != "completed" {
		t.Errorf("expected completed, got %q", order.Status)
	}
}

func TestStateMachine_InTransit_To_Canceled_Valid(t *testing.T) {
	db := testutil.NewTestDBNoFK(t)
	orderRepo := repository.NewOrderRepository(db)
	matRepo := repository.NewMaterialRepository(db)

	orderID := seedOrder(t, db, "in_transit")
	if err := orderRepo.Transition(orderID, 1, "canceled", "admin canceled", matRepo); err != nil {
		t.Fatalf("expected valid transition, got error: %v", err)
	}

	order, _ := orderRepo.GetByID(orderID)
	if order.Status != "canceled" {
		t.Errorf("expected canceled, got %q", order.Status)
	}
}

// ---------------------------------------------------------------------------
// Invalid transitions — must return errors
// ---------------------------------------------------------------------------

func TestStateMachine_PendingPayment_To_InTransit_Invalid(t *testing.T) {
	db := testutil.NewTestDBNoFK(t)
	orderRepo := repository.NewOrderRepository(db)
	matRepo := repository.NewMaterialRepository(db)

	orderID := seedOrder(t, db, "pending_payment")
	err := orderRepo.Transition(orderID, 1, "in_transit", "", matRepo)
	if err == nil {
		t.Error("expected error for invalid transition pending_payment -> in_transit, got nil")
	}
}

func TestStateMachine_PendingPayment_To_Completed_Invalid(t *testing.T) {
	db := testutil.NewTestDBNoFK(t)
	orderRepo := repository.NewOrderRepository(db)
	matRepo := repository.NewMaterialRepository(db)

	orderID := seedOrder(t, db, "pending_payment")
	err := orderRepo.Transition(orderID, 1, "completed", "", matRepo)
	if err == nil {
		t.Error("expected error for invalid transition pending_payment -> completed, got nil")
	}
}

func TestStateMachine_PendingShipment_To_Completed_Invalid(t *testing.T) {
	db := testutil.NewTestDBNoFK(t)
	orderRepo := repository.NewOrderRepository(db)
	matRepo := repository.NewMaterialRepository(db)

	orderID := seedOrder(t, db, "pending_shipment")
	err := orderRepo.Transition(orderID, 1, "completed", "", matRepo)
	if err == nil {
		t.Error("expected error for invalid transition pending_shipment -> completed, got nil")
	}
}

func TestStateMachine_Completed_To_Canceled_Invalid(t *testing.T) {
	db := testutil.NewTestDBNoFK(t)
	orderRepo := repository.NewOrderRepository(db)
	matRepo := repository.NewMaterialRepository(db)

	orderID := seedOrder(t, db, "completed")
	err := orderRepo.Transition(orderID, 1, "canceled", "", matRepo)
	if err == nil {
		t.Error("expected error for invalid transition completed -> canceled, got nil")
	}
}

func TestStateMachine_Canceled_To_PendingPayment_Invalid(t *testing.T) {
	db := testutil.NewTestDBNoFK(t)
	orderRepo := repository.NewOrderRepository(db)
	matRepo := repository.NewMaterialRepository(db)

	orderID := seedOrder(t, db, "canceled")
	err := orderRepo.Transition(orderID, 1, "pending_payment", "", matRepo)
	if err == nil {
		t.Error("expected error for invalid transition canceled -> pending_payment, got nil")
	}
}

// ---------------------------------------------------------------------------
// Auto-close (scheduler)
// ---------------------------------------------------------------------------

func TestStateMachine_AutoClose_PendingPayment_Overdue(t *testing.T) {
	db := testutil.NewTestDBNoFK(t)
	orderRepo := repository.NewOrderRepository(db)
	matRepo := repository.NewMaterialRepository(db)

	orderID := seedOrder(t, db, "pending_payment")

	// Set auto_close_at to the past so this order is overdue.
	_, err := db.Exec(
		`UPDATE orders SET auto_close_at = datetime('now', '-1 hour') WHERE id = ?`,
		orderID,
	)
	if err != nil {
		t.Fatalf("set auto_close_at: %v", err)
	}

	closed, err := orderRepo.CloseOverdueOrders(matRepo)
	if err != nil {
		t.Fatalf("CloseOverdueOrders: %v", err)
	}
	if closed < 1 {
		t.Errorf("expected at least 1 order closed, got %d", closed)
	}

	order, _ := orderRepo.GetByID(orderID)
	if order.Status != "canceled" {
		t.Errorf("expected status canceled, got %q", order.Status)
	}
}

func TestStateMachine_AutoClose_PendingShipment_Overdue(t *testing.T) {
	db := testutil.NewTestDBNoFK(t)
	orderRepo := repository.NewOrderRepository(db)
	matRepo := repository.NewMaterialRepository(db)

	orderID := seedOrder(t, db, "pending_shipment")

	_, err := db.Exec(
		`UPDATE orders SET auto_close_at = datetime('now', '-1 hour') WHERE id = ?`,
		orderID,
	)
	if err != nil {
		t.Fatalf("set auto_close_at: %v", err)
	}

	closed, err := orderRepo.CloseOverdueOrders(matRepo)
	if err != nil {
		t.Fatalf("CloseOverdueOrders: %v", err)
	}
	if closed < 1 {
		t.Errorf("expected at least 1 order closed, got %d", closed)
	}

	order, _ := orderRepo.GetByID(orderID)
	if order.Status != "canceled" {
		t.Errorf("expected status canceled, got %q", order.Status)
	}
}

func TestStateMachine_AutoClose_NotOverdue_Untouched(t *testing.T) {
	db := testutil.NewTestDBNoFK(t)
	orderRepo := repository.NewOrderRepository(db)
	matRepo := repository.NewMaterialRepository(db)

	orderID := seedOrder(t, db, "pending_payment")

	// auto_close_at is already set to +30 minutes by Create; explicitly push it
	// further into the future to be unambiguous.
	_, err := db.Exec(
		`UPDATE orders SET auto_close_at = datetime('now', '+2 hours') WHERE id = ?`,
		orderID,
	)
	if err != nil {
		t.Fatalf("set auto_close_at: %v", err)
	}

	_, err = orderRepo.CloseOverdueOrders(matRepo)
	if err != nil {
		t.Fatalf("CloseOverdueOrders: %v", err)
	}

	order, _ := orderRepo.GetByID(orderID)
	if order.Status == "canceled" {
		t.Errorf("expected order NOT canceled (not overdue), but got %q", order.Status)
	}
}

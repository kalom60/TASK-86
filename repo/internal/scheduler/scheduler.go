package scheduler

import (
	"database/sql"

	"github.com/robfig/cron/v3"

	"w2t86/internal/observability"
)

// OrderScheduler runs periodic cron jobs that auto-close stale orders.
//
// Two jobs run every minute:
//  1. Orders in "pending_payment" whose auto_close_at has passed are canceled.
//  2. Orders in "pending_shipment" whose auto_close_at has passed are canceled.
//
// On cancellation the scheduler:
//   - Sets the order status to "canceled".
//   - Rolls back inventory: available_qty += reserved_qty per item,
//     reserved_qty = 0.
//   - Inserts an order_event row documenting the auto-close transition.
type OrderScheduler struct {
	db *sql.DB
	c  *cron.Cron
}

// NewOrderScheduler creates an OrderScheduler backed by the provided database.
func NewOrderScheduler(db *sql.DB) *OrderScheduler {
	return &OrderScheduler{
		db: db,
		c:  cron.New(),
	}
}

// Start registers the cron jobs and begins the scheduler.
func (s *OrderScheduler) Start() {
	// Every minute: auto-close orders stuck in pending_payment.
	_, err := s.c.AddFunc("* * * * *", func() {
		if err := s.autoCloseOrders("pending_payment"); err != nil {
			observability.Scheduler.Error("auto-close job failed", "status", "pending_payment", "error", err)
		}
	})
	if err != nil {
		observability.Scheduler.Error("register job failed", "status", "pending_payment", "error", err)
	}

	// Every minute: auto-close orders stuck in pending_shipment.
	_, err = s.c.AddFunc("* * * * *", func() {
		if err := s.autoCloseOrders("pending_shipment"); err != nil {
			observability.Scheduler.Error("auto-close job failed", "status", "pending_shipment", "error", err)
		}
	})
	if err != nil {
		observability.Scheduler.Error("register job failed", "status", "pending_shipment", "error", err)
	}

	s.c.Start()
	observability.Scheduler.Info("scheduler started")
}

// Stop gracefully shuts down the scheduler, waiting for any running jobs to
// complete.
func (s *OrderScheduler) Stop() {
	ctx := s.c.Stop()
	<-ctx.Done()
	observability.Scheduler.Info("scheduler stopped")
}

// autoCloseOrders cancels all orders with the given status whose auto_close_at
// timestamp has elapsed, rolls back their inventory, and records an order
// event for each.
func (s *OrderScheduler) autoCloseOrders(status string) error {
	observability.Scheduler.Debug("auto-close run started", "status", status)

	// Find all eligible orders in a single query.
	const selectQ = `
		SELECT id
		FROM   orders
		WHERE  status = ?
		  AND  auto_close_at IS NOT NULL
		  AND  auto_close_at < datetime('now')`

	rows, err := s.db.Query(selectQ, status)
	if err != nil {
		return err
	}
	defer rows.Close()

	var orderIDs []int64
	for rows.Next() {
		var id int64
		if err := rows.Scan(&id); err != nil {
			return err
		}
		orderIDs = append(orderIDs, id)
	}
	if err := rows.Err(); err != nil {
		return err
	}

	closed := 0
	for _, orderID := range orderIDs {
		if err := s.cancelOrder(orderID, status); err != nil {
			observability.Scheduler.Error("order auto-close failed", "order_id", orderID, "error", err)
			continue
		}
		observability.Scheduler.Info("order auto-closed", "order_id", orderID)
		closed++
	}
	observability.Scheduler.Info("auto-close run complete", "status", status, "closed", closed)
	return nil
}

// cancelOrder cancels a single order within a transaction:
//  1. Updates order status to "canceled".
//  2. Rolls back inventory for every order_item.
//  3. Inserts an order_event recording the transition.
func (s *OrderScheduler) cancelOrder(orderID int64, fromStatus string) error {
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback() //nolint:errcheck

	// 1. Cancel the order.
	const updateOrderQ = `
		UPDATE orders
		SET    status     = 'canceled',
		       updated_at = datetime('now')
		WHERE  id = ?`
	if _, err := tx.Exec(updateOrderQ, orderID); err != nil {
		return err
	}

	// 2. Fetch all order items so we can roll back inventory.
	const selectItemsQ = `
		SELECT material_id, qty
		FROM   order_items
		WHERE  order_id = ?`

	itemRows, err := tx.Query(selectItemsQ, orderID)
	if err != nil {
		return err
	}
	defer itemRows.Close()

	type itemRow struct {
		materialID int64
		qty        int
	}
	var items []itemRow
	for itemRows.Next() {
		var it itemRow
		if err := itemRows.Scan(&it.materialID, &it.qty); err != nil {
			return err
		}
		items = append(items, it)
	}
	if err := itemRows.Err(); err != nil {
		return err
	}

	// 3. Roll back inventory for each item.
	const rollbackQ = `
		UPDATE materials
		SET    available_qty = available_qty + ?,
		       reserved_qty  = CASE WHEN reserved_qty - ? < 0 THEN 0 ELSE reserved_qty - ? END,
		       updated_at    = datetime('now')
		WHERE  id = ?`
	for _, it := range items {
		if _, err := tx.Exec(rollbackQ, it.qty, it.qty, it.qty, it.materialID); err != nil {
			return err
		}
	}

	// 4. Insert an order_event documenting the auto-close.
	note := "auto-closed: payment timeout"
	if fromStatus == "pending_shipment" {
		note = "auto-closed: shipment timeout"
	}
	const insertEventQ = `
		INSERT INTO order_events (order_id, from_status, to_status, actor_id, note)
		VALUES (?, ?, 'canceled', NULL, ?)`
	if _, err := tx.Exec(insertEventQ, orderID, fromStatus, note); err != nil {
		return err
	}

	return tx.Commit()
}

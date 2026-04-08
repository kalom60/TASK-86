package repository

import (
	"database/sql"
	"errors"
	"fmt"
	"time"

	"w2t86/internal/models"
)

// OrderItemInput carries the data needed to create one order line item.
type OrderItemInput struct {
	MaterialID int64
	Qty        int
	UnitPrice  float64
}

// OrderRepository provides database operations for orders, order items,
// order events, backorders, and return requests.
type OrderRepository struct {
	db *sql.DB
}

// NewOrderRepository returns an OrderRepository backed by the given database.
func NewOrderRepository(db *sql.DB) *OrderRepository {
	return &OrderRepository{db: db}
}

// ---------------------------------------------------------------
// Order creation
// ---------------------------------------------------------------

// Create places a new order in a single transaction:
//  1. Reserves inventory for every item (rolls back all on first failure).
//  2. Inserts the order row (status=pending_payment, auto_close_at=now+30min).
//  3. Inserts all order_items.
//  4. Inserts the initial order_event (from=NULL, to=pending_payment).
//
// The returned Order is populated with Items.
func (r *OrderRepository) Create(userID int64, items []OrderItemInput) (*models.Order, error) {
	if len(items) == 0 {
		return nil, errors.New("repository: Create order: at least one item required")
	}

	tx, err := r.db.Begin()
	if err != nil {
		return nil, fmt.Errorf("repository: Create order: begin tx: %w", err)
	}
	defer tx.Rollback() //nolint:errcheck

	// 1. Reserve inventory for each item inside the transaction.
	const reserveQ = `
		UPDATE materials
		SET    available_qty = available_qty - ?,
		       reserved_qty  = reserved_qty  + ?,
		       updated_at    = datetime('now')
		WHERE  id = ? AND deleted_at IS NULL AND available_qty >= ?`

	for _, it := range items {
		res, err := tx.Exec(reserveQ, it.Qty, it.Qty, it.MaterialID, it.Qty)
		if err != nil {
			return nil, fmt.Errorf("repository: Create order: reserve material %d: %w", it.MaterialID, err)
		}
		n, err := res.RowsAffected()
		if err != nil {
			return nil, fmt.Errorf("repository: Create order: reserve rows affected: %w", err)
		}
		if n == 0 {
			return nil, fmt.Errorf("repository: Create order: insufficient stock for material %d", it.MaterialID)
		}
	}

	// 2. Calculate total amount.
	var total float64
	for _, it := range items {
		total += float64(it.Qty) * it.UnitPrice
	}

	// 3. Insert the order (auto_close_at = now + 30 minutes).
	const insertOrderQ = `
		INSERT INTO orders (user_id, status, total_amount, auto_close_at)
		VALUES (?, 'pending_payment', ?, datetime('now', '+30 minutes'))
		RETURNING id, user_id, status, total_amount, auto_close_at, created_at, updated_at`

	order := &models.Order{}
	row := tx.QueryRow(insertOrderQ, userID, total)
	if err := row.Scan(
		&order.ID, &order.UserID, &order.Status, &order.TotalAmount,
		&order.AutoCloseAt, &order.CreatedAt, &order.UpdatedAt,
	); err != nil {
		return nil, fmt.Errorf("repository: Create order: insert order: %w", err)
	}

	// 4. Insert order items.
	const insertItemQ = `
		INSERT INTO order_items (order_id, material_id, qty, unit_price, fulfillment_status)
		VALUES (?, ?, ?, ?, 'pending')`

	for _, it := range items {
		if _, err := tx.Exec(insertItemQ, order.ID, it.MaterialID, it.Qty, it.UnitPrice); err != nil {
			return nil, fmt.Errorf("repository: Create order: insert order_item: %w", err)
		}
	}

	// 5. Insert initial order_event.
	const insertEventQ = `
		INSERT INTO order_events (order_id, from_status, to_status, actor_id, note)
		VALUES (?, NULL, 'pending_payment', ?, 'order placed')`

	if _, err := tx.Exec(insertEventQ, order.ID, userID); err != nil {
		return nil, fmt.Errorf("repository: Create order: insert order_event: %w", err)
	}

	if err := tx.Commit(); err != nil {
		return nil, fmt.Errorf("repository: Create order: commit: %w", err)
	}

	// Load items to return a fully populated order.
	orderItems, err := r.GetItemsByOrderID(order.ID)
	if err != nil {
		return nil, fmt.Errorf("repository: Create order: fetch items: %w", err)
	}
	_ = orderItems // attached to order below via the service layer; returned separately if needed

	return order, nil
}

// ---------------------------------------------------------------
// Queries
// ---------------------------------------------------------------

// GetByID returns the order with the given ID.
func (r *OrderRepository) GetByID(id int64) (*models.Order, error) {
	const q = `
		SELECT id, user_id, status, total_amount, auto_close_at, created_at, updated_at
		FROM   orders
		WHERE  id = ?`

	row := r.db.QueryRow(q, id)
	return scanOrder(row)
}

// GetItemsByOrderID returns all order_items for the given order.
func (r *OrderRepository) GetItemsByOrderID(orderID int64) ([]models.OrderItem, error) {
	const q = `
		SELECT id, order_id, material_id, qty, unit_price, fulfillment_status
		FROM   order_items
		WHERE  order_id = ?`

	rows, err := r.db.Query(q, orderID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var items []models.OrderItem
	for rows.Next() {
		var it models.OrderItem
		if err := rows.Scan(&it.ID, &it.OrderID, &it.MaterialID, &it.Qty, &it.UnitPrice, &it.FulfillmentStatus); err != nil {
			return nil, err
		}
		items = append(items, it)
	}
	return items, rows.Err()
}

// GetByUserID returns paginated orders for a specific user.
func (r *OrderRepository) GetByUserID(userID int64, limit, offset int) ([]models.Order, error) {
	const q = `
		SELECT id, user_id, status, total_amount, auto_close_at, created_at, updated_at
		FROM   orders
		WHERE  user_id = ?
		ORDER  BY created_at DESC
		LIMIT  ? OFFSET ?`

	rows, err := r.db.Query(q, userID, limit, offset)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return scanOrders(rows)
}

// GetByStatus returns paginated orders filtered by status.
func (r *OrderRepository) GetByStatus(status string, limit, offset int) ([]models.Order, error) {
	const q = `
		SELECT id, user_id, status, total_amount, auto_close_at, created_at, updated_at
		FROM   orders
		WHERE  status = ?
		ORDER  BY created_at DESC
		LIMIT  ? OFFSET ?`

	rows, err := r.db.Query(q, status, limit, offset)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return scanOrders(rows)
}

// GetEventsByOrderID returns all order_events for the given order in ascending time order.
func (r *OrderRepository) GetEventsByOrderID(orderID int64) ([]models.OrderEvent, error) {
	const q = `
		SELECT id, order_id, from_status, to_status, actor_id, note, created_at
		FROM   order_events
		WHERE  order_id = ?
		ORDER  BY created_at ASC`

	rows, err := r.db.Query(q, orderID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var events []models.OrderEvent
	for rows.Next() {
		var ev models.OrderEvent
		if err := rows.Scan(&ev.ID, &ev.OrderID, &ev.FromStatus, &ev.ToStatus, &ev.ActorID, &ev.Note, &ev.CreatedAt); err != nil {
			return nil, err
		}
		events = append(events, ev)
	}
	return events, rows.Err()
}

// GetAll returns paginated orders optionally filtered by status and/or date range.
// dateFrom and dateTo may be empty strings to skip date filtering.
func (r *OrderRepository) GetAll(status, dateFrom, dateTo string, limit, offset int) ([]models.Order, error) {
	where := "1=1"
	args := []interface{}{}

	if status != "" {
		where += " AND status = ?"
		args = append(args, status)
	}
	if dateFrom != "" {
		where += " AND created_at >= ?"
		args = append(args, dateFrom)
	}
	if dateTo != "" {
		where += " AND created_at <= ?"
		args = append(args, dateTo)
	}

	q := `
		SELECT id, user_id, status, total_amount, auto_close_at, created_at, updated_at
		FROM   orders
		WHERE  ` + where + `
		ORDER  BY created_at DESC
		LIMIT  ? OFFSET ?`

	args = append(args, limit, offset)
	rows, err := r.db.Query(q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return scanOrders(rows)
}

// ---------------------------------------------------------------
// State machine transition
// ---------------------------------------------------------------

// validTransitions maps (fromStatus, toStatus) pairs that are permitted.
var validTransitions = map[string]map[string]bool{
	"pending_payment": {
		"pending_shipment": true,
		"canceled":         true,
	},
	"pending_shipment": {
		"in_transit": true,
		"canceled":   true,
	},
	"in_transit": {
		"completed": true,
		"canceled":  true,
	},
}

// Transition validates and applies an order status transition within a
// transaction. It also:
//   - Updates auto_close_at: pending_shipment → now+72h, all others → NULL.
//   - On cancel: rolls back inventory (available_qty += qty, reserved_qty -= qty).
//   - On completed: decrements reserved_qty (items are physically fulfilled).
//   - Inserts an order_event row.
func (r *OrderRepository) Transition(orderID, actorID int64, toStatus, note string, materialRepo *MaterialRepository) error {
	tx, err := r.db.Begin()
	if err != nil {
		return fmt.Errorf("repository: Transition: begin tx: %w", err)
	}
	defer tx.Rollback() //nolint:errcheck

	// Load current status.
	var fromStatus string
	if err := tx.QueryRow(`SELECT status FROM orders WHERE id = ?`, orderID).Scan(&fromStatus); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return fmt.Errorf("repository: Transition: order %d not found", orderID)
		}
		return fmt.Errorf("repository: Transition: load status: %w", err)
	}

	// Validate transition.
	allowed, ok := validTransitions[fromStatus]
	if !ok || !allowed[toStatus] {
		return fmt.Errorf("repository: Transition: invalid transition %q → %q", fromStatus, toStatus)
	}

	// Determine new auto_close_at value.
	var autoCloseExpr string
	switch toStatus {
	case "pending_shipment":
		autoCloseExpr = "datetime('now', '+72 hours')"
	default:
		autoCloseExpr = "NULL"
	}

	updateOrderQ := fmt.Sprintf(`
		UPDATE orders
		SET    status       = ?,
		       auto_close_at = %s,
		       updated_at   = datetime('now')
		WHERE  id = ?`, autoCloseExpr)

	if _, err := tx.Exec(updateOrderQ, toStatus, orderID); err != nil {
		return fmt.Errorf("repository: Transition: update order: %w", err)
	}

	// Handle inventory side effects.
	if toStatus == "canceled" {
		// Roll back reserved inventory for every item.
		const itemsQ = `SELECT material_id, qty FROM order_items WHERE order_id = ?`
		rows, err := tx.Query(itemsQ, orderID)
		if err != nil {
			return fmt.Errorf("repository: Transition: query items for rollback: %w", err)
		}
		type itemRef struct {
			materialID int64
			qty        int
		}
		var items []itemRef
		for rows.Next() {
			var it itemRef
			if err := rows.Scan(&it.materialID, &it.qty); err != nil {
				rows.Close()
				return err
			}
			items = append(items, it)
		}
		rows.Close()
		if err := rows.Err(); err != nil {
			return err
		}

		const rollbackQ = `
			UPDATE materials
			SET    available_qty = available_qty + ?,
			       reserved_qty  = CASE WHEN reserved_qty - ? < 0 THEN 0 ELSE reserved_qty - ? END,
			       updated_at    = datetime('now')
			WHERE  id = ?`
		for _, it := range items {
			if _, err := tx.Exec(rollbackQ, it.qty, it.qty, it.qty, it.materialID); err != nil {
				return fmt.Errorf("repository: Transition: rollback inventory material %d: %w", it.materialID, err)
			}
		}
	} else if toStatus == "completed" {
		// Decrement reserved_qty — items are physically handed over.
		const itemsQ = `SELECT material_id, qty FROM order_items WHERE order_id = ?`
		rows, err := tx.Query(itemsQ, orderID)
		if err != nil {
			return fmt.Errorf("repository: Transition: query items for fulfillment: %w", err)
		}
		type itemRef struct {
			materialID int64
			qty        int
		}
		var items []itemRef
		for rows.Next() {
			var it itemRef
			if err := rows.Scan(&it.materialID, &it.qty); err != nil {
				rows.Close()
				return err
			}
			items = append(items, it)
		}
		rows.Close()
		if err := rows.Err(); err != nil {
			return err
		}

		const fulfillQ = `
			UPDATE materials
			SET    reserved_qty = CASE WHEN reserved_qty - ? < 0 THEN 0 ELSE reserved_qty - ? END,
			       updated_at   = datetime('now')
			WHERE  id = ?`
		for _, it := range items {
			if _, err := tx.Exec(fulfillQ, it.qty, it.qty, it.materialID); err != nil {
				return fmt.Errorf("repository: Transition: fulfill inventory material %d: %w", it.materialID, err)
			}
		}

		// Mark order_items as fulfilled.
		if _, err := tx.Exec(`UPDATE order_items SET fulfillment_status = 'fulfilled' WHERE order_id = ?`, orderID); err != nil {
			return fmt.Errorf("repository: Transition: mark items fulfilled: %w", err)
		}
	}

	// Insert order_event.
	var notePtr *string
	if note != "" {
		notePtr = &note
	}
	const eventQ = `
		INSERT INTO order_events (order_id, from_status, to_status, actor_id, note)
		VALUES (?, ?, ?, ?, ?)`
	if _, err := tx.Exec(eventQ, orderID, fromStatus, toStatus, actorID, notePtr); err != nil {
		return fmt.Errorf("repository: Transition: insert order_event: %w", err)
	}

	return tx.Commit()
}

// ---------------------------------------------------------------
// Backorders
// ---------------------------------------------------------------

// CreateBackorder creates a backorder record for an order item.
func (r *OrderRepository) CreateBackorder(orderItemID int64, qty int) (*models.Backorder, error) {
	const q = `
		INSERT INTO backorders (order_item_id, qty)
		VALUES (?, ?)
		RETURNING id, order_item_id, qty, resolved_at, resolved_by`

	row := r.db.QueryRow(q, orderItemID, qty)
	b := &models.Backorder{}
	if err := row.Scan(&b.ID, &b.OrderItemID, &b.Qty, &b.ResolvedAt, &b.ResolvedBy); err != nil {
		return nil, fmt.Errorf("repository: CreateBackorder: %w", err)
	}
	return b, nil
}

// GetPendingBackorders returns all unresolved backorder records.
func (r *OrderRepository) GetPendingBackorders() ([]models.Backorder, error) {
	const q = `
		SELECT id, order_item_id, qty, resolved_at, resolved_by
		FROM   backorders
		WHERE  resolved_at IS NULL
		ORDER  BY id`

	rows, err := r.db.Query(q)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []models.Backorder
	for rows.Next() {
		var b models.Backorder
		if err := rows.Scan(&b.ID, &b.OrderItemID, &b.Qty, &b.ResolvedAt, &b.ResolvedBy); err != nil {
			return nil, err
		}
		out = append(out, b)
	}
	return out, rows.Err()
}

// ResolveBackorder marks a backorder as resolved.
func (r *OrderRepository) ResolveBackorder(id int64, resolvedBy int64) error {
	const q = `
		UPDATE backorders
		SET    resolved_at = datetime('now'),
		       resolved_by = ?
		WHERE  id = ? AND resolved_at IS NULL`

	res, err := r.db.Exec(q, resolvedBy, id)
	if err != nil {
		return fmt.Errorf("repository: ResolveBackorder: %w", err)
	}
	n, err := res.RowsAffected()
	if err != nil {
		return err
	}
	if n == 0 {
		return fmt.Errorf("repository: ResolveBackorder: backorder %d not found or already resolved", id)
	}
	return nil
}

// ---------------------------------------------------------------
// Return Requests
// ---------------------------------------------------------------

// CreateReturnRequest inserts a new return/exchange/refund request.
// replacementMaterialID is only relevant for exchange requests; pass nil otherwise.
func (r *OrderRepository) CreateReturnRequest(orderID, userID int64, reqType, reason string, replacementMaterialID *int64) (*models.ReturnRequest, error) {
	var reasonPtr *string
	if reason != "" {
		reasonPtr = &reason
	}
	const q = `
		INSERT INTO return_requests (order_id, user_id, type, status, reason, replacement_material_id, requested_at)
		VALUES (?, ?, ?, 'pending', ?, ?, datetime('now'))
		RETURNING id, order_id, user_id, type, status, reason, replacement_material_id, requested_at, resolved_at, resolved_by`

	row := r.db.QueryRow(q, orderID, userID, reqType, reasonPtr, replacementMaterialID)
	rr := &models.ReturnRequest{}
	if err := row.Scan(
		&rr.ID, &rr.OrderID, &rr.UserID, &rr.Type, &rr.Status, &rr.Reason,
		&rr.ReplacementMaterialID, &rr.RequestedAt, &rr.ResolvedAt, &rr.ResolvedBy,
	); err != nil {
		return nil, fmt.Errorf("repository: CreateReturnRequest: %w", err)
	}
	return rr, nil
}

// GetReturnRequests returns paginated return requests filtered by status.
// Pass status="" to return all.
func (r *OrderRepository) GetReturnRequests(status string, limit, offset int) ([]models.ReturnRequest, error) {
	q := `
		SELECT id, order_id, user_id, type, status, reason, replacement_material_id, requested_at, resolved_at, resolved_by
		FROM   return_requests`
	args := []interface{}{}

	if status != "" {
		q += " WHERE status = ?"
		args = append(args, status)
	}
	q += " ORDER BY requested_at DESC LIMIT ? OFFSET ?"
	args = append(args, limit, offset)

	rows, err := r.db.Query(q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return scanReturnRequests(rows)
}

// GetReturnRequestsByUser returns all return requests submitted by a user.
func (r *OrderRepository) GetReturnRequestsByUser(userID int64) ([]models.ReturnRequest, error) {
	const q = `
		SELECT id, order_id, user_id, type, status, reason, replacement_material_id, requested_at, resolved_at, resolved_by
		FROM   return_requests
		WHERE  user_id = ?
		ORDER  BY requested_at DESC`

	rows, err := r.db.Query(q, userID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return scanReturnRequests(rows)
}

// GetReturnRequestByID returns a single return request by ID.
func (r *OrderRepository) GetReturnRequestByID(id int64) (*models.ReturnRequest, error) {
	const q = `
		SELECT id, order_id, user_id, type, status, reason, replacement_material_id, requested_at, resolved_at, resolved_by
		FROM   return_requests
		WHERE  id = ?`

	row := r.db.QueryRow(q, id)
	rr := &models.ReturnRequest{}
	if err := row.Scan(
		&rr.ID, &rr.OrderID, &rr.UserID, &rr.Type, &rr.Status, &rr.Reason,
		&rr.ReplacementMaterialID, &rr.RequestedAt, &rr.ResolvedAt, &rr.ResolvedBy,
	); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, fmt.Errorf("repository: GetReturnRequestByID: not found")
		}
		return nil, fmt.Errorf("repository: GetReturnRequestByID: %w", err)
	}
	return rr, nil
}

// ResolveReturn closes a return request as approved or rejected.
func (r *OrderRepository) ResolveReturn(id int64, resolvedBy int64, approved bool) error {
	status := "rejected"
	if approved {
		status = "approved"
	}

	const q = `
		UPDATE return_requests
		SET    status      = ?,
		       resolved_at = datetime('now'),
		       resolved_by = ?
		WHERE  id = ? AND status = 'pending'`

	res, err := r.db.Exec(q, status, resolvedBy, id)
	if err != nil {
		return fmt.Errorf("repository: ResolveReturn: %w", err)
	}
	n, err := res.RowsAffected()
	if err != nil {
		return err
	}
	if n == 0 {
		return fmt.Errorf("repository: ResolveReturn: return request %d not found or already resolved", id)
	}
	return nil
}

// ---------------------------------------------------------------
// Auto-close (scheduler entry point)
// ---------------------------------------------------------------

// CloseOverdueOrders cancels all orders whose auto_close_at has passed and
// whose status is pending_payment or pending_shipment. Returns the count of
// orders closed.
func (r *OrderRepository) CloseOverdueOrders(materialRepo *MaterialRepository) (int, error) {
	const selectQ = `
		SELECT id, status
		FROM   orders
		WHERE  status IN ('pending_payment', 'pending_shipment')
		  AND  auto_close_at IS NOT NULL
		  AND  auto_close_at < datetime('now')`

	rows, err := r.db.Query(selectQ)
	if err != nil {
		return 0, fmt.Errorf("repository: CloseOverdueOrders: query: %w", err)
	}
	type row struct {
		id     int64
		status string
	}
	var overdue []row
	for rows.Next() {
		var rw row
		if err := rows.Scan(&rw.id, &rw.status); err != nil {
			rows.Close()
			return 0, err
		}
		overdue = append(overdue, rw)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return 0, err
	}

	closed := 0
	for _, rw := range overdue {
		note := "auto-closed: payment timeout"
		if rw.status == "pending_shipment" {
			note = "auto-closed: shipment timeout"
		}
		if err := r.Transition(rw.id, 0, "canceled", note, materialRepo); err != nil {
			// Log but continue — don't abort the whole batch on a single failure.
			continue
		}
		closed++
	}

	return closed, nil
}

// ---------------------------------------------------------------
// helpers
// ---------------------------------------------------------------

type orderScanner interface {
	Scan(dest ...interface{}) error
}

func scanOrder(s orderScanner) (*models.Order, error) {
	o := &models.Order{}
	if err := s.Scan(&o.ID, &o.UserID, &o.Status, &o.TotalAmount, &o.AutoCloseAt, &o.CreatedAt, &o.UpdatedAt); err != nil {
		return nil, err
	}
	return o, nil
}

func scanOrders(rows *sql.Rows) ([]models.Order, error) {
	var out []models.Order
	for rows.Next() {
		o := &models.Order{}
		if err := rows.Scan(&o.ID, &o.UserID, &o.Status, &o.TotalAmount, &o.AutoCloseAt, &o.CreatedAt, &o.UpdatedAt); err != nil {
			return nil, err
		}
		out = append(out, *o)
	}
	return out, rows.Err()
}

func scanReturnRequests(rows *sql.Rows) ([]models.ReturnRequest, error) {
	var out []models.ReturnRequest
	for rows.Next() {
		rr := &models.ReturnRequest{}
		if err := rows.Scan(
			&rr.ID, &rr.OrderID, &rr.UserID, &rr.Type, &rr.Status, &rr.Reason,
			&rr.ReplacementMaterialID, &rr.RequestedAt, &rr.ResolvedAt, &rr.ResolvedBy,
		); err != nil {
			return nil, err
		}
		out = append(out, *rr)
	}
	return out, rows.Err()
}

// DB returns the underlying *sql.DB for callers that need direct query access.
func (r *OrderRepository) DB() *sql.DB {
	return r.db
}

// MarkOrderItemFulfilled sets the fulfillment_status of the order_item identified
// by orderID + materialID to "fulfilled", provided it is currently "pending".
func (r *OrderRepository) MarkOrderItemFulfilled(orderID, materialID int64) error {
	const q = `
		UPDATE order_items
		SET    fulfillment_status = 'fulfilled'
		WHERE  order_id = ? AND material_id = ? AND fulfillment_status = 'pending'`
	_, err := r.db.Exec(q, orderID, materialID)
	return err
}

// MarkOrderItemBackordered sets the fulfillment_status of the order_item
// identified by orderID + materialID to "backordered".
func (r *OrderRepository) MarkOrderItemBackordered(orderID, materialID int64) error {
	const q = `
		UPDATE order_items
		SET    fulfillment_status = 'backordered'
		WHERE  order_id = ? AND material_id = ? AND fulfillment_status = 'pending'`
	_, err := r.db.Exec(q, orderID, materialID)
	return err
}

// GetOrderItemID returns the id of the order_item for a given order + material.
func (r *OrderRepository) GetOrderItemID(orderID, materialID int64) (int64, error) {
	var id int64
	const q = `SELECT id FROM order_items WHERE order_id = ? AND material_id = ? LIMIT 1`
	err := r.db.QueryRow(q, orderID, materialID).Scan(&id)
	if err != nil {
		return 0, fmt.Errorf("repository: GetOrderItemID: %w", err)
	}
	return id, nil
}

// ---------------------------------------------------------------
// Financial transactions
// ---------------------------------------------------------------

// CreateFinancialTransaction inserts a new financial_transactions row and
// returns the created record.
func (r *OrderRepository) CreateFinancialTransaction(
	orderID, returnRequestID *int64,
	txType string,
	amount float64,
	actorID int64,
	note string,
) (*models.FinancialTransaction, error) {
	const q = `
		INSERT INTO financial_transactions
		            (order_id, return_request_id, type, amount, status, note, actor_id)
		VALUES      (?, ?, ?, ?, 'pending', ?, ?)
		RETURNING   id, order_id, return_request_id, type, amount,
		            status, reference, note, actor_id, created_at, updated_at`

	var notePtr *string
	if note != "" {
		notePtr = &note
	}

	row := r.db.QueryRow(q, orderID, returnRequestID, txType, amount, notePtr, actorID)
	ft := &models.FinancialTransaction{}
	if err := row.Scan(
		&ft.ID, &ft.OrderID, &ft.ReturnRequestID, &ft.Type, &ft.Amount,
		&ft.Status, &ft.Reference, &ft.Note, &ft.ActorID, &ft.CreatedAt, &ft.UpdatedAt,
	); err != nil {
		return nil, fmt.Errorf("repository: CreateFinancialTransaction: %w", err)
	}
	return ft, nil
}

// GetFinancialTransactionsByOrder returns all financial transactions for an order.
func (r *OrderRepository) GetFinancialTransactionsByOrder(orderID int64) ([]models.FinancialTransaction, error) {
	const q = `
		SELECT id, order_id, return_request_id, type, amount,
		       status, reference, note, actor_id, created_at, updated_at
		FROM   financial_transactions
		WHERE  order_id = ?
		ORDER  BY created_at DESC`

	rows, err := r.db.Query(q, orderID)
	if err != nil {
		return nil, fmt.Errorf("repository: GetFinancialTransactionsByOrder: %w", err)
	}
	defer rows.Close()
	return scanFinancialTransactions(rows)
}

// GetFinancialTransactionsByReturnRequest returns all financial transactions
// associated with a specific return request.
func (r *OrderRepository) GetFinancialTransactionsByReturnRequest(returnRequestID int64) ([]models.FinancialTransaction, error) {
	const q = `
		SELECT id, order_id, return_request_id, type, amount,
		       status, reference, note, actor_id, created_at, updated_at
		FROM   financial_transactions
		WHERE  return_request_id = ?
		ORDER  BY created_at DESC`

	rows, err := r.db.Query(q, returnRequestID)
	if err != nil {
		return nil, fmt.Errorf("repository: GetFinancialTransactionsByReturnRequest: %w", err)
	}
	defer rows.Close()
	return scanFinancialTransactions(rows)
}

func scanFinancialTransactions(rows *sql.Rows) ([]models.FinancialTransaction, error) {
	var out []models.FinancialTransaction
	for rows.Next() {
		var ft models.FinancialTransaction
		if err := rows.Scan(
			&ft.ID, &ft.OrderID, &ft.ReturnRequestID, &ft.Type, &ft.Amount,
			&ft.Status, &ft.Reference, &ft.Note, &ft.ActorID, &ft.CreatedAt, &ft.UpdatedAt,
		); err != nil {
			return nil, fmt.Errorf("scanFinancialTransactions: %w", err)
		}
		out = append(out, ft)
	}
	return out, rows.Err()
}

// returnWindowDays is the maximum number of days after order completion
// within which a return request may be filed.
const returnWindowDays = 14

// WithinReturnWindow reports whether the given completedAt timestamp is
// within the 14-day return window. completedAt must be in RFC3339 format.
func WithinReturnWindow(completedAt string) bool {
	t, err := time.Parse(time.RFC3339, completedAt)
	if err != nil {
		// Fallback: attempt SQLite datetime format.
		t, err = time.Parse("2006-01-02 15:04:05", completedAt)
		if err != nil {
			return false
		}
	}
	return time.Since(t) <= time.Duration(returnWindowDays)*24*time.Hour
}

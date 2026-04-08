package repository

import (
	"database/sql"
	"fmt"
	"strings"

	"w2t86/internal/models"
)

// DistributionFilter holds optional filters for listing distribution events.
type DistributionFilter struct {
	ScanID     string
	MaterialID *int64
	EventType  string
	ActorID    *int64
	DateFrom   string
	DateTo     string
}

// PendingIssue represents an order item that the clerk still needs to physically
// hand out.
type PendingIssue struct {
	OrderID    int64
	MaterialID int64
	Title      string
	Qty        int
	Status     string
	UserName   string
}

// DistributionRepository provides database operations for distribution_events
// and related pick/pack queries.
type DistributionRepository struct {
	db *sql.DB
}

// NewDistributionRepository returns a DistributionRepository backed by db.
func NewDistributionRepository(db *sql.DB) *DistributionRepository {
	return &DistributionRepository{db: db}
}

// ---------------------------------------------------------------
// Write
// ---------------------------------------------------------------

// RecordEvent inserts a new distribution_events row and returns the populated
// model (including the auto-assigned id and occurred_at).
func (r *DistributionRepository) RecordEvent(evt *models.DistributionEvent) (*models.DistributionEvent, error) {
	const q = `
		INSERT INTO distribution_events
		            (order_id, material_id, qty, event_type, scan_id, actor_id,
		             custody_from, custody_to, occurred_at)
		VALUES      (?, ?, ?, ?, ?, ?, ?, ?, datetime('now'))
		RETURNING   id, order_id, material_id, qty, event_type, scan_id, actor_id,
		            custody_from, custody_to, occurred_at`

	row := r.db.QueryRow(q,
		evt.OrderID, evt.MaterialID, evt.Qty, evt.EventType, evt.ScanID,
		evt.ActorID, evt.CustodyFrom, evt.CustodyTo,
	)

	out := &models.DistributionEvent{}
	if err := row.Scan(
		&out.ID, &out.OrderID, &out.MaterialID, &out.Qty, &out.EventType,
		&out.ScanID, &out.ActorID, &out.CustodyFrom, &out.CustodyTo, &out.OccurredAt,
	); err != nil {
		return nil, fmt.Errorf("repository: RecordEvent: %w", err)
	}
	return out, nil
}

// ---------------------------------------------------------------
// Read — per-order
// ---------------------------------------------------------------

// GetByOrderID returns all distribution events for a given order, oldest first.
func (r *DistributionRepository) GetByOrderID(orderID int64) ([]models.DistributionEvent, error) {
	const q = `
		SELECT id, order_id, material_id, qty, event_type, scan_id, actor_id,
		       custody_from, custody_to, occurred_at
		FROM   distribution_events
		WHERE  order_id = ?
		ORDER  BY occurred_at ASC`

	rows, err := r.db.Query(q, orderID)
	if err != nil {
		return nil, fmt.Errorf("repository: GetByOrderID: %w", err)
	}
	defer rows.Close()
	return scanDistributionEvents(rows)
}

// ---------------------------------------------------------------
// Read — ledger (filterable)
// ---------------------------------------------------------------

// ListEvents returns a paginated, filtered view of all distribution events,
// newest first.
func (r *DistributionRepository) ListEvents(filters DistributionFilter, limit, offset int) ([]models.DistributionEvent, error) {
	conditions := []string{"1=1"}
	args := []interface{}{}

	if filters.ScanID != "" {
		conditions = append(conditions, "scan_id = ?")
		args = append(args, filters.ScanID)
	}
	if filters.MaterialID != nil {
		conditions = append(conditions, "material_id = ?")
		args = append(args, *filters.MaterialID)
	}
	if filters.EventType != "" {
		conditions = append(conditions, "event_type = ?")
		args = append(args, filters.EventType)
	}
	if filters.ActorID != nil {
		conditions = append(conditions, "actor_id = ?")
		args = append(args, *filters.ActorID)
	}
	if filters.DateFrom != "" {
		conditions = append(conditions, "occurred_at >= ?")
		args = append(args, filters.DateFrom)
	}
	if filters.DateTo != "" {
		conditions = append(conditions, "occurred_at <= ?")
		args = append(args, filters.DateTo)
	}

	q := `
		SELECT id, order_id, material_id, qty, event_type, scan_id, actor_id,
		       custody_from, custody_to, occurred_at
		FROM   distribution_events
		WHERE  ` + strings.Join(conditions, " AND ") + `
		ORDER  BY occurred_at DESC
		LIMIT  ? OFFSET ?`

	args = append(args, limit, offset)
	rows, err := r.db.Query(q, args...)
	if err != nil {
		return nil, fmt.Errorf("repository: ListEvents: %w", err)
	}
	defer rows.Close()
	return scanDistributionEvents(rows)
}

// ---------------------------------------------------------------
// Read — custody chain
// ---------------------------------------------------------------

// GetCustodyChain returns the full chronological event history for a specific
// physical copy identified by scan_id.
func (r *DistributionRepository) GetCustodyChain(scanID string) ([]models.DistributionEvent, error) {
	const q = `
		SELECT id, order_id, material_id, qty, event_type, scan_id, actor_id,
		       custody_from, custody_to, occurred_at
		FROM   distribution_events
		WHERE  scan_id = ?
		ORDER  BY occurred_at ASC`

	rows, err := r.db.Query(q, scanID)
	if err != nil {
		return nil, fmt.Errorf("repository: GetCustodyChain: %w", err)
	}
	defer rows.Close()
	return scanDistributionEvents(rows)
}

// ---------------------------------------------------------------
// Read — pending issues / pick list
// ---------------------------------------------------------------

// GetPendingIssues returns order items that have not yet been fully issued,
// where the parent order is in pending_shipment or in_transit status.
// It joins against materials for the title and users for the student name.
func (r *DistributionRepository) GetPendingIssues(limit, offset int) ([]PendingIssue, error) {
	const q = `
		SELECT  o.id            AS order_id,
		        oi.material_id,
		        m.title,
		        oi.qty,
		        o.status,
		        u.username      AS user_name
		FROM    order_items oi
		JOIN    orders   o  ON o.id  = oi.order_id
		JOIN    materials m  ON m.id  = oi.material_id
		JOIN    users     u  ON u.id  = o.user_id
		WHERE   o.status IN ('pending_shipment', 'in_transit')
		  AND   oi.fulfillment_status = 'pending'
		ORDER   BY o.created_at ASC
		LIMIT   ? OFFSET ?`

	rows, err := r.db.Query(q, limit, offset)
	if err != nil {
		return nil, fmt.Errorf("repository: GetPendingIssues: %w", err)
	}
	defer rows.Close()

	var out []PendingIssue
	for rows.Next() {
		var pi PendingIssue
		if err := rows.Scan(
			&pi.OrderID, &pi.MaterialID, &pi.Title,
			&pi.Qty, &pi.Status, &pi.UserName,
		); err != nil {
			return nil, fmt.Errorf("repository: GetPendingIssues: scan: %w", err)
		}
		out = append(out, pi)
	}
	return out, rows.Err()
}

// ---------------------------------------------------------------
// Counts
// ---------------------------------------------------------------

// CountBackorders returns the number of unresolved backorder records.
func (r *DistributionRepository) CountBackorders() (int, error) {
	const q = `SELECT COUNT(*) FROM backorders WHERE resolved_at IS NULL`
	var n int
	if err := r.db.QueryRow(q).Scan(&n); err != nil {
		return 0, fmt.Errorf("repository: CountBackorders: %w", err)
	}
	return n, nil
}

// ---------------------------------------------------------------
// helpers
// ---------------------------------------------------------------

func scanDistributionEvents(rows *sql.Rows) ([]models.DistributionEvent, error) {
	var out []models.DistributionEvent
	for rows.Next() {
		var e models.DistributionEvent
		if err := rows.Scan(
			&e.ID, &e.OrderID, &e.MaterialID, &e.Qty, &e.EventType,
			&e.ScanID, &e.ActorID, &e.CustodyFrom, &e.CustodyTo, &e.OccurredAt,
		); err != nil {
			return nil, fmt.Errorf("scanDistributionEvents: %w", err)
		}
		out = append(out, e)
	}
	return out, rows.Err()
}

package repository

import (
	"database/sql"
	"fmt"
	"math"

	"w2t86/internal/models"
)

// AnalyticsRepository provides all KPI, spatial, and export queries.
type AnalyticsRepository struct {
	db *sql.DB
}

// NewAnalyticsRepository returns an AnalyticsRepository backed by the given database.
func NewAnalyticsRepository(db *sql.DB) *AnalyticsRepository {
	return &AnalyticsRepository{db: db}
}

// InventoryLevel summarises stock levels for one material.
type InventoryLevel struct {
	MaterialID   int64
	Title        string
	TotalQty     int
	AvailableQty int
	ReservedQty  int
}

// MaterialStat carries order-count and average-rating for a material.
type MaterialStat struct {
	MaterialID int64
	Title      string
	OrderCount int
	AvgRating  float64
}

// CourseOrderStat holds per-section/material demand and fulfillment data for
// an instructor, derived from the actual course_plans, course_sections, and
// order_items tables.
type CourseOrderStat struct {
	CourseName    string
	SectionName   string // empty when no section is assigned
	MaterialTitle string
	RequestedQty  int
	ApprovedQty   int
	PlanStatus    string
	Ordered       int // order_items rows that reference this material
	Fulfilled     int // order_items with fulfillment_status='fulfilled'
}

// OrderExportRow is a flattened row for the orders CSV export.
type OrderExportRow struct {
	OrderID     int64
	UserName    string
	UserEmail   string
	Status      string
	TotalAmount float64
	CreatedAt   string
	ItemCount   int
}

// DistribExportRow is a flattened row for the distribution-events CSV export.
type DistribExportRow struct {
	ScanID        string
	EventType     string
	MaterialTitle string
	ActorName     string
	OccurredAt    string
}

// ---------------------------------------------------------------
// KPI queries
// ---------------------------------------------------------------

// OrdersByStatus returns a map of order-status → count for all non-deleted orders.
func (r *AnalyticsRepository) OrdersByStatus() (map[string]int, error) {
	const q = `
		SELECT status, COUNT(*) AS cnt
		FROM   orders
		GROUP  BY status`

	rows, err := r.db.Query(q)
	if err != nil {
		return nil, fmt.Errorf("analytics: OrdersByStatus: %w", err)
	}
	defer rows.Close()

	out := make(map[string]int)
	for rows.Next() {
		var status string
		var cnt int
		if err := rows.Scan(&status, &cnt); err != nil {
			return nil, fmt.Errorf("analytics: OrdersByStatus scan: %w", err)
		}
		out[status] = cnt
	}
	return out, rows.Err()
}

// periodExpr converts a period string ("7d", "30d", "90d") to a SQLite
// datetime modifier.  Defaults to 30 days.
func periodExpr(period string) string {
	switch period {
	case "7d":
		return "-7 days"
	case "90d":
		return "-90 days"
	default:
		return "-30 days"
	}
}

// FulfillmentRate returns the fraction of orders (in the given period) whose
// status is "completed", expressed as a value 0–100.
func (r *AnalyticsRepository) FulfillmentRate(period string) (float64, error) {
	mod := periodExpr(period)
	const q = `
		SELECT
			CAST(SUM(CASE WHEN status = 'completed' THEN 1 ELSE 0 END) AS REAL) /
			NULLIF(COUNT(*), 0) * 100
		FROM orders
		WHERE created_at >= datetime('now', ?)`

	var rate sql.NullFloat64
	if err := r.db.QueryRow(q, mod).Scan(&rate); err != nil {
		return 0, fmt.Errorf("analytics: FulfillmentRate: %w", err)
	}
	if !rate.Valid {
		return 0, nil
	}
	return rate.Float64, nil
}

// ReturnRate returns the fraction of completed orders (in the given period)
// that have at least one return request, expressed as a value 0–100.
func (r *AnalyticsRepository) ReturnRate(period string) (float64, error) {
	mod := periodExpr(period)
	const q = `
		SELECT
			CAST(COUNT(DISTINCT rr.order_id) AS REAL) /
			NULLIF(COUNT(DISTINCT o.id), 0) * 100
		FROM   orders o
		LEFT   JOIN return_requests rr ON rr.order_id = o.id
		WHERE  o.status = 'completed'
		  AND  o.created_at >= datetime('now', ?)`

	var rate sql.NullFloat64
	if err := r.db.QueryRow(q, mod).Scan(&rate); err != nil {
		return 0, fmt.Errorf("analytics: ReturnRate: %w", err)
	}
	if !rate.Valid {
		return 0, nil
	}
	return rate.Float64, nil
}

// InventoryLevels returns stock levels for all non-deleted, active materials.
func (r *AnalyticsRepository) InventoryLevels() ([]InventoryLevel, error) {
	const q = `
		SELECT id, title, total_qty, available_qty, reserved_qty
		FROM   materials
		WHERE  deleted_at IS NULL
		ORDER  BY title`

	rows, err := r.db.Query(q)
	if err != nil {
		return nil, fmt.Errorf("analytics: InventoryLevels: %w", err)
	}
	defer rows.Close()

	var out []InventoryLevel
	for rows.Next() {
		var l InventoryLevel
		if err := rows.Scan(&l.MaterialID, &l.Title, &l.TotalQty, &l.AvailableQty, &l.ReservedQty); err != nil {
			return nil, fmt.Errorf("analytics: InventoryLevels scan: %w", err)
		}
		out = append(out, l)
	}
	return out, rows.Err()
}

// ActiveUserCount returns the number of users that have placed at least one
// order in the last 30 days.
func (r *AnalyticsRepository) ActiveUserCount() (int, error) {
	const q = `
		SELECT COUNT(DISTINCT user_id)
		FROM   orders
		WHERE  created_at >= datetime('now', '-30 days')`

	var n int
	if err := r.db.QueryRow(q).Scan(&n); err != nil {
		return 0, fmt.Errorf("analytics: ActiveUserCount: %w", err)
	}
	return n, nil
}

// TopMaterials returns the top N materials by total order-item count, with
// their average star rating.
func (r *AnalyticsRepository) TopMaterials(limit int) ([]MaterialStat, error) {
	const q = `
		SELECT m.id,
		       m.title,
		       COUNT(oi.id)                                    AS order_count,
		       COALESCE(AVG(CAST(rt.stars AS REAL)), 0.0)     AS avg_rating
		FROM   materials m
		JOIN   order_items oi ON oi.material_id = m.id
		LEFT   JOIN ratings rt ON rt.material_id = m.id
		WHERE  m.deleted_at IS NULL
		GROUP  BY m.id, m.title
		ORDER  BY order_count DESC
		LIMIT  ?`

	rows, err := r.db.Query(q, limit)
	if err != nil {
		return nil, fmt.Errorf("analytics: TopMaterials: %w", err)
	}
	defer rows.Close()

	var out []MaterialStat
	for rows.Next() {
		var s MaterialStat
		if err := rows.Scan(&s.MaterialID, &s.Title, &s.OrderCount, &s.AvgRating); err != nil {
			return nil, fmt.Errorf("analytics: TopMaterials scan: %w", err)
		}
		out = append(out, s)
	}
	return out, rows.Err()
}

// ---------------------------------------------------------------
// KPI snapshots
// ---------------------------------------------------------------

// SaveKPISnapshot inserts a new kpi_snapshots row.
func (r *AnalyticsRepository) SaveKPISnapshot(name, dimension string, value float64, period string) error {
	const q = `
		INSERT INTO kpi_snapshots (metric_name, dimension, value, period, computed_at)
		VALUES (?, ?, ?, ?, datetime('now'))`

	var dimPtr *string
	if dimension != "" {
		dimPtr = &dimension
	}
	var periodPtr *string
	if period != "" {
		periodPtr = &period
	}

	if _, err := r.db.Exec(q, name, dimPtr, value, periodPtr); err != nil {
		return fmt.Errorf("analytics: SaveKPISnapshot: %w", err)
	}
	return nil
}

// GetKPIHistory returns the most-recent `limit` snapshots for the given metric
// and dimension, ordered newest-first.
func (r *AnalyticsRepository) GetKPIHistory(name, dimension string, limit int) ([]models.KPISnapshot, error) {
	const q = `
		SELECT id, metric_name, dimension, value, period, computed_at
		FROM   kpi_snapshots
		WHERE  metric_name = ?
		  AND  (dimension = ? OR (dimension IS NULL AND ? = ''))
		ORDER  BY computed_at DESC
		LIMIT  ?`

	rows, err := r.db.Query(q, name, dimension, dimension, limit)
	if err != nil {
		return nil, fmt.Errorf("analytics: GetKPIHistory: %w", err)
	}
	defer rows.Close()

	var out []models.KPISnapshot
	for rows.Next() {
		var k models.KPISnapshot
		if err := rows.Scan(&k.ID, &k.MetricName, &k.Dimension, &k.Value, &k.Period, &k.ComputedAt); err != nil {
			return nil, fmt.Errorf("analytics: GetKPIHistory scan: %w", err)
		}
		out = append(out, k)
	}
	return out, rows.Err()
}

// ---------------------------------------------------------------
// Instructor: course order stats
// ---------------------------------------------------------------

// CourseOrderStats returns per-section/material demand and fulfillment data
// for all course plans owned by the given instructor.  Results come from the
// actual course_plans, course_sections, courses, and order_items tables — no
// approximations.
func (r *AnalyticsRepository) CourseOrderStats(instructorID int64) ([]CourseOrderStat, error) {
	const q = `
		SELECT
			c.name                                                                AS course_name,
			COALESCE(cs.name, '')                                                 AS section_name,
			m.title                                                               AS material_title,
			cp.requested_qty,
			cp.approved_qty,
			cp.status                                                             AS plan_status,
			COUNT(oi.id)                                                          AS ordered,
			SUM(CASE WHEN oi.fulfillment_status = 'fulfilled' THEN 1 ELSE 0 END) AS fulfilled
		FROM   courses c
		JOIN   course_plans cp ON cp.course_id = c.id
		LEFT   JOIN course_sections cs ON cs.id = cp.section_id
		JOIN   materials m ON m.id = cp.material_id
		LEFT   JOIN order_items oi ON oi.material_id = m.id
		LEFT   JOIN orders o ON o.id = oi.order_id AND o.status != 'canceled'
		WHERE  c.instructor_id = ?
		  AND  m.deleted_at IS NULL
		GROUP  BY c.id, COALESCE(cp.section_id, 0), cp.material_id
		ORDER  BY c.name, COALESCE(cs.name, ''), m.title`

	rows, err := r.db.Query(q, instructorID)
	if err != nil {
		return nil, fmt.Errorf("analytics: CourseOrderStats: %w", err)
	}
	defer rows.Close()

	var out []CourseOrderStat
	for rows.Next() {
		var s CourseOrderStat
		if err := rows.Scan(&s.CourseName, &s.SectionName, &s.MaterialTitle,
			&s.RequestedQty, &s.ApprovedQty, &s.PlanStatus,
			&s.Ordered, &s.Fulfilled); err != nil {
			return nil, fmt.Errorf("analytics: CourseOrderStats scan: %w", err)
		}
		out = append(out, s)
	}
	return out, rows.Err()
}

// CountPendingPlanItems returns the number of course_plans rows with
// status='pending' across all courses owned by the given instructor.
func (r *AnalyticsRepository) CountPendingPlanItems(instructorID int64) (int, error) {
	const q = `
		SELECT COUNT(*)
		FROM   course_plans cp
		JOIN   courses c ON c.id = cp.course_id
		WHERE  c.instructor_id = ? AND cp.status = 'pending'`
	var n int
	if err := r.db.QueryRow(q, instructorID).Scan(&n); err != nil {
		return 0, fmt.Errorf("analytics: CountPendingPlanItems: %w", err)
	}
	return n, nil
}

// ---------------------------------------------------------------
// Spatial queries
// ---------------------------------------------------------------

// GetLocations returns all locations of the given type.  Pass "" to return all.
func (r *AnalyticsRepository) GetLocations(locType string) ([]models.Location, error) {
	q := `SELECT id, name, type, geom_wkt, lat, lng, properties, created_at FROM locations`
	args := []interface{}{}
	if locType != "" {
		q += ` WHERE type = ?`
		args = append(args, locType)
	}
	q += ` ORDER BY name`

	rows, err := r.db.Query(q, args...)
	if err != nil {
		return nil, fmt.Errorf("analytics: GetLocations: %w", err)
	}
	defer rows.Close()

	var out []models.Location
	for rows.Next() {
		var l models.Location
		if err := rows.Scan(&l.ID, &l.Name, &l.Type, &l.GeomWKT, &l.Lat, &l.Lng, &l.Properties, &l.CreatedAt); err != nil {
			return nil, fmt.Errorf("analytics: GetLocations scan: %w", err)
		}
		out = append(out, l)
	}
	return out, rows.Err()
}

// GetSpatialAggregates returns all spatial_aggregates rows for the given layer type.
func (r *AnalyticsRepository) GetSpatialAggregates(layerType string) ([]models.SpatialAggregate, error) {
	const q = `
		SELECT id, layer_type, cell_key, metric, value, computed_at
		FROM   spatial_aggregates
		WHERE  layer_type = ?
		ORDER  BY cell_key`

	rows, err := r.db.Query(q, layerType)
	if err != nil {
		return nil, fmt.Errorf("analytics: GetSpatialAggregates: %w", err)
	}
	defer rows.Close()

	var out []models.SpatialAggregate
	for rows.Next() {
		var sa models.SpatialAggregate
		if err := rows.Scan(&sa.ID, &sa.LayerType, &sa.CellKey, &sa.Metric, &sa.Value, &sa.ComputedAt); err != nil {
			return nil, fmt.Errorf("analytics: GetSpatialAggregates scan: %w", err)
		}
		out = append(out, sa)
	}
	return out, rows.Err()
}

// UpsertSpatialAggregate inserts or replaces a spatial_aggregates row.
func (r *AnalyticsRepository) UpsertSpatialAggregate(layerType, cellKey, metric string, value float64) error {
	const q = `
		INSERT INTO spatial_aggregates (layer_type, cell_key, metric, value, computed_at)
		VALUES (?, ?, ?, ?, datetime('now'))
		ON CONFLICT(layer_type, cell_key, metric)
		DO UPDATE SET value = excluded.value, computed_at = excluded.computed_at`

	if _, err := r.db.Exec(q, layerType, cellKey, metric, value); err != nil {
		return fmt.Errorf("analytics: UpsertSpatialAggregate: %w", err)
	}
	return nil
}

// ComputeGridAggregation groups locations of the given layerType into lat/lng
// grid cells of size gridSizeKm (approximated as degrees: 1 deg ≈ 111 km),
// counts the locations per cell, and saves the result to spatial_aggregates.
func (r *AnalyticsRepository) ComputeGridAggregation(layerType, metric string, gridSizeKm float64) error {
	locs, err := r.GetLocations(layerType)
	if err != nil {
		return fmt.Errorf("analytics: ComputeGridAggregation: fetch locations: %w", err)
	}

	const degPerKm = 1.0 / 111.0
	gridStep := gridSizeKm * degPerKm

	counts := make(map[string]float64)
	for _, loc := range locs {
		if loc.Lat == nil || loc.Lng == nil {
			continue
		}
		cellLat := math.Floor(*loc.Lat/gridStep) * gridStep
		cellLng := math.Floor(*loc.Lng/gridStep) * gridStep
		cellKey := fmt.Sprintf("%.6f,%.6f", cellLat, cellLng)
		counts[cellKey]++
	}

	for cellKey, count := range counts {
		if err := r.UpsertSpatialAggregate(layerType, cellKey, metric, count); err != nil {
			return fmt.Errorf("analytics: ComputeGridAggregation: upsert %q: %w", cellKey, err)
		}
	}
	return nil
}

// ---------------------------------------------------------------
// Exports
// ---------------------------------------------------------------

// ExportOrders returns a flat slice of order rows for CSV export.  All
// filter parameters are optional (pass "" to skip).
func (r *AnalyticsRepository) ExportOrders(status, dateFrom, dateTo string) ([]OrderExportRow, error) {
	where := "1=1"
	args := []interface{}{}

	if status != "" {
		where += " AND o.status = ?"
		args = append(args, status)
	}
	if dateFrom != "" {
		where += " AND o.created_at >= ?"
		args = append(args, dateFrom)
	}
	if dateTo != "" {
		where += " AND o.created_at <= ?"
		args = append(args, dateTo)
	}

	q := `
		SELECT
			o.id,
			u.username,
			u.email,
			o.status,
			o.total_amount,
			o.created_at,
			COUNT(oi.id) AS item_count
		FROM   orders o
		JOIN   users u  ON u.id = o.user_id
		LEFT   JOIN order_items oi ON oi.order_id = o.id
		WHERE  ` + where + `
		GROUP  BY o.id, u.username, u.email, o.status, o.total_amount, o.created_at
		ORDER  BY o.created_at DESC`

	rows, err := r.db.Query(q, args...)
	if err != nil {
		return nil, fmt.Errorf("analytics: ExportOrders: %w", err)
	}
	defer rows.Close()

	var out []OrderExportRow
	for rows.Next() {
		var row OrderExportRow
		if err := rows.Scan(
			&row.OrderID, &row.UserName, &row.UserEmail,
			&row.Status, &row.TotalAmount, &row.CreatedAt, &row.ItemCount,
		); err != nil {
			return nil, fmt.Errorf("analytics: ExportOrders scan: %w", err)
		}
		out = append(out, row)
	}
	return out, rows.Err()
}

// ExportDistribution returns a flat slice of distribution-event rows for CSV
// export, optionally filtered by date range.
func (r *AnalyticsRepository) ExportDistribution(dateFrom, dateTo string) ([]DistribExportRow, error) {
	where := "1=1"
	args := []interface{}{}

	if dateFrom != "" {
		where += " AND de.occurred_at >= ?"
		args = append(args, dateFrom)
	}
	if dateTo != "" {
		where += " AND de.occurred_at <= ?"
		args = append(args, dateTo)
	}

	q := `
		SELECT
			COALESCE(de.scan_id, ''),
			de.event_type,
			m.title,
			COALESCE(u.username, ''),
			de.occurred_at
		FROM   distribution_events de
		JOIN   materials m ON m.id = de.material_id
		LEFT   JOIN users u ON u.id = de.actor_id
		WHERE  ` + where + `
		ORDER  BY de.occurred_at DESC`

	rows, err := r.db.Query(q, args...)
	if err != nil {
		return nil, fmt.Errorf("analytics: ExportDistribution: %w", err)
	}
	defer rows.Close()

	var out []DistribExportRow
	for rows.Next() {
		var row DistribExportRow
		if err := rows.Scan(
			&row.ScanID, &row.EventType, &row.MaterialTitle,
			&row.ActorName, &row.OccurredAt,
		); err != nil {
			return nil, fmt.Errorf("analytics: ExportDistribution scan: %w", err)
		}
		out = append(out, row)
	}
	return out, rows.Err()
}

// ---------------------------------------------------------------
// Dashboard stat counts (used by GET /api/stats/:stat)
// ---------------------------------------------------------------

// CountOrdersForUser returns the number of orders placed by a specific user.
func (r *AnalyticsRepository) CountOrdersForUser(userID int64) (int, error) {
	var n int
	err := r.db.QueryRow(`SELECT COUNT(*) FROM orders WHERE user_id = ?`, userID).Scan(&n)
	return n, err
}

// CountFavoritesListsForUser returns the number of favorites lists owned by a user.
func (r *AnalyticsRepository) CountFavoritesListsForUser(userID int64) (int, error) {
	var n int
	err := r.db.QueryRow(`SELECT COUNT(*) FROM favorites_lists WHERE user_id = ?`, userID).Scan(&n)
	return n, err
}

// CountRecentMaterialViewsForUser returns the number of distinct materials
// viewed by the user in the last 30 days.
func (r *AnalyticsRepository) CountRecentMaterialViewsForUser(userID int64) (int, error) {
	var n int
	err := r.db.QueryRow(`
		SELECT COUNT(DISTINCT material_id)
		FROM   browse_history
		WHERE  user_id = ?
		  AND  visited_at >= datetime('now', '-30 days')`,
		userID,
	).Scan(&n)
	return n, err
}

// TotalOrderCount returns the total number of orders across all users.
func (r *AnalyticsRepository) TotalOrderCount() (int, error) {
	var n int
	err := r.db.QueryRow(`SELECT COUNT(*) FROM orders`).Scan(&n)
	return n, err
}

// PendingReturnRequestCount returns the number of return requests in 'pending' status.
func (r *AnalyticsRepository) PendingReturnRequestCount() (int, error) {
	var n int
	err := r.db.QueryRow(`SELECT COUNT(*) FROM return_requests WHERE status = 'pending'`).Scan(&n)
	return n, err
}

// PendingIssueCount returns the number of order items awaiting distribution.
func (r *AnalyticsRepository) PendingIssueCount() (int, error) {
	var n int
	err := r.db.QueryRow(`
		SELECT COUNT(*)
		FROM   order_items oi
		JOIN   orders o ON o.id = oi.order_id
		WHERE  o.status = 'pending_shipment'
		  AND  oi.fulfillment_status = 'pending'`).Scan(&n)
	return n, err
}

// BackorderCount returns the number of unresolved backorder records.
func (r *AnalyticsRepository) BackorderCount() (int, error) {
	var n int
	err := r.db.QueryRow(`SELECT COUNT(*) FROM backorders WHERE resolved_at IS NULL`).Scan(&n)
	return n, err
}

// InstructorPendingApprovalCount returns the count of return requests pending
// approval. Instructors approve/reject return requests.
func (r *AnalyticsRepository) InstructorPendingApprovalCount() (int, error) {
	var n int
	err := r.db.QueryRow(`SELECT COUNT(*) FROM return_requests WHERE status = 'pending'`).Scan(&n)
	return n, err
}

// ModerationQueueCount returns the number of comments currently in the moderation queue.
func (r *AnalyticsRepository) ModerationQueueCount() (int, error) {
	var n int
	err := r.db.QueryRow(`SELECT COUNT(*) FROM comments WHERE status = 'collapsed'`).Scan(&n)
	return n, err
}

// ---------------------------------------------------------------
// Extended KPI metrics
// ---------------------------------------------------------------

// GMV returns Gross Merchandise Value: the sum of total_amount for completed
// orders in the given period ("7d", "30d", "90d").
func (r *AnalyticsRepository) GMV(period string) (float64, error) {
	mod := periodExpr(period)
	const q = `
		SELECT COALESCE(SUM(total_amount), 0)
		FROM   orders
		WHERE  status = 'completed'
		  AND  created_at >= datetime('now', ?)`

	var v float64
	if err := r.db.QueryRow(q, mod).Scan(&v); err != nil {
		return 0, fmt.Errorf("analytics: GMV: %w", err)
	}
	return v, nil
}

// AOV returns Average Order Value: GMV / number of completed orders in the
// given period.  Returns 0 when there are no completed orders.
func (r *AnalyticsRepository) AOV(period string) (float64, error) {
	mod := periodExpr(period)
	const q = `
		SELECT
			COALESCE(SUM(total_amount), 0) /
			NULLIF(CAST(COUNT(*) AS REAL), 0)
		FROM   orders
		WHERE  status = 'completed'
		  AND  created_at >= datetime('now', ?)`

	var v sql.NullFloat64
	if err := r.db.QueryRow(q, mod).Scan(&v); err != nil {
		return 0, fmt.Errorf("analytics: AOV: %w", err)
	}
	if !v.Valid {
		return 0, nil
	}
	return v.Float64, nil
}

// ConversionRate returns the fraction of registered users (non-deleted) who
// have placed at least one order, expressed as a value 0–100.
func (r *AnalyticsRepository) ConversionRate() (float64, error) {
	const q = `
		SELECT
			CAST(COUNT(DISTINCT o.user_id) AS REAL) /
			NULLIF(COUNT(DISTINCT u.id), 0) * 100
		FROM   users u
		LEFT   JOIN orders o ON o.user_id = u.id
		WHERE  u.deleted_at IS NULL`

	var rate sql.NullFloat64
	if err := r.db.QueryRow(q).Scan(&rate); err != nil {
		return 0, fmt.Errorf("analytics: ConversionRate: %w", err)
	}
	if !rate.Valid {
		return 0, nil
	}
	return rate.Float64, nil
}

// RepeatPurchaseRate returns the fraction of ordering users who have placed
// 2 or more orders, expressed as a value 0–100.
func (r *AnalyticsRepository) RepeatPurchaseRate() (float64, error) {
	const q = `
		WITH user_orders AS (
			SELECT user_id, COUNT(*) AS order_count
			FROM   orders
			GROUP  BY user_id
		)
		SELECT
			CAST(SUM(CASE WHEN order_count >= 2 THEN 1 ELSE 0 END) AS REAL) /
			NULLIF(COUNT(*), 0) * 100
		FROM user_orders`

	var rate sql.NullFloat64
	if err := r.db.QueryRow(q).Scan(&rate); err != nil {
		return 0, fmt.Errorf("analytics: RepeatPurchaseRate: %w", err)
	}
	if !rate.Valid {
		return 0, nil
	}
	return rate.Float64, nil
}

// FunnelStage represents the order count at a given status stage in the funnel.
type FunnelStage struct {
	Stage string
	Count int
}

// OrderFunnel returns the drop-off counts at each order pipeline stage in the
// canonical order: pending_payment → pending_shipment → in_transit → completed.
// The canceled stage is also included for reference.
func (r *AnalyticsRepository) OrderFunnel() ([]FunnelStage, error) {
	const q = `
		SELECT status, COUNT(*) AS cnt
		FROM   orders
		WHERE  status IN ('pending_payment', 'pending_shipment', 'in_transit', 'completed', 'canceled')
		GROUP  BY status`

	rows, err := r.db.Query(q)
	if err != nil {
		return nil, fmt.Errorf("analytics: OrderFunnel: %w", err)
	}
	defer rows.Close()

	// Build a map first, then return in canonical stage order.
	counts := make(map[string]int)
	for rows.Next() {
		var status string
		var cnt int
		if err := rows.Scan(&status, &cnt); err != nil {
			return nil, fmt.Errorf("analytics: OrderFunnel scan: %w", err)
		}
		counts[status] = cnt
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}

	order := []string{"pending_payment", "pending_shipment", "in_transit", "completed", "canceled"}
	out := make([]FunnelStage, 0, len(order))
	for _, s := range order {
		out = append(out, FunnelStage{Stage: s, Count: counts[s]})
	}
	return out, nil
}

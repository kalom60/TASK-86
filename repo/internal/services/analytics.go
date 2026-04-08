package services

import (
	"bytes"
	"encoding/csv"
	"encoding/json"
	"fmt"
	"strconv"

	"w2t86/internal/crypto"
	"w2t86/internal/models"
	"w2t86/internal/repository"
)

// AnalyticsService orchestrates KPI computations, geospatial queries, and
// PII-masked CSV exports.
type AnalyticsService struct {
	repo *repository.AnalyticsRepository
}

// NewAnalyticsService creates an AnalyticsService wired to the given repository.
func NewAnalyticsService(r *repository.AnalyticsRepository) *AnalyticsService {
	return &AnalyticsService{repo: r}
}

// ---------------------------------------------------------------
// Dashboard data
// ---------------------------------------------------------------

// AdminDashboard contains all KPI data shown on the admin dashboard.
type AdminDashboard struct {
	OrdersByStatus   map[string]int
	FulfillmentRate  float64
	ReturnRate       float64
	ActiveUsers      int
	Inventory        []repository.InventoryLevel
	TopMaterials     []repository.MaterialStat
	GMV              float64
	AOV              float64
	ConversionRate   float64
	RepeatPurchase   float64
	Funnel           []repository.FunnelStage
}

// AdminDashboardData gathers all admin-dashboard KPIs in a single call.
func (s *AnalyticsService) AdminDashboardData() (*AdminDashboard, error) {
	obStatus, err := s.repo.OrdersByStatus()
	if err != nil {
		return nil, fmt.Errorf("service: AdminDashboardData: orders by status: %w", err)
	}

	fulfillRate, err := s.repo.FulfillmentRate("30d")
	if err != nil {
		return nil, fmt.Errorf("service: AdminDashboardData: fulfillment rate: %w", err)
	}

	returnRate, err := s.repo.ReturnRate("30d")
	if err != nil {
		return nil, fmt.Errorf("service: AdminDashboardData: return rate: %w", err)
	}

	activeUsers, err := s.repo.ActiveUserCount()
	if err != nil {
		return nil, fmt.Errorf("service: AdminDashboardData: active users: %w", err)
	}

	inventory, err := s.repo.InventoryLevels()
	if err != nil {
		return nil, fmt.Errorf("service: AdminDashboardData: inventory: %w", err)
	}

	topMaterials, err := s.repo.TopMaterials(10)
	if err != nil {
		return nil, fmt.Errorf("service: AdminDashboardData: top materials: %w", err)
	}

	gmv, err := s.repo.GMV("30d")
	if err != nil {
		return nil, fmt.Errorf("service: AdminDashboardData: GMV: %w", err)
	}

	aov, err := s.repo.AOV("30d")
	if err != nil {
		return nil, fmt.Errorf("service: AdminDashboardData: AOV: %w", err)
	}

	conversionRate, err := s.repo.ConversionRate()
	if err != nil {
		return nil, fmt.Errorf("service: AdminDashboardData: conversion rate: %w", err)
	}

	repeatPurchase, err := s.repo.RepeatPurchaseRate()
	if err != nil {
		return nil, fmt.Errorf("service: AdminDashboardData: repeat purchase: %w", err)
	}

	funnel, err := s.repo.OrderFunnel()
	if err != nil {
		return nil, fmt.Errorf("service: AdminDashboardData: funnel: %w", err)
	}

	return &AdminDashboard{
		OrdersByStatus:  obStatus,
		FulfillmentRate: fulfillRate,
		ReturnRate:      returnRate,
		ActiveUsers:     activeUsers,
		Inventory:       inventory,
		TopMaterials:    topMaterials,
		GMV:             gmv,
		AOV:             aov,
		ConversionRate:  conversionRate,
		RepeatPurchase:  repeatPurchase,
		Funnel:          funnel,
	}, nil
}

// InstructorDashboard contains course-level analytics for an instructor.
type InstructorDashboard struct {
	CourseStats      []repository.CourseOrderStat
	PendingApprovals int
}

// InstructorDashboardData gathers the instructor-dashboard data for the given user.
func (s *AnalyticsService) InstructorDashboardData(userID int64) (*InstructorDashboard, error) {
	stats, err := s.repo.CourseOrderStats(userID)
	if err != nil {
		return nil, fmt.Errorf("service: InstructorDashboardData: course stats: %w", err)
	}

	// Count course plan items awaiting approval from the actual course_plans table.
	pending, err := s.repo.CountPendingPlanItems(userID)
	if err != nil {
		return nil, fmt.Errorf("service: InstructorDashboardData: pending plans: %w", err)
	}

	return &InstructorDashboard{
		CourseStats:      stats,
		PendingApprovals: pending,
	}, nil
}

// ---------------------------------------------------------------
// Geospatial
// ---------------------------------------------------------------

// MapData contains location points, density aggregates, and a GeoJSON string.
type MapData struct {
	Locations  []models.Location
	Aggregates []models.SpatialAggregate
	GeoJSON    string
}

// GetMapData returns all geospatial data for the given layer type.
func (s *AnalyticsService) GetMapData(layerType string) (*MapData, error) {
	locs, err := s.repo.GetLocations(layerType)
	if err != nil {
		return nil, fmt.Errorf("service: GetMapData: locations: %w", err)
	}

	aggs, err := s.repo.GetSpatialAggregates(layerType)
	if err != nil {
		return nil, fmt.Errorf("service: GetMapData: aggregates: %w", err)
	}

	geoJSON := locationsToGeoJSON(locs)

	return &MapData{
		Locations:  locs,
		Aggregates: aggs,
		GeoJSON:    geoJSON,
	}, nil
}

// ComputeGrid triggers grid aggregation for the given layer and metric.
func (s *AnalyticsService) ComputeGrid(layerType, metric string, gridSizeKm float64) error {
	if err := s.repo.ComputeGridAggregation(layerType, metric, gridSizeKm); err != nil {
		return fmt.Errorf("service: ComputeGrid: %w", err)
	}
	return nil
}

// geoJSONFeature is the internal representation of a GeoJSON Feature.
type geoJSONFeature struct {
	Type     string                 `json:"type"`
	Geometry geoJSONGeometry        `json:"geometry"`
	Props    map[string]interface{} `json:"properties"`
}

type geoJSONGeometry struct {
	Type        string    `json:"type"`
	Coordinates []float64 `json:"coordinates"`
}

type geoJSONCollection struct {
	Type     string           `json:"type"`
	Features []geoJSONFeature `json:"features"`
}

// locationsToGeoJSON converts a slice of Location rows into a GeoJSON
// FeatureCollection string.  Locations without lat/lng are skipped.
func locationsToGeoJSON(locations []models.Location) string {
	features := make([]geoJSONFeature, 0, len(locations))
	for _, loc := range locations {
		if loc.Lat == nil || loc.Lng == nil {
			continue
		}
		props := map[string]interface{}{
			"id":   loc.ID,
			"name": loc.Name,
		}
		if loc.Type != nil {
			props["type"] = *loc.Type
		}
		if loc.Properties != nil {
			// Attempt to merge the stored JSON properties blob.
			var extra map[string]interface{}
			if err := json.Unmarshal([]byte(*loc.Properties), &extra); err == nil {
				for k, v := range extra {
					props[k] = v
				}
			} else {
				props["properties"] = *loc.Properties
			}
		}
		features = append(features, geoJSONFeature{
			Type: "Feature",
			Geometry: geoJSONGeometry{
				Type:        "Point",
				Coordinates: []float64{*loc.Lng, *loc.Lat}, // GeoJSON order: [lng, lat]
			},
			Props: props,
		})
	}

	col := geoJSONCollection{
		Type:     "FeatureCollection",
		Features: features,
	}
	b, err := json.Marshal(col)
	if err != nil {
		return `{"type":"FeatureCollection","features":[]}`
	}
	return string(b)
}

// ---------------------------------------------------------------
// CSV exports with PII masking
// ---------------------------------------------------------------

// GetKPIHistory returns the most-recent limit snapshots for the given metric
// and dimension.
func (s *AnalyticsService) GetKPIHistory(name, dimension string, limit int) ([]models.KPISnapshot, error) {
	history, err := s.repo.GetKPIHistory(name, dimension, limit)
	if err != nil {
		return nil, fmt.Errorf("service: GetKPIHistory: %w", err)
	}
	return history, nil
}

// ExportOrdersCSV returns a CSV byte slice for orders, with UserName masked
// via crypto.MaskName and UserEmail masked via crypto.MaskID.
func (s *AnalyticsService) ExportOrdersCSV(status, dateFrom, dateTo string) ([]byte, error) {
	rows, err := s.repo.ExportOrders(status, dateFrom, dateTo)
	if err != nil {
		return nil, fmt.Errorf("service: ExportOrdersCSV: %w", err)
	}

	var buf bytes.Buffer
	w := csv.NewWriter(&buf)

	// Header
	if err := w.Write([]string{
		"order_id", "user_name", "user_email", "status",
		"total_amount", "created_at", "item_count",
	}); err != nil {
		return nil, fmt.Errorf("service: ExportOrdersCSV: write header: %w", err)
	}

	for _, row := range rows {
		record := []string{
			strconv.FormatInt(row.OrderID, 10),
			crypto.MaskName(row.UserName),
			crypto.MaskID(row.UserEmail),
			row.Status,
			strconv.FormatFloat(row.TotalAmount, 'f', 2, 64),
			row.CreatedAt,
			strconv.Itoa(row.ItemCount),
		}
		if err := w.Write(record); err != nil {
			return nil, fmt.Errorf("service: ExportOrdersCSV: write row: %w", err)
		}
	}

	w.Flush()
	if err := w.Error(); err != nil {
		return nil, fmt.Errorf("service: ExportOrdersCSV: flush: %w", err)
	}
	return buf.Bytes(), nil
}

// ExportDistributionCSV returns a CSV byte slice for distribution events, with
// ActorName masked via crypto.MaskName.
func (s *AnalyticsService) ExportDistributionCSV(dateFrom, dateTo string) ([]byte, error) {
	rows, err := s.repo.ExportDistribution(dateFrom, dateTo)
	if err != nil {
		return nil, fmt.Errorf("service: ExportDistributionCSV: %w", err)
	}

	var buf bytes.Buffer
	w := csv.NewWriter(&buf)

	// Header
	if err := w.Write([]string{
		"scan_id", "event_type", "material_title", "actor_name", "occurred_at",
	}); err != nil {
		return nil, fmt.Errorf("service: ExportDistributionCSV: write header: %w", err)
	}

	for _, row := range rows {
		record := []string{
			row.ScanID,
			row.EventType,
			row.MaterialTitle,
			crypto.MaskName(row.ActorName),
			row.OccurredAt,
		}
		if err := w.Write(record); err != nil {
			return nil, fmt.Errorf("service: ExportDistributionCSV: write row: %w", err)
		}
	}

	w.Flush()
	if err := w.Error(); err != nil {
		return nil, fmt.Errorf("service: ExportDistributionCSV: flush: %w", err)
	}
	return buf.Bytes(), nil
}

// ---------------------------------------------------------------
// Dashboard stat cards
// ---------------------------------------------------------------

// DashboardStatCard holds the data for a single dashboard stat card.
type DashboardStatCard struct {
	Count int
	Label string
	Icon  string
	Color string
}

// DashboardStat returns the count and display metadata for a named stat card.
// The stat name matches the path segment used in GET /api/stats/:stat.
func (s *AnalyticsService) DashboardStat(stat string, userID int64) (*DashboardStatCard, error) {
	switch stat {
	case "my-orders":
		n, err := s.repo.CountOrdersForUser(userID)
		return &DashboardStatCard{Count: n, Label: "My Orders", Icon: "cart", Color: "primary"}, err
	case "my-favorites":
		n, err := s.repo.CountFavoritesListsForUser(userID)
		return &DashboardStatCard{Count: n, Label: "My Favorites", Icon: "heart", Color: "danger"}, err
	case "recent-materials":
		n, err := s.repo.CountRecentMaterialViewsForUser(userID)
		return &DashboardStatCard{Count: n, Label: "Recent Materials", Icon: "book", Color: "success"}, err
	case "total-orders":
		n, err := s.repo.TotalOrderCount()
		return &DashboardStatCard{Count: n, Label: "Total Orders", Icon: "bag", Color: "primary"}, err
	case "pending-returns":
		n, err := s.repo.PendingReturnRequestCount()
		return &DashboardStatCard{Count: n, Label: "Pending Returns", Icon: "arrow-return-left", Color: "warning"}, err
	case "active-users":
		n, err := s.repo.ActiveUserCount()
		return &DashboardStatCard{Count: n, Label: "Active Users", Icon: "people", Color: "success"}, err
	case "pending-issues":
		n, err := s.repo.PendingIssueCount()
		return &DashboardStatCard{Count: n, Label: "Pending Issues", Icon: "box-seam", Color: "warning"}, err
	case "backorders":
		n, err := s.repo.BackorderCount()
		return &DashboardStatCard{Count: n, Label: "Backorders", Icon: "exclamation-circle", Color: "danger"}, err
	case "course-plans":
		n, err := s.repo.InstructorPendingApprovalCount()
		return &DashboardStatCard{Count: n, Label: "Pending Approvals", Icon: "calendar3", Color: "primary"}, err
	case "moderation-queue":
		n, err := s.repo.ModerationQueueCount()
		return &DashboardStatCard{Count: n, Label: "Moderation Queue", Icon: "shield-check", Color: "primary"}, err
	case "conversion-rate":
		rate, err := s.repo.ConversionRate()
		return &DashboardStatCard{Count: int(rate), Label: "Conversion Rate %", Icon: "graph-up-arrow", Color: "success"}, err
	case "repeat-purchase":
		rate, err := s.repo.RepeatPurchaseRate()
		return &DashboardStatCard{Count: int(rate), Label: "Repeat Purchase %", Icon: "arrow-repeat", Color: "info"}, err
	default:
		return &DashboardStatCard{Count: 0, Label: stat, Icon: "bar-chart", Color: "secondary"}, nil
	}
}

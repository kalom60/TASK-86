package handlers

import (
	"fmt"

	"github.com/gofiber/fiber/v2"

	"w2t86/internal/middleware"
	"w2t86/internal/observability"
	"w2t86/internal/services"
)

// AnalyticsHandler handles all analytics, KPI, geospatial, and export routes.
type AnalyticsHandler struct {
	analyticsService *services.AnalyticsService
}

// NewAnalyticsHandler creates an AnalyticsHandler backed by the given service.
func NewAnalyticsHandler(as *services.AnalyticsService) *AnalyticsHandler {
	return &AnalyticsHandler{analyticsService: as}
}

// ---------------------------------------------------------------
// Dashboard routes
// ---------------------------------------------------------------

// AdminDashboard handles GET /dashboard/admin
// Renders the admin analytics dashboard with full KPI data.
func (h *AnalyticsHandler) AdminDashboard(c *fiber.Ctx) error {
	user := middleware.GetUser(c)

	data, err := h.analyticsService.AdminDashboardData()
	if err != nil {
		return internalErr(c, observability.App, "load admin dashboard data failed", err, "user_id", user.ID)
	}

	// Compute total and pending counts for the KPI cards.
	total := 0
	for _, n := range data.OrdersByStatus {
		total += n
	}
	pending := data.OrdersByStatus["pending_payment"] + data.OrdersByStatus["pending_shipment"]

	return c.Render("analytics/admin_dashboard", fiber.Map{
		"Title":           "Admin Dashboard",
		"User":            user,
		"ActivePage":      "analytics",
		"Dashboard":       data,
		"TotalOrders":     total,
		"PendingOrders":   pending,
		"FulfillmentRate": fmt.Sprintf("%.1f", data.FulfillmentRate),
		"ReturnRate":      fmt.Sprintf("%.1f", data.ReturnRate),
		"ActiveUsers":     data.ActiveUsers,
		"Inventory":       data.Inventory,
		"TopMaterials":    data.TopMaterials,
		"OrdersByStatus":  data.OrdersByStatus,
		"GMV":             fmt.Sprintf("%.2f", data.GMV),
		"AOV":             fmt.Sprintf("%.2f", data.AOV),
		"ConversionRate":  fmt.Sprintf("%.1f", data.ConversionRate),
		"RepeatPurchase":  fmt.Sprintf("%.1f", data.RepeatPurchase),
		"Funnel":          data.Funnel,
	}, "layouts/base")
}

// InstructorDashboard handles GET /dashboard/instructor
// Renders the instructor analytics dashboard.
func (h *AnalyticsHandler) InstructorDashboard(c *fiber.Ctx) error {
	user := middleware.GetUser(c)

	data, err := h.analyticsService.InstructorDashboardData(user.ID)
	if err != nil {
		return internalErr(c, observability.App, "load instructor dashboard data failed", err, "user_id", user.ID)
	}

	return c.Render("analytics/instructor_dashboard", fiber.Map{
		"Title":            "Instructor Dashboard",
		"User":             user,
		"ActivePage":       "analytics",
		"CourseStats":      data.CourseStats,
		"PendingApprovals": data.PendingApprovals,
	}, "layouts/base")
}

// ---------------------------------------------------------------
// Geospatial routes
// ---------------------------------------------------------------

// MapPage handles GET /analytics/map
// Renders the full-screen Leaflet map page.
func (h *AnalyticsHandler) MapPage(c *fiber.Ctx) error {
	user := middleware.GetUser(c)
	return c.Render("analytics/map", fiber.Map{
		"Title":      "Geospatial Map",
		"User":       user,
		"ActivePage": "analytics",
	}, "layouts/base")
}

// MapData handles GET /analytics/map/data
// Returns MapData as JSON for consumption by Leaflet/map.js.
func (h *AnalyticsHandler) MapData(c *fiber.Ctx) error {
	layerType := c.Query("layer", "")

	mapData, err := h.analyticsService.GetMapData(layerType)
	if err != nil {
		observability.App.Error("get map data failed", "error", err, "path", c.Path(), "method", c.Method(), "layer", layerType)
		return c.Status(fiber.StatusInternalServerError).JSON(APIError{
			Code: fiber.StatusInternalServerError,
			Msg:  "An unexpected error occurred. Please try again.",
		})
	}

	return c.JSON(fiber.Map{
		"locations":  mapData.Locations,
		"aggregates": mapData.Aggregates,
		"geojson":    mapData.GeoJSON,
	})
}

// ComputeGrid handles POST /analytics/map/compute
// Triggers grid aggregation computation.
func (h *AnalyticsHandler) ComputeGrid(c *fiber.Ctx) error {
	layerType := c.FormValue("layer", "")
	metric := c.FormValue("metric", "count")
	gridSizeKm := float64(10)
	if gs := c.FormValue("grid_size_km"); gs != "" {
		if v, err := fmt.Sscanf(gs, "%f", &gridSizeKm); err != nil || v == 0 {
			gridSizeKm = 10
		}
	}

	if err := h.analyticsService.ComputeGrid(layerType, metric, gridSizeKm); err != nil {
		observability.App.Error("compute grid failed", "error", err, "path", c.Path(), "method", c.Method(), "layer", layerType, "metric", metric)
		return c.Status(fiber.StatusInternalServerError).JSON(APIError{
			Code: fiber.StatusInternalServerError,
			Msg:  "An unexpected error occurred. Please try again.",
		})
	}

	return c.JSON(fiber.Map{"status": "ok"})
}

// ---------------------------------------------------------------
// Export routes
// ---------------------------------------------------------------

// ExportOrders handles GET /analytics/export/orders
// Streams a CSV file download with PII-masked order data.
func (h *AnalyticsHandler) ExportOrders(c *fiber.Ctx) error {
	user := middleware.GetUser(c)
	status := c.Query("status")
	dateFrom := c.Query("date_from")
	dateTo := c.Query("date_to")

	observability.App.Info("orders export requested",
		"actor_id", user.ID, "role", user.Role,
		"status_filter", status, "date_from", dateFrom, "date_to", dateTo,
		"ip", c.IP())

	csvBytes, err := h.analyticsService.ExportOrdersCSV(status, dateFrom, dateTo)
	if err != nil {
		return internalErr(c, observability.App, "export orders CSV failed", err,
			"actor_id", user.ID, "status_filter", status)
	}

	c.Set("Content-Type", "text/csv")
	c.Set("Content-Disposition", `attachment; filename="orders_export.csv"`)
	return c.Send(csvBytes)
}

// ExportDistribution handles GET /analytics/export/distribution
// Streams a CSV file download with PII-masked distribution event data.
func (h *AnalyticsHandler) ExportDistribution(c *fiber.Ctx) error {
	user := middleware.GetUser(c)
	dateFrom := c.Query("date_from")
	dateTo := c.Query("date_to")

	observability.App.Info("distribution export requested",
		"actor_id", user.ID, "role", user.Role,
		"date_from", dateFrom, "date_to", dateTo,
		"ip", c.IP())

	csvBytes, err := h.analyticsService.ExportDistributionCSV(dateFrom, dateTo)
	if err != nil {
		return internalErr(c, observability.App, "export distribution CSV failed", err,
			"actor_id", user.ID, "date_from", dateFrom, "date_to", dateTo)
	}

	c.Set("Content-Type", "text/csv")
	c.Set("Content-Disposition", `attachment; filename="distribution_export.csv"`)
	return c.Send(csvBytes)
}

// ---------------------------------------------------------------
// Dashboard stat card API  (GET /api/stats/:stat)
// ---------------------------------------------------------------

// adminOnlyStats is the set of stat names that expose sensitive business metrics
// and must only be served to admin-role users.
var adminOnlyStats = map[string]bool{
	"total-orders":    true,
	"active-users":    true,
	"conversion-rate": true,
	"repeat-purchase": true,
}

// DashboardStat handles GET /api/stats/:stat — returns a card-body HTML
// partial used by HTMX to populate dashboard KPI cards on page load.
func (h *AnalyticsHandler) DashboardStat(c *fiber.Ctx) error {
	user := middleware.GetUser(c)
	stat := c.Params("stat")

	// Sensitive metrics are restricted to admin users only.
	if adminOnlyStats[stat] && user.Role != "admin" {
		return c.Status(fiber.StatusForbidden).JSON(fiber.Map{
			"error": "access denied: admin role required for this metric",
		})
	}

	card, err := h.analyticsService.DashboardStat(stat, user.ID)
	if err != nil {
		observability.App.Warn("dashboard stat failed", "stat", stat, "user_id", user.ID, "error", err)
		// Return a graceful placeholder rather than a hard error so the card
		// stays visible.
		card = &services.DashboardStatCard{Count: 0, Label: stat, Icon: "bar-chart", Color: "secondary"}
	}

	return c.Render("partials/stat_card_body", fiber.Map{
		"Count": card.Count,
		"Label": card.Label,
		"Icon":  card.Icon,
		"Color": card.Color,
	})
}

// ---------------------------------------------------------------
// KPI history route
// ---------------------------------------------------------------

// KPIHistory handles GET /analytics/kpi/:name
// Returns KPI snapshot history as JSON for charts.
func (h *AnalyticsHandler) KPIHistory(c *fiber.Ctx) error {
	name := c.Params("name")
	dimension := c.Query("dimension", "")
	limit := c.QueryInt("limit", 30)

	history, err := h.analyticsService.GetKPIHistory(name, dimension, limit)
	if err != nil {
		observability.App.Error("get KPI history failed", "error", err, "path", c.Path(), "method", c.Method(), "kpi_name", name)
		return c.Status(fiber.StatusInternalServerError).JSON(APIError{
			Code: fiber.StatusInternalServerError,
			Msg:  "An unexpected error occurred. Please try again.",
		})
	}

	return c.JSON(fiber.Map{
		"metric":    name,
		"dimension": dimension,
		"history":   history,
	})
}

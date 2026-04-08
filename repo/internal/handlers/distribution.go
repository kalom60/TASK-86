package handlers

import (
	"strconv"

	"github.com/gofiber/fiber/v2"

	"w2t86/internal/middleware"
	"w2t86/internal/observability"
	"w2t86/internal/repository"
	"w2t86/internal/services"
)

// DistributionHandler handles all clerk-facing distribution routes.
type DistributionHandler struct {
	distService *services.DistributionService
}

// NewDistributionHandler creates a DistributionHandler backed by ds.
func NewDistributionHandler(ds *services.DistributionService) *DistributionHandler {
	return &DistributionHandler{distService: ds}
}

// ---------------------------------------------------------------
// Pick list
// ---------------------------------------------------------------

// PickList handles GET /distribution.
// Renders the clerk's daily pick list with stats and pending issue rows.
func (h *DistributionHandler) PickList(c *fiber.Ctx) error {
	user := middleware.GetUser(c)

	limit := 50
	offset := 0
	if p := c.QueryInt("page", 1); p > 1 {
		offset = (p - 1) * limit
	}

	issues, err := h.distService.GetPendingIssues(limit, offset)
	if err != nil {
		observability.Distribution.Warn("get pending issues failed", "error", err)
		issues = nil
	}

	// Count backorders for the stats bar.
	backorders, err := h.distService.CountBackorders()
	if err != nil {
		observability.Distribution.Warn("count backorders failed", "error", err)
		backorders = 0
	}

	return c.Render("distribution/picklist", fiber.Map{
		"Title":       "Distribution — Pick List",
		"User":        user,
		"ActivePage":  "distribution",
		"Issues":      issues,
		"TotalIssues": len(issues),
		"Backorders":  backorders,
	}, "layouts/base")
}

// ---------------------------------------------------------------
// Issue
// ---------------------------------------------------------------

// IssueItems handles POST /distribution/issue.
// Expects form fields: order_id, scan_id, material_id[], qty[].
func (h *DistributionHandler) IssueItems(c *fiber.Ctx) error {
	user := middleware.GetUser(c)

	orderID, err := strconv.ParseInt(c.FormValue("order_id"), 10, 64)
	if err != nil || orderID <= 0 {
		return htmxErr(c, fiber.StatusBadRequest, "Invalid order ID")
	}

	scanID := c.FormValue("scan_id")
	if scanID == "" {
		return htmxErr(c, fiber.StatusBadRequest, "Scan ID is required")
	}

	materialIDs := c.Request().PostArgs().PeekMulti("material_id")
	qtys := c.Request().PostArgs().PeekMulti("qty")
	issuedQtys := c.Request().PostArgs().PeekMulti("issued_qty") // optional partial issue
	if len(materialIDs) == 0 || len(materialIDs) != len(qtys) {
		return htmxErr(c, fiber.StatusBadRequest, "Invalid item data")
	}

	items := make([]services.IssueItem, 0, len(materialIDs))
	for i, rawID := range materialIDs {
		mid, err := strconv.ParseInt(string(rawID), 10, 64)
		if err != nil {
			return htmxErr(c, fiber.StatusBadRequest, "Invalid material ID")
		}
		qty, err := strconv.Atoi(string(qtys[i]))
		if err != nil || qty <= 0 {
			return htmxErr(c, fiber.StatusBadRequest, "Invalid quantity")
		}
		item := services.IssueItem{MaterialID: mid, Qty: qty}
		// Parse optional partial-issue quantity.
		if i < len(issuedQtys) {
			if n, err := strconv.Atoi(string(issuedQtys[i])); err == nil && n > 0 {
				item.IssuedQty = n
			}
		}
		items = append(items, item)
	}

	if err := h.distService.IssueItems(orderID, user.ID, scanID, items); err != nil {
		return internalErr(c, observability.Distribution, "issue items failed", err, "order_id", orderID, "actor_id", user.ID)
	}

	observability.Distribution.Info("items issued", "order_id", orderID, "actor_id", user.ID, "scan_id", scanID, "item_count", len(items))

	if c.Get("HX-Request") == "true" {
		return c.Render("partials/flash", fiber.Map{
			"Message": "Items issued successfully.",
		})
	}
	return c.Redirect("/distribution", fiber.StatusFound)
}

// ---------------------------------------------------------------
// Return
// ---------------------------------------------------------------

// RecordReturn handles POST /distribution/return.
// Expects form fields: order_id, material_id, scan_id, qty.
func (h *DistributionHandler) RecordReturn(c *fiber.Ctx) error {
	user := middleware.GetUser(c)

	orderID, err := strconv.ParseInt(c.FormValue("order_id"), 10, 64)
	if err != nil || orderID <= 0 {
		return htmxErr(c, fiber.StatusBadRequest, "Invalid order ID")
	}
	materialID, err := strconv.ParseInt(c.FormValue("material_id"), 10, 64)
	if err != nil || materialID <= 0 {
		return htmxErr(c, fiber.StatusBadRequest, "Invalid material ID")
	}
	scanID := c.FormValue("scan_id")
	if scanID == "" {
		return htmxErr(c, fiber.StatusBadRequest, "Scan ID is required")
	}
	qty, err := strconv.Atoi(c.FormValue("qty"))
	if err != nil || qty <= 0 {
		return htmxErr(c, fiber.StatusBadRequest, "Invalid quantity")
	}

	if err := h.distService.RecordReturn(orderID, materialID, user.ID, scanID, qty); err != nil {
		return internalErr(c, observability.Distribution, "record return failed", err, "order_id", orderID, "actor_id", user.ID)
	}

	observability.Distribution.Info("return recorded", "order_id", orderID, "material_id", materialID, "actor_id", user.ID, "scan_id", scanID, "qty", qty)

	if c.Get("HX-Request") == "true" {
		return c.Render("partials/flash", fiber.Map{
			"Message": "Return recorded successfully.",
		})
	}
	return c.Redirect("/distribution", fiber.StatusFound)
}

// ---------------------------------------------------------------
// Exchange
// ---------------------------------------------------------------

// RecordExchange handles POST /distribution/exchange.
// Expects form fields: order_id, old_material_id, new_material_id, scan_id, qty.
func (h *DistributionHandler) RecordExchange(c *fiber.Ctx) error {
	user := middleware.GetUser(c)

	orderID, err := strconv.ParseInt(c.FormValue("order_id"), 10, 64)
	if err != nil || orderID <= 0 {
		return htmxErr(c, fiber.StatusBadRequest, "Invalid order ID")
	}
	oldMaterialID, err := strconv.ParseInt(c.FormValue("old_material_id"), 10, 64)
	if err != nil || oldMaterialID <= 0 {
		return htmxErr(c, fiber.StatusBadRequest, "Invalid old material ID")
	}
	newMaterialID, err := strconv.ParseInt(c.FormValue("new_material_id"), 10, 64)
	if err != nil || newMaterialID <= 0 {
		return htmxErr(c, fiber.StatusBadRequest, "Invalid new material ID")
	}
	scanID := c.FormValue("scan_id")
	if scanID == "" {
		return htmxErr(c, fiber.StatusBadRequest, "Scan ID is required")
	}
	qty, err := strconv.Atoi(c.FormValue("qty"))
	if err != nil || qty <= 0 {
		return htmxErr(c, fiber.StatusBadRequest, "Invalid quantity")
	}

	if err := h.distService.RecordExchange(orderID, oldMaterialID, newMaterialID, user.ID, scanID, qty); err != nil {
		return internalErr(c, observability.Distribution, "record exchange failed", err, "order_id", orderID, "actor_id", user.ID)
	}

	observability.Distribution.Info("exchange recorded", "order_id", orderID, "old_material_id", oldMaterialID, "new_material_id", newMaterialID, "actor_id", user.ID, "qty", qty)

	if c.Get("HX-Request") == "true" {
		return c.Render("partials/flash", fiber.Map{
			"Message": "Exchange recorded successfully.",
		})
	}
	return c.Redirect("/distribution", fiber.StatusFound)
}

// ---------------------------------------------------------------
// Reissue
// ---------------------------------------------------------------

// ReissueForm handles GET /distribution/reissue.
// Renders the two-step reissue form page.
func (h *DistributionHandler) ReissueForm(c *fiber.Ctx) error {
	user := middleware.GetUser(c)
	return c.Render("distribution/reissue_form", fiber.Map{
		"Title":      "Reissue Copy",
		"User":       user,
		"ActivePage": "distribution",
	}, "layouts/base")
}

// ReissueItem handles POST /distribution/reissue.
// Expects form fields: order_id, material_id, old_scan_id, new_scan_id, reason.
func (h *DistributionHandler) ReissueItem(c *fiber.Ctx) error {
	user := middleware.GetUser(c)

	orderID, err := strconv.ParseInt(c.FormValue("order_id"), 10, 64)
	if err != nil || orderID <= 0 {
		return htmxErr(c, fiber.StatusBadRequest, "Invalid order ID")
	}
	materialID, err := strconv.ParseInt(c.FormValue("material_id"), 10, 64)
	if err != nil || materialID <= 0 {
		return htmxErr(c, fiber.StatusBadRequest, "Invalid material ID")
	}
	oldScanID := c.FormValue("old_scan_id")
	newScanID := c.FormValue("new_scan_id")
	reason := c.FormValue("reason")

	if err := h.distService.ReissueItem(orderID, materialID, user.ID, oldScanID, newScanID, reason); err != nil {
		return internalErr(c, observability.Distribution, "reissue item failed", err, "order_id", orderID, "actor_id", user.ID)
	}

	observability.Distribution.Info("item reissued", "order_id", orderID, "material_id", materialID, "actor_id", user.ID, "reason", reason)

	if c.Get("HX-Request") == "true" {
		return c.Render("partials/flash", fiber.Map{
			"Message": "Reissue recorded successfully.",
		})
	}
	return c.Redirect("/distribution", fiber.StatusFound)
}

// ---------------------------------------------------------------
// Ledger
// ---------------------------------------------------------------

// Ledger handles GET /distribution/ledger.
// Renders the filterable, paginated event ledger.
func (h *DistributionHandler) Ledger(c *fiber.Ctx) error {
	user := middleware.GetUser(c)

	filters, limit, offset, page := parseLedgerQuery(c)

	events, err := h.distService.GetLedger(filters, limit, offset)
	if err != nil {
		observability.Distribution.Warn("get ledger failed", "error", err)
		events = nil
	}

	return c.Render("distribution/ledger", fiber.Map{
		"Title":      "Distribution Ledger",
		"User":       user,
		"ActivePage": "distribution",
		"Events":     events,
		"Filters":    filters,
		"Page":       page,
		"Limit":      limit,
	}, "layouts/base")
}

// LedgerSearch handles GET /distribution/ledger/search.
// HTMX partial: re-renders only the events table body with current filters.
func (h *DistributionHandler) LedgerSearch(c *fiber.Ctx) error {
	filters, limit, offset, page := parseLedgerQuery(c)

	events, err := h.distService.GetLedger(filters, limit, offset)
	if err != nil {
		observability.Distribution.Warn("get ledger search failed", "error", err)
		events = nil
	}

	return c.Render("distribution/ledger", fiber.Map{
		"Events":  events,
		"Filters": filters,
		"Page":    page,
		"Limit":   limit,
	})
}

// ---------------------------------------------------------------
// Custody chain
// ---------------------------------------------------------------

// CustodyChain handles GET /distribution/custody/:scanID.
// Renders the timeline of all events for a single physical copy.
func (h *DistributionHandler) CustodyChain(c *fiber.Ctx) error {
	user := middleware.GetUser(c)
	scanID := c.Params("scanID")
	if scanID == "" {
		return apiErr(c, fiber.StatusBadRequest, "Scan ID is required")
	}

	chain, err := h.distService.GetCustodyChain(scanID)
	if err != nil {
		return internalErr(c, observability.Distribution, "get custody chain failed", err, "scan_id", scanID)
	}

	return c.Render("distribution/custody", fiber.Map{
		"Title":      "Custody Chain — " + scanID,
		"User":       user,
		"ActivePage": "distribution",
		"ScanID":     scanID,
		"Chain":      chain,
	}, "layouts/base")
}

// ---------------------------------------------------------------
// helpers
// ---------------------------------------------------------------

// parseLedgerQuery extracts filter, pagination values from the request.
func parseLedgerQuery(c *fiber.Ctx) (repository.DistributionFilter, int, int, int) {
	const pageSize = 50

	page := c.QueryInt("page", 1)
	if page < 1 {
		page = 1
	}
	offset := (page - 1) * pageSize

	filters := repository.DistributionFilter{
		ScanID:    c.Query("scan_id"),
		EventType: c.Query("event_type"),
		DateFrom:  c.Query("date_from"),
		DateTo:    c.Query("date_to"),
	}

	if rawMID := c.Query("material_id"); rawMID != "" {
		if mid, err := strconv.ParseInt(rawMID, 10, 64); err == nil {
			filters.MaterialID = &mid
		}
	}
	if rawAID := c.Query("actor_id"); rawAID != "" {
		if aid, err := strconv.ParseInt(rawAID, 10, 64); err == nil {
			filters.ActorID = &aid
		}
	}

	return filters, pageSize, offset, page
}

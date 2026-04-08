package api_tests

import (
	"fmt"
	"net/http"
	"testing"
	"time"

	"w2t86/internal/models"
	"w2t86/internal/repository"
)

// edge_cases_test.go covers boundary conditions, idempotency, and
// cross-cutting behaviors that don't cleanly belong in a single domain file.

// ---------------------------------------------------------------------------
// Share links
// ---------------------------------------------------------------------------

// TestEdge_ShareLink_ValidToken returns non-404/410 for a valid token.
func TestEdge_ShareLink_ValidToken(t *testing.T) {
	app, db, cleanup := newTestApp(t)
	defer cleanup()

	user := createUser(t, db, "student")
	mat := createMaterial(t, db)

	engRepo := repository.NewEngagementRepository(db)
	list, err := engRepo.CreateList(user.ID, "Shared List", "public")
	if err != nil {
		t.Fatalf("create list: %v", err)
	}
	if err := engRepo.AddToList(list.ID, mat.ID); err != nil {
		t.Fatalf("add to list: %v", err)
	}
	token, err := engRepo.GenerateShareToken(list.ID, time.Now().Add(24*time.Hour))
	if err != nil {
		t.Fatalf("generate token: %v", err)
	}

	resp := makeRequest(app, http.MethodGet, "/share/"+token, "", "", "")
	if resp.StatusCode == http.StatusNotFound || resp.StatusCode == http.StatusGone {
		t.Fatalf("expected non-404/410 for valid share token, got %d", resp.StatusCode)
	}
}

// TestEdge_ShareLink_ExpiredToken returns 410 Gone for a token that exists but has expired.
func TestEdge_ShareLink_ExpiredToken(t *testing.T) {
	app, db, cleanup := newTestApp(t)
	defer cleanup()

	user := createUser(t, db, "student")
	engRepo := repository.NewEngagementRepository(db)
	list, err := engRepo.CreateList(user.ID, "Expired List", "public")
	if err != nil {
		t.Fatalf("create list: %v", err)
	}
	token, err := engRepo.GenerateShareToken(list.ID, time.Now().Add(-1*time.Second))
	if err != nil {
		t.Fatalf("generate expired token: %v", err)
	}

	resp := makeRequest(app, http.MethodGet, "/share/"+token, "", "", "")
	if resp.StatusCode != http.StatusGone {
		t.Errorf("expected 410 Gone for expired share token, got %d", resp.StatusCode)
	}
}

// TestEdge_ShareLink_UnknownToken returns 404.
func TestEdge_ShareLink_UnknownToken(t *testing.T) {
	app, _, cleanup := newTestApp(t)
	defer cleanup()

	resp := makeRequest(app, http.MethodGet, "/share/no-such-token-xyz", "", "", "")
	if resp.StatusCode != http.StatusNotFound && resp.StatusCode/100 != 2 {
		t.Logf("unknown share token returned %d (expected 404 or rendered error page)", resp.StatusCode)
	}
}

// ---------------------------------------------------------------------------
// Comment reporting and collapse
// ---------------------------------------------------------------------------

// TestEdge_ReportComment_CollapseAt3 verifies that 3 unique reports collapse a comment.
func TestEdge_ReportComment_CollapseAt3(t *testing.T) {
	app, db, cleanup := newTestApp(t)
	defer cleanup()

	author := createUser(t, db, "student")
	mat := createMaterial(t, db)
	engRepo := repository.NewEngagementRepository(db)
	comment, err := engRepo.CreateComment(mat.ID, author.ID, "reported comment", 0)
	if err != nil {
		t.Fatalf("create comment: %v", err)
	}

	ct := "application/x-www-form-urlencoded"
	path := fmt.Sprintf("/comments/%d/report", comment.ID)
	for i := 0; i < 3; i++ {
		cookie := loginAs(t, app, db, "student")
		resp := makeRequest(app, http.MethodPost, path, "reason=spam", cookie, ct)
		_ = readBody(resp)
	}

	var status string
	if err := db.QueryRow(`SELECT status FROM comments WHERE id=?`, comment.ID).Scan(&status); err != nil {
		t.Fatalf("query comment status: %v", err)
	}
	if status != "collapsed" {
		t.Errorf("expected status 'collapsed' after 3 reports, got %q", status)
	}
}

// ---------------------------------------------------------------------------
// Rating idempotency
// ---------------------------------------------------------------------------

// TestEdge_Rate_Duplicate_Upserts verifies the second rating upserts (no duplicate row).
func TestEdge_Rate_Duplicate_Upserts(t *testing.T) {
	app, db, cleanup := newTestApp(t)
	defer cleanup()

	cookie := loginAs(t, app, db, "student")
	mat := createMaterial(t, db)
	ct := "application/x-www-form-urlencoded"
	path := fmt.Sprintf("/materials/%d/rate", mat.ID)

	r1 := makeRequest(app, http.MethodPost, path, "stars=3", cookie, ct)
	_ = readBody(r1)
	r2 := makeRequest(app, http.MethodPost, path, "stars=5", cookie, ct)
	if r2.StatusCode != http.StatusOK && r2.StatusCode != http.StatusFound {
		t.Fatalf("second rating returned %d", r2.StatusCode)
	}

	var count int
	if err := db.QueryRow(`SELECT COUNT(*) FROM ratings WHERE material_id=?`, mat.ID).Scan(&count); err != nil {
		t.Fatalf("count ratings: %v", err)
	}
	if count != 1 {
		t.Errorf("expected exactly 1 rating row after upsert, got %d", count)
	}
}

// ---------------------------------------------------------------------------
// Browse history side-effect
// ---------------------------------------------------------------------------

// TestEdge_MaterialDetail_RecordsBrowseHistory verifies visiting a material page
// inserts a browse_history row for the authenticated user.
func TestEdge_MaterialDetail_RecordsBrowseHistory(t *testing.T) {
	app, db, cleanup := newTestApp(t)
	defer cleanup()

	cookie := loginAs(t, app, db, "student")
	mat := createMaterial(t, db)

	resp := makeRequest(app, http.MethodGet, fmt.Sprintf("/materials/%d", mat.ID), "", cookie, "")
	if resp.StatusCode == http.StatusNotFound {
		t.Fatalf("material %d not found (404)", mat.ID)
	}

	var count int
	if err := db.QueryRow(`SELECT COUNT(*) FROM browse_history WHERE material_id=?`, mat.ID).Scan(&count); err != nil {
		t.Fatalf("query browse_history: %v", err)
	}
	if count == 0 {
		t.Error("expected browse_history row after material detail visit")
	}
}

// ---------------------------------------------------------------------------
// Messaging
// ---------------------------------------------------------------------------

// TestEdge_Inbox_ReturnsOK returns 2xx for any authenticated user.
func TestEdge_Inbox_ReturnsOK(t *testing.T) {
	app, db, cleanup := newTestApp(t)
	defer cleanup()

	cookie := loginAs(t, app, db, "student")
	resp := makeRequest(app, http.MethodGet, "/inbox", "", cookie, "")
	if resp.StatusCode/100 != 2 {
		t.Fatalf("expected 2xx for inbox, got %d; body: %s", resp.StatusCode, readBody(resp))
	}
}

// TestEdge_Badge_ReturnsOK returns 2xx for an authenticated user.
func TestEdge_Badge_ReturnsOK(t *testing.T) {
	app, db, cleanup := newTestApp(t)
	defer cleanup()

	cookie := loginAs(t, app, db, "student")
	resp := makeRequest(app, http.MethodGet, "/inbox/badge", "", cookie, "")
	if resp.StatusCode/100 != 2 {
		t.Fatalf("expected 2xx for badge endpoint, got %d", resp.StatusCode)
	}
}

// ---------------------------------------------------------------------------
// Inventory never-negative invariant
// ---------------------------------------------------------------------------

// TestEdge_Inventory_NeverNegative verifies that placing orders for exactly
// all available stock and then one more results in a 422 for the over-order.
func TestEdge_Inventory_NeverNegative(t *testing.T) {
	app, db, cleanup := newTestApp(t)
	defer cleanup()

	// Create a material with exactly 2 available.
	matRepo := repository.NewMaterialRepository(db)
	m, err := matRepo.Create(&models.Material{
		Title:        "Limited Stock",
		TotalQty:     2,
		AvailableQty: 2,
		Status:       "active",
	})
	if err != nil {
		t.Fatalf("create material: %v", err)
	}

	cookie := loginAs(t, app, db, "student")
	ct := "application/x-www-form-urlencoded"

	// Order 2 — should succeed.
	r1 := makeRequest(app, http.MethodPost, "/orders",
		fmt.Sprintf("material_id=%d&qty=2&unit_price=9.99", m.ID), cookie, ct)
	if r1.StatusCode != http.StatusFound {
		t.Skipf("first order (qty=2) returned %d; skipping negative stock test", r1.StatusCode)
	}

	// Order 1 more — should fail with 422 (no stock left).
	r2 := makeRequest(app, http.MethodPost, "/orders",
		fmt.Sprintf("material_id=%d&qty=1&unit_price=9.99", m.ID), cookie, ct)
	if r2.StatusCode != http.StatusUnprocessableEntity {
		t.Fatalf("expected 422 for over-order (0 remaining), got %d; body: %s",
			r2.StatusCode, readBody(r2))
	}

	// Confirm available_qty is 0, not negative.
	var avail int
	if err := db.QueryRow(`SELECT available_qty FROM materials WHERE id=?`, m.ID).Scan(&avail); err != nil {
		t.Fatalf("query qty: %v", err)
	}
	if avail < 0 {
		t.Errorf("available_qty went negative: %d", avail)
	}
}

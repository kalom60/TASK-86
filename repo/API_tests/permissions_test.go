package api_tests

import (
	"fmt"
	"net/http"
	"testing"

	"w2t86/internal/repository"
)

// permissions_test.go verifies role-based access control across the portal.
// Each test focuses on one protected route and checks that:
//   - The required role CAN access it (no 403/401).
//   - A lower-privileged role CANNOT access it (403/401).

// ---------------------------------------------------------------------------
// Moderation queue
// ---------------------------------------------------------------------------

// TestPermission_Moderation_StudentForbidden returns 403 for a student.
func TestPermission_Moderation_StudentForbidden(t *testing.T) {
	app, db, cleanup := newTestApp(t)
	defer cleanup()

	cookie := loginAs(t, app, db, "student")
	resp := makeRequest(app, http.MethodGet, "/moderation", "", cookie, "")
	if resp.StatusCode != http.StatusForbidden && resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("expected 403/401 for student, got %d", resp.StatusCode)
	}
}

// TestPermission_Moderation_ModeratorAllowed returns non-403 for a moderator.
func TestPermission_Moderation_ModeratorAllowed(t *testing.T) {
	app, db, cleanup := newTestApp(t)
	defer cleanup()

	cookie := loginAs(t, app, db, "moderator")
	resp := makeRequest(app, http.MethodGet, "/moderation", "", cookie, "")
	if resp.StatusCode == http.StatusForbidden || resp.StatusCode == http.StatusUnauthorized {
		t.Fatalf("moderator should access /moderation, got %d", resp.StatusCode)
	}
}

// TestPermission_Moderation_AdminAllowed returns non-403 for an admin.
func TestPermission_Moderation_AdminAllowed(t *testing.T) {
	app, db, cleanup := newTestApp(t)
	defer cleanup()

	cookie := loginAs(t, app, db, "admin")
	resp := makeRequest(app, http.MethodGet, "/moderation", "", cookie, "")
	if resp.StatusCode == http.StatusForbidden || resp.StatusCode == http.StatusUnauthorized {
		t.Fatalf("admin should access /moderation, got %d", resp.StatusCode)
	}
}

// ---------------------------------------------------------------------------
// Distribution
// ---------------------------------------------------------------------------

// TestPermission_Distribution_StudentForbidden returns 403 for a student.
func TestPermission_Distribution_StudentForbidden(t *testing.T) {
	app, db, cleanup := newTestApp(t)
	defer cleanup()

	cookie := loginAs(t, app, db, "student")
	resp := makeRequest(app, http.MethodGet, "/distribution", "", cookie, "")
	if resp.StatusCode != http.StatusForbidden && resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("expected 403/401 for student, got %d", resp.StatusCode)
	}
}

// TestPermission_Distribution_ClerkAllowed returns non-403 for a clerk.
func TestPermission_Distribution_ClerkAllowed(t *testing.T) {
	app, db, cleanup := newTestApp(t)
	defer cleanup()

	cookie := loginAs(t, app, db, "clerk")
	resp := makeRequest(app, http.MethodGet, "/distribution", "", cookie, "")
	if resp.StatusCode == http.StatusForbidden || resp.StatusCode == http.StatusUnauthorized {
		t.Fatalf("clerk should access /distribution, got %d", resp.StatusCode)
	}
}

// TestPermission_DistributionLedger_InstructorForbidden returns 403.
func TestPermission_DistributionLedger_InstructorForbidden(t *testing.T) {
	app, db, cleanup := newTestApp(t)
	defer cleanup()

	cookie := loginAs(t, app, db, "instructor")
	resp := makeRequest(app, http.MethodGet, "/distribution/ledger", "", cookie, "")
	if resp.StatusCode != http.StatusForbidden && resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("expected 403/401 for instructor, got %d", resp.StatusCode)
	}
}

// ---------------------------------------------------------------------------
// Admin panel
// ---------------------------------------------------------------------------

// TestPermission_AdminUsers_ModeratorForbidden returns 403 for a moderator.
func TestPermission_AdminUsers_ModeratorForbidden(t *testing.T) {
	app, db, cleanup := newTestApp(t)
	defer cleanup()

	cookie := loginAs(t, app, db, "moderator")
	resp := makeRequest(app, http.MethodGet, "/admin/users", "", cookie, "")
	if resp.StatusCode != http.StatusForbidden && resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("expected 403/401 for moderator accessing admin users, got %d", resp.StatusCode)
	}
}

// TestPermission_AdminUsers_AdminAllowed returns non-403 for an admin.
func TestPermission_AdminUsers_AdminAllowed(t *testing.T) {
	app, db, cleanup := newTestApp(t)
	defer cleanup()

	cookie := loginAs(t, app, db, "admin")
	resp := makeRequest(app, http.MethodGet, "/admin/users", "", cookie, "")
	if resp.StatusCode == http.StatusForbidden || resp.StatusCode == http.StatusUnauthorized {
		t.Fatalf("admin should access /admin/users, got %d", resp.StatusCode)
	}
}

// TestPermission_Analytics_ClerkForbidden returns 403 for a clerk.
func TestPermission_Analytics_ClerkForbidden(t *testing.T) {
	app, db, cleanup := newTestApp(t)
	defer cleanup()

	cookie := loginAs(t, app, db, "clerk")
	resp := makeRequest(app, http.MethodGet, "/analytics/export/orders", "", cookie, "")
	if resp.StatusCode != http.StatusForbidden && resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("expected 403/401 for clerk on analytics export, got %d", resp.StatusCode)
	}
}

// TestPermission_Analytics_AdminAllowed returns non-403 for admin.
func TestPermission_Analytics_AdminAllowed(t *testing.T) {
	app, db, cleanup := newTestApp(t)
	defer cleanup()

	cookie := loginAs(t, app, db, "admin")
	resp := makeRequest(app, http.MethodGet, "/analytics/export/orders", "", cookie, "")
	if resp.StatusCode == http.StatusForbidden || resp.StatusCode == http.StatusUnauthorized {
		t.Fatalf("admin should access analytics export, got %d", resp.StatusCode)
	}
}

// ---------------------------------------------------------------------------
// Return requests
// ---------------------------------------------------------------------------

// TestPermission_AdminReturns_InstructorAllowed returns non-403 for instructor.
func TestPermission_AdminReturns_InstructorAllowed(t *testing.T) {
	app, db, cleanup := newTestApp(t)
	defer cleanup()

	cookie := loginAs(t, app, db, "instructor")
	resp := makeRequest(app, http.MethodGet, "/admin/returns", "", cookie, "")
	if resp.StatusCode == http.StatusForbidden || resp.StatusCode == http.StatusUnauthorized {
		t.Fatalf("instructor should access /admin/returns, got %d", resp.StatusCode)
	}
}

// TestPermission_AdminReturns_StudentForbidden returns 403 for a student.
func TestPermission_AdminReturns_StudentForbidden(t *testing.T) {
	app, db, cleanup := newTestApp(t)
	defer cleanup()

	cookie := loginAs(t, app, db, "student")
	resp := makeRequest(app, http.MethodGet, "/admin/returns", "", cookie, "")
	if resp.StatusCode != http.StatusForbidden && resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("expected 403/401 for student on admin returns, got %d", resp.StatusCode)
	}
}

// ---------------------------------------------------------------------------
// Moderation actions — approve / remove
// ---------------------------------------------------------------------------

// TestPermission_ApproveComment_ModeratorAllowed verifies a moderator can call the approve endpoint.
func TestPermission_ApproveComment_ModeratorAllowed(t *testing.T) {
	app, db, cleanup := newTestApp(t)
	defer cleanup()

	// Create a collapsed comment to approve.
	author := createUser(t, db, "student")
	mat := createMaterial(t, db)
	engRepo := repository.NewEngagementRepository(db)
	comment, err := engRepo.CreateComment(mat.ID, author.ID, "test comment", 0)
	if err != nil {
		t.Fatalf("create comment: %v", err)
	}
	// Manually collapse it.
	if _, err := db.Exec(`UPDATE comments SET status='collapsed' WHERE id=?`, comment.ID); err != nil {
		t.Fatalf("collapse comment: %v", err)
	}

	cookie := loginAs(t, app, db, "moderator")
	resp := makeRequest(app, http.MethodPost,
		fmt.Sprintf("/moderation/%d/approve", comment.ID),
		"", cookie, "", htmx())
	if resp.StatusCode == http.StatusForbidden || resp.StatusCode == http.StatusUnauthorized {
		t.Fatalf("moderator should be allowed to approve, got %d", resp.StatusCode)
	}
}

// TestPermission_ApproveComment_StudentForbidden returns 403.
func TestPermission_ApproveComment_StudentForbidden(t *testing.T) {
	app, db, cleanup := newTestApp(t)
	defer cleanup()

	cookie := loginAs(t, app, db, "student")
	resp := makeRequest(app, http.MethodPost, "/moderation/1/approve", "", cookie, "", htmx())
	if resp.StatusCode != http.StatusForbidden && resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("expected 403/401 for student approving comment, got %d", resp.StatusCode)
	}
}

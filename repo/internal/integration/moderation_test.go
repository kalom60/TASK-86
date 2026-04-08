package integration_test

import (
	"fmt"
	"net/http"
	"testing"

	"w2t86/internal/repository"
)

// TestModerationQueue_Empty verifies GET /moderation for a moderator returns 200.
func TestModerationQueue_Empty(t *testing.T) {
	app, db, cleanup := newTestApp(t)
	defer cleanup()

	modCookie := loginAs(t, app, db, "moderator")

	resp := makeRequest(app, http.MethodGet, "/moderation", "", modCookie, "")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200 for moderator on /moderation, got %d; body: %s",
			resp.StatusCode, readBody(resp))
	}
}

// TestModerationQueue_RequiresModerator verifies that a student cannot access
// GET /moderation and receives 403 Forbidden.
func TestModerationQueue_RequiresModerator(t *testing.T) {
	app, db, cleanup := newTestApp(t)
	defer cleanup()

	studentCookie := loginAs(t, app, db, "student")

	resp := makeRequest(app, http.MethodGet, "/moderation", "", studentCookie, "")
	if resp.StatusCode != http.StatusForbidden {
		t.Fatalf("expected 403 for student on /moderation, got %d", resp.StatusCode)
	}
}

// TestApproveComment verifies POST /moderation/:id/approve transitions a
// collapsed comment to active status in the DB.
func TestApproveComment(t *testing.T) {
	app, db, cleanup := newTestApp(t)
	defer cleanup()

	// Create a comment author.
	author := createTestUser(t, db, "student")
	mat := createTestMaterial(t, db)

	// Create comment and collapse it (set status directly).
	engRepo := repository.NewEngagementRepository(db)
	comment, err := engRepo.CreateComment(mat.ID, author.ID, "This needs review", 0)
	if err != nil {
		t.Fatalf("create comment: %v", err)
	}

	// Set it to collapsed directly in DB.
	if _, err := db.Exec(`UPDATE comments SET status = 'collapsed' WHERE id = ?`, comment.ID); err != nil {
		t.Fatalf("set collapsed: %v", err)
	}

	modCookie := loginAs(t, app, db, "moderator")

	resp := makeRequest(app, http.MethodPost,
		fmt.Sprintf("/moderation/%d/approve", comment.ID),
		"", modCookie, "application/x-www-form-urlencoded", htmxHeaders())

	// On HTMX request, the handler returns 200 with empty body.
	if resp.StatusCode != http.StatusOK && resp.StatusCode != http.StatusFound {
		t.Fatalf("expected 200 or 302 on approve, got %d; body: %s",
			resp.StatusCode, readBody(resp))
	}

	// Verify DB.
	var status string
	if err := db.QueryRow(`SELECT status FROM comments WHERE id = ?`, comment.ID).Scan(&status); err != nil {
		t.Fatalf("query comment: %v", err)
	}
	if status != "active" {
		t.Errorf("expected comment status 'active' after approve, got %q", status)
	}
}

// TestRemoveComment verifies POST /moderation/:id/remove transitions a collapsed
// comment to removed status in the DB.
func TestRemoveComment(t *testing.T) {
	app, db, cleanup := newTestApp(t)
	defer cleanup()

	author := createTestUser(t, db, "student")
	mat := createTestMaterial(t, db)

	engRepo := repository.NewEngagementRepository(db)
	comment, err := engRepo.CreateComment(mat.ID, author.ID, "Spam comment", 0)
	if err != nil {
		t.Fatalf("create comment: %v", err)
	}
	if _, err := db.Exec(`UPDATE comments SET status = 'collapsed' WHERE id = ?`, comment.ID); err != nil {
		t.Fatalf("set collapsed: %v", err)
	}

	modCookie := loginAs(t, app, db, "moderator")

	resp := makeRequest(app, http.MethodPost,
		fmt.Sprintf("/moderation/%d/remove", comment.ID),
		"", modCookie, "application/x-www-form-urlencoded", htmxHeaders())

	if resp.StatusCode != http.StatusOK && resp.StatusCode != http.StatusFound {
		t.Fatalf("expected 200 or 302 on remove, got %d; body: %s",
			resp.StatusCode, readBody(resp))
	}

	var status string
	if err := db.QueryRow(`SELECT status FROM comments WHERE id = ?`, comment.ID).Scan(&status); err != nil {
		t.Fatalf("query comment: %v", err)
	}
	if status != "removed" {
		t.Errorf("expected comment status 'removed' after remove, got %q", status)
	}
}

// TestApproveComment_WrongStatus verifies that approving an already-active
// comment returns 422 (the repository enforces status='collapsed').
func TestApproveComment_WrongStatus(t *testing.T) {
	app, db, cleanup := newTestApp(t)
	defer cleanup()

	author := createTestUser(t, db, "student")
	mat := createTestMaterial(t, db)

	engRepo := repository.NewEngagementRepository(db)
	comment, err := engRepo.CreateComment(mat.ID, author.ID, "Active comment", 0)
	if err != nil {
		t.Fatalf("create comment: %v", err)
	}
	// comment is 'visible' — not collapsed; approve should fail.

	modCookie := loginAs(t, app, db, "moderator")

	resp := makeRequest(app, http.MethodPost,
		fmt.Sprintf("/moderation/%d/approve", comment.ID),
		"", modCookie, "application/x-www-form-urlencoded", htmxHeaders())

	if resp.StatusCode != http.StatusUnprocessableEntity {
		t.Fatalf("expected 422 on approve of non-collapsed comment, got %d", resp.StatusCode)
	}
}

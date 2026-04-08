package integration_test

import (
	"fmt"
	"net/http"
	"strings"
	"testing"
)

// TestListUsers_Admin verifies GET /admin/users returns 200 for an admin.
func TestListUsers_Admin(t *testing.T) {
	app, db, cleanup := newTestApp(t)
	defer cleanup()

	adminCookie := loginAs(t, app, db, "admin")

	resp := makeRequest(app, http.MethodGet, "/admin/users", "", adminCookie, "")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200 for admin on /admin/users, got %d; body: %s",
			resp.StatusCode, readBody(resp))
	}
}

// TestListUsers_NotAdmin verifies GET /admin/users (student) returns 403.
func TestListUsers_NotAdmin(t *testing.T) {
	app, db, cleanup := newTestApp(t)
	defer cleanup()

	studentCookie := loginAs(t, app, db, "student")

	resp := makeRequest(app, http.MethodGet, "/admin/users", "", studentCookie, "")
	if resp.StatusCode != http.StatusForbidden {
		t.Fatalf("expected 403 for student on /admin/users, got %d", resp.StatusCode)
	}
}

// TestCreateUser verifies POST /admin/users creates a user and redirects to the
// new user's profile page.
func TestCreateUser(t *testing.T) {
	app, db, cleanup := newTestApp(t)
	defer cleanup()

	adminCookie := loginAs(t, app, db, "admin")

	body := "username=newstudent123&email=newstudent123@example.com&password=SuperSecurePass1!&role=student"
	resp := makeRequest(app, http.MethodPost, "/admin/users",
		body, adminCookie, "application/x-www-form-urlencoded")

	// On success: redirects to /admin/users/:id (302).
	if resp.StatusCode != http.StatusFound {
		t.Fatalf("expected 302 on create user, got %d; body: %s",
			resp.StatusCode, readBody(resp))
	}

	if resp.StatusCode == http.StatusFound {
		loc := resp.Header.Get("Location")
		if !strings.HasPrefix(loc, "/admin/users/") {
			t.Errorf("expected redirect to /admin/users/:id, got: %s", loc)
		}
	}

	// Verify DB.
	var count int
	if err := db.QueryRow(`SELECT COUNT(*) FROM users WHERE username = 'newstudent123'`).Scan(&count); err != nil {
		t.Fatalf("query user: %v", err)
	}
	if count != 1 {
		t.Errorf("expected 1 user row for newstudent123, got %d", count)
	}
}

// TestUpdateRole verifies POST /admin/users/:id/role changes a user's role.
func TestUpdateRole(t *testing.T) {
	app, db, cleanup := newTestApp(t)
	defer cleanup()

	targetUser := createTestUser(t, db, "student")
	adminCookie := loginAs(t, app, db, "admin")

	body := "role=instructor"
	resp := makeRequest(app, http.MethodPost,
		fmt.Sprintf("/admin/users/%d/role", targetUser.ID),
		body, adminCookie, "application/x-www-form-urlencoded", htmxHeaders())

	if resp.StatusCode != http.StatusOK && resp.StatusCode != http.StatusFound {
		t.Fatalf("expected 200 or 302 on update role, got %d; body: %s",
			resp.StatusCode, readBody(resp))
	}

	// Verify DB.
	var role string
	if err := db.QueryRow(`SELECT role FROM users WHERE id = ?`, targetUser.ID).Scan(&role); err != nil {
		t.Fatalf("query user role: %v", err)
	}
	if role != "instructor" {
		t.Errorf("expected role 'instructor', got %q", role)
	}
}

// TestUnlockUser verifies POST /admin/users/:id/unlock clears the lock on a user.
func TestUnlockUser(t *testing.T) {
	app, db, cleanup := newTestApp(t)
	defer cleanup()

	targetUser := createTestUser(t, db, "student")

	// Lock the user directly.
	if _, err := db.Exec(`UPDATE users SET locked_until = datetime('now', '+15 minutes'), failed_attempts = 5 WHERE id = ?`, targetUser.ID); err != nil {
		t.Fatalf("lock user: %v", err)
	}

	adminCookie := loginAs(t, app, db, "admin")

	resp := makeRequest(app, http.MethodPost,
		fmt.Sprintf("/admin/users/%d/unlock", targetUser.ID),
		"", adminCookie, "application/x-www-form-urlencoded", htmxHeaders())

	if resp.StatusCode != http.StatusOK && resp.StatusCode != http.StatusFound {
		t.Fatalf("expected 200 or 302 on unlock user, got %d; body: %s",
			resp.StatusCode, readBody(resp))
	}

	// Verify DB.
	var lockedUntil *string
	var failedAttempts int
	if err := db.QueryRow(`SELECT locked_until, failed_attempts FROM users WHERE id = ?`, targetUser.ID).Scan(&lockedUntil, &failedAttempts); err != nil {
		t.Fatalf("query user: %v", err)
	}
	if lockedUntil != nil {
		t.Errorf("expected locked_until to be NULL after unlock")
	}
	if failedAttempts != 0 {
		t.Errorf("expected failed_attempts=0 after unlock, got %d", failedAttempts)
	}
}

// TestDuplicatesPage verifies GET /admin/duplicates for an admin returns a
// non-auth-error status.
func TestDuplicatesPage(t *testing.T) {
	app, db, cleanup := newTestApp(t)
	defer cleanup()

	adminCookie := loginAs(t, app, db, "admin")

	resp := makeRequest(app, http.MethodGet, "/admin/duplicates", "", adminCookie, "")
	if resp.StatusCode == http.StatusForbidden {
		t.Fatal("expected non-403 for admin on /admin/duplicates")
	}
	if resp.StatusCode == http.StatusUnauthorized {
		t.Fatal("expected non-401 for admin on /admin/duplicates")
	}
}

// TestAuditLog verifies GET /admin/audit for an admin returns a non-auth-error
// status.
func TestAuditLog(t *testing.T) {
	app, db, cleanup := newTestApp(t)
	defer cleanup()

	adminCookie := loginAs(t, app, db, "admin")

	resp := makeRequest(app, http.MethodGet, "/admin/audit", "", adminCookie, "")
	if resp.StatusCode == http.StatusForbidden {
		t.Fatal("expected non-403 for admin on /admin/audit")
	}
	if resp.StatusCode == http.StatusUnauthorized {
		t.Fatal("expected non-401 for admin on /admin/audit")
	}
}

// TestCreateUser_ShortPassword verifies that creating a user with a password
// shorter than 12 characters returns a non-redirect error response.
func TestCreateUser_ShortPassword(t *testing.T) {
	app, db, cleanup := newTestApp(t)
	defer cleanup()

	adminCookie := loginAs(t, app, db, "admin")

	body := "username=shortpwuser&email=shortpw@example.com&password=short&role=student"
	resp := makeRequest(app, http.MethodPost, "/admin/users",
		body, adminCookie, "application/x-www-form-urlencoded")

	// Should NOT be a 302 redirect (creation failed).
	if resp.StatusCode == http.StatusFound {
		t.Fatal("expected non-302 for short password create user")
	}
}

// TestUpdateRole_NonAdmin verifies that a non-admin (e.g., instructor) cannot
// change a user's role and receives 403.
func TestUpdateRole_NonAdmin(t *testing.T) {
	app, db, cleanup := newTestApp(t)
	defer cleanup()

	targetUser := createTestUser(t, db, "student")
	instructorCookie := loginAs(t, app, db, "instructor")

	resp := makeRequest(app, http.MethodPost,
		fmt.Sprintf("/admin/users/%d/role", targetUser.ID),
		"role=clerk", instructorCookie, "application/x-www-form-urlencoded")

	if resp.StatusCode != http.StatusForbidden {
		t.Fatalf("expected 403 for instructor on update role, got %d", resp.StatusCode)
	}
}

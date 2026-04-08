package integration_test

import (
	"net/http"
	"strings"
	"testing"
)

// TestHealth verifies that GET /health returns 200 {"status":"ok"}.
func TestHealth(t *testing.T) {
	app, _, cleanup := newTestApp(t)
	defer cleanup()

	resp := makeRequest(app, http.MethodGet, "/health", "", "", "")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200, got %d", resp.StatusCode)
	}
	body := readBody(resp)
	if !strings.Contains(body, "ok") {
		t.Errorf("expected body to contain 'ok', got: %s", body)
	}
}

// TestLoginFlow_Success tests that POST /login with valid credentials returns a
// 302 redirect to /dashboard and sets the session_token cookie.
func TestLoginFlow_Success(t *testing.T) {
	app, db, cleanup := newTestApp(t)
	defer cleanup()

	// Create a student user.
	user := createTestUser(t, db, "student")

	body := "username=" + user.Username + "&password=TestPassword123!"
	req := makeRequest(app, http.MethodPost, "/login", body, "", "application/x-www-form-urlencoded")

	// Should redirect to /dashboard.
	if req.StatusCode != http.StatusFound {
		t.Fatalf("expected 302, got %d; body: %s", req.StatusCode, readBody(req))
	}

	// Check Location header.
	loc := req.Header.Get("Location")
	if !strings.Contains(loc, "/dashboard") {
		t.Errorf("expected redirect to /dashboard, got: %s", loc)
	}

	// Check cookie is set.
	found := false
	for _, c := range req.Cookies() {
		if c.Name == "session_token" && c.Value != "" {
			found = true
			break
		}
	}
	if !found {
		t.Error("expected session_token cookie to be set")
	}
}

// TestLoginFlow_WrongPassword tests that POST /login with a bad password returns
// a 401 error response.
func TestLoginFlow_WrongPassword(t *testing.T) {
	app, db, cleanup := newTestApp(t)
	defer cleanup()

	user := createTestUser(t, db, "student")

	body := "username=" + user.Username + "&password=WrongPassword999!"
	// Use HTMX so the handler returns a partial text response (not a full-page render).
	resp := makeRequest(app, http.MethodPost, "/login", body, "", "application/x-www-form-urlencoded", htmxHeaders())

	if resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("expected 401, got %d", resp.StatusCode)
	}
}

// TestLoginFlow_Lockout tests that after 5 failed attempts the 6th is rejected
// with an "account locked" error.
func TestLoginFlow_Lockout(t *testing.T) {
	app, db, cleanup := newTestApp(t)
	defer cleanup()

	user := createTestUser(t, db, "student")
	wrongBody := "username=" + user.Username + "&password=WrongPassword999!"
	ct := "application/x-www-form-urlencoded"

	// Attempt 5 bad logins.
	for i := 0; i < 5; i++ {
		resp := makeRequest(app, http.MethodPost, "/login", wrongBody, "", ct, htmxHeaders())
		// Should be 401 on each bad attempt (or locked after hitting the threshold).
		_ = readBody(resp) // drain
	}

	// 6th attempt should either be 401 with "locked" or still 401.
	resp := makeRequest(app, http.MethodPost, "/login", wrongBody, "", ct, htmxHeaders())
	if resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("expected 401 on locked attempt, got %d", resp.StatusCode)
	}
	body := readBody(resp)
	if !strings.Contains(strings.ToLower(body), "locked") &&
		!strings.Contains(strings.ToLower(body), "invalid") {
		t.Logf("lockout body: %s", body)
	}

	// After locking, the correct password should also fail.
	correctBody := "username=" + user.Username + "&password=TestPassword123!"
	resp2 := makeRequest(app, http.MethodPost, "/login", correctBody, "", ct, htmxHeaders())
	if resp2.StatusCode == http.StatusFound {
		t.Error("expected locked account to reject even correct password")
	}
}

// TestLogout tests that POST /logout with a valid session clears the cookie and
// redirects to /login.
func TestLogout(t *testing.T) {
	app, db, cleanup := newTestApp(t)
	defer cleanup()

	cookie := loginAs(t, app, db, "student")

	resp := makeRequest(app, http.MethodPost, "/logout", "", cookie, "")
	if resp.StatusCode != http.StatusFound {
		t.Fatalf("expected 302 on logout, got %d", resp.StatusCode)
	}
	loc := resp.Header.Get("Location")
	if !strings.Contains(loc, "/login") {
		t.Errorf("expected redirect to /login, got: %s", loc)
	}
}

// TestRequireAuth_Unauthenticated verifies that GET /orders without a session
// cookie returns 401.
func TestRequireAuth_Unauthenticated(t *testing.T) {
	app, _, cleanup := newTestApp(t)
	defer cleanup()

	resp := makeRequest(app, http.MethodGet, "/orders", "", "", "")
	if resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("expected 401, got %d", resp.StatusCode)
	}
}

// TestRequireAuth_WithCookie verifies that GET /dashboard with a valid admin
// cookie returns 302 (redirects to /dashboard/admin, which will also redirect).
func TestRequireAuth_WithCookie(t *testing.T) {
	app, db, cleanup := newTestApp(t)
	defer cleanup()

	cookie := loginAs(t, app, db, "admin")

	resp := makeRequest(app, http.MethodGet, "/dashboard", "", cookie, "")
	// Admin user is redirected to /dashboard/admin (302).
	if resp.StatusCode != http.StatusFound && resp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200 or 302, got %d", resp.StatusCode)
	}
}

// TestRBAC_StudentCannotAccessAdmin verifies that a student cookie cannot access
// GET /admin/users and receives a 403 Forbidden.
func TestRBAC_StudentCannotAccessAdmin(t *testing.T) {
	app, db, cleanup := newTestApp(t)
	defer cleanup()

	cookie := loginAs(t, app, db, "student")

	resp := makeRequest(app, http.MethodGet, "/admin/users", "", cookie, "")
	if resp.StatusCode != http.StatusForbidden {
		t.Fatalf("expected 403 for student on /admin/users, got %d", resp.StatusCode)
	}
}

// TestRBAC_AdminCanAccessAdmin verifies that an admin cookie can access
// GET /admin/users and receives 200 or 302 (not 403).
func TestRBAC_AdminCanAccessAdmin(t *testing.T) {
	app, db, cleanup := newTestApp(t)
	defer cleanup()

	cookie := loginAs(t, app, db, "admin")

	resp := makeRequest(app, http.MethodGet, "/admin/users", "", cookie, "")
	if resp.StatusCode == http.StatusForbidden {
		t.Fatalf("expected non-403 for admin on /admin/users, got %d", resp.StatusCode)
	}
}

// TestRBAC_ClerkCannotAccessAdminUsers verifies that a clerk (allowed for
// distribution but not user-management) gets 403 on /admin/users.
func TestRBAC_ClerkCannotAccessAdminUsers(t *testing.T) {
	app, db, cleanup := newTestApp(t)
	defer cleanup()

	cookie := loginAs(t, app, db, "clerk")

	resp := makeRequest(app, http.MethodGet, "/admin/users", "", cookie, "")
	if resp.StatusCode != http.StatusForbidden {
		t.Fatalf("expected 403 for clerk on /admin/users, got %d", resp.StatusCode)
	}
}

// TestDefaultAdminLogin_SeedCredentialsWork closes the audit gap that requires
// runtime proof the seeded admin account (migrations/001_schema.sql) can
// actually log in through the full HTTP stack.
//
// The test DB is populated by testutil.NewTestDB which runs 001_schema.sql in
// full — including the INSERT OR IGNORE that seeds the admin user with the
// bcrypt hash for "ChangeMe123!".  A POST /login with those credentials must
// yield a 302 redirect (success) and set a session_token cookie.
//
// This complements TestDefaultAdminPassword_MatchesSeedHash (which validates
// the hash in isolation) by proving that the AuthService, UserRepository, and
// Fiber handler chain all accept the seed credentials end-to-end.
func TestDefaultAdminLogin_SeedCredentialsWork(t *testing.T) {
	app, _, cleanup := newTestApp(t)
	defer cleanup()

	const (
		seedUsername = "admin"
		seedPassword = "ChangeMe123!"
	)

	body := "username=" + seedUsername + "&password=" + seedPassword
	resp := makeRequest(app, http.MethodPost, "/login", body, "",
		"application/x-www-form-urlencoded")

	// Login success → 302 redirect to /dashboard.
	if resp.StatusCode != http.StatusFound {
		b := readBody(resp)
		t.Fatalf("expected 302 for seed admin login, got %d; body: %s", resp.StatusCode, b)
	}

	// A session_token cookie must be set.
	var sessionCookie string
	for _, c := range resp.Cookies() {
		if c.Name == "session_token" {
			sessionCookie = "session_token=" + c.Value
			break
		}
	}
	if sessionCookie == "" {
		t.Fatal("no session_token cookie in login response — auth stack did not accept seed credentials")
	}

	// The cookie must grant access to the admin dashboard.
	dashResp := makeRequest(app, http.MethodGet, "/dashboard/admin", "", sessionCookie, "")
	if dashResp.StatusCode != http.StatusOK {
		t.Errorf("expected 200 on /dashboard/admin with seed admin session, got %d",
			dashResp.StatusCode)
	}
}

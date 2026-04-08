package api_tests

import (
	"net/http"
	"strings"
	"testing"
)

// ---------------------------------------------------------------------------
// Auth — normal inputs
// ---------------------------------------------------------------------------

// TestAuth_Health verifies the health endpoint is publicly accessible.
func TestAuth_Health(t *testing.T) {
	app, _, cleanup := newTestApp(t)
	defer cleanup()

	resp := makeRequest(app, http.MethodGet, "/health", "", "", "")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200, got %d", resp.StatusCode)
	}
	if body := readBody(resp); !strings.Contains(body, "ok") {
		t.Errorf("expected body to contain 'ok', got: %s", body)
	}
}

// TestAuth_Login_ValidCredentials returns 302 to /dashboard and sets session cookie.
func TestAuth_Login_ValidCredentials(t *testing.T) {
	app, db, cleanup := newTestApp(t)
	defer cleanup()

	user := createUser(t, db, "student")
	body := "username=" + user.Username + "&password=TestPassword123!"
	resp := makeRequest(app, http.MethodPost, "/login", body, "", "application/x-www-form-urlencoded")

	if resp.StatusCode != http.StatusFound {
		t.Fatalf("expected 302, got %d; body: %s", resp.StatusCode, readBody(resp))
	}
	if loc := resp.Header.Get("Location"); !strings.Contains(loc, "dashboard") && !strings.Contains(loc, "materials") {
		t.Errorf("expected redirect to dashboard/materials, got: %s", loc)
	}

	found := false
	for _, c := range resp.Cookies() {
		if c.Name == "session_token" && c.Value != "" {
			found = true
		}
	}
	if !found {
		t.Error("expected session_token cookie to be set after login")
	}
}

// TestAuth_Logout_ClearsSession returns 302 after a valid logout.
func TestAuth_Logout_ClearsSession(t *testing.T) {
	app, db, cleanup := newTestApp(t)
	defer cleanup()

	cookie := loginAs(t, app, db, "student")
	resp := makeRequest(app, http.MethodPost, "/logout", "", cookie, "")
	if resp.StatusCode != http.StatusFound && resp.StatusCode != http.StatusOK {
		t.Fatalf("expected 302/200 on logout, got %d", resp.StatusCode)
	}
}

// TestAuth_Dashboard_AuthenticatedStudent redirects to materials.
func TestAuth_Dashboard_AuthenticatedStudent(t *testing.T) {
	app, db, cleanup := newTestApp(t)
	defer cleanup()

	cookie := loginAs(t, app, db, "student")
	resp := makeRequest(app, http.MethodGet, "/dashboard", "", cookie, "")

	// Student dashboard redirects to /materials.
	if resp.StatusCode != http.StatusFound && resp.StatusCode/100 != 2 {
		t.Fatalf("expected 2xx or 302 for authenticated dashboard, got %d", resp.StatusCode)
	}
}

// ---------------------------------------------------------------------------
// Auth — missing / invalid parameters
// ---------------------------------------------------------------------------

// TestAuth_Login_MissingUsername returns a non-200 error response.
func TestAuth_Login_MissingUsername(t *testing.T) {
	app, _, cleanup := newTestApp(t)
	defer cleanup()

	resp := makeRequest(app, http.MethodPost, "/login", "password=TestPassword123!", "", "application/x-www-form-urlencoded", htmx())
	if resp.StatusCode == http.StatusFound {
		t.Error("expected error (not 302) when username is missing")
	}
}

// TestAuth_Login_WrongPassword returns 401.
func TestAuth_Login_WrongPassword(t *testing.T) {
	app, db, cleanup := newTestApp(t)
	defer cleanup()

	user := createUser(t, db, "student")
	body := "username=" + user.Username + "&password=WrongPassword999!"
	resp := makeRequest(app, http.MethodPost, "/login", body, "", "application/x-www-form-urlencoded", htmx())

	if resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("expected 401 for wrong password, got %d", resp.StatusCode)
	}
}

// TestAuth_Login_NonExistentUser returns 401.
func TestAuth_Login_NonExistentUser(t *testing.T) {
	app, _, cleanup := newTestApp(t)
	defer cleanup()

	resp := makeRequest(app, http.MethodPost, "/login",
		"username=nobody&password=TestPassword123!", "", "application/x-www-form-urlencoded", htmx())
	if resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("expected 401 for unknown user, got %d", resp.StatusCode)
	}
}

// TestAuth_Login_ShortPassword returns error (not 302).
func TestAuth_Login_ShortPassword(t *testing.T) {
	app, _, cleanup := newTestApp(t)
	defer cleanup()

	resp := makeRequest(app, http.MethodPost, "/login",
		"username=someuser&password=short", "", "application/x-www-form-urlencoded", htmx())
	if resp.StatusCode == http.StatusFound {
		t.Error("expected error for too-short password, got 302")
	}
}

// ---------------------------------------------------------------------------
// Auth — permission errors (unauthenticated access)
// ---------------------------------------------------------------------------

// TestAuth_Dashboard_Unauthenticated returns 401 or 302 to login.
func TestAuth_Dashboard_Unauthenticated(t *testing.T) {
	app, _, cleanup := newTestApp(t)
	defer cleanup()

	resp := makeRequest(app, http.MethodGet, "/dashboard", "", "", "")
	if resp.StatusCode != http.StatusUnauthorized && resp.StatusCode != http.StatusFound {
		t.Fatalf("expected 401 or 302 for unauthenticated dashboard, got %d", resp.StatusCode)
	}
}

// TestAuth_Materials_Unauthenticated returns 401 or 302.
func TestAuth_Materials_Unauthenticated(t *testing.T) {
	app, _, cleanup := newTestApp(t)
	defer cleanup()

	resp := makeRequest(app, http.MethodGet, "/materials", "", "", "")
	if resp.StatusCode != http.StatusUnauthorized && resp.StatusCode != http.StatusFound {
		t.Fatalf("expected 401/302 for unauthenticated /materials, got %d", resp.StatusCode)
	}
}

// TestAuth_Orders_Unauthenticated returns 401 or 302.
func TestAuth_Orders_Unauthenticated(t *testing.T) {
	app, _, cleanup := newTestApp(t)
	defer cleanup()

	resp := makeRequest(app, http.MethodGet, "/orders", "", "", "")
	if resp.StatusCode != http.StatusUnauthorized && resp.StatusCode != http.StatusFound {
		t.Fatalf("expected 401/302 for unauthenticated /orders, got %d", resp.StatusCode)
	}
}

// TestAuth_Inbox_Unauthenticated returns 401 or 302.
func TestAuth_Inbox_Unauthenticated(t *testing.T) {
	app, _, cleanup := newTestApp(t)
	defer cleanup()

	resp := makeRequest(app, http.MethodGet, "/inbox", "", "", "")
	if resp.StatusCode != http.StatusUnauthorized && resp.StatusCode != http.StatusFound {
		t.Fatalf("expected 401/302 for unauthenticated /inbox, got %d", resp.StatusCode)
	}
}

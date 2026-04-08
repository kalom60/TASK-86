package handlers

import (
	"time"

	"github.com/gofiber/fiber/v2"

	"w2t86/internal/observability"
	"w2t86/internal/services"
)

// AuthHandler handles authentication-related HTTP routes.
type AuthHandler struct {
	authService *services.AuthService
}

// NewAuthHandler creates an AuthHandler backed by the given AuthService.
func NewAuthHandler(as *services.AuthService) *AuthHandler {
	return &AuthHandler{authService: as}
}

// LoginPage handles GET /login.
// It renders the "login" template (full page).
func (h *AuthHandler) LoginPage(c *fiber.Ctx) error {
	return c.Render("login", fiber.Map{
		"Title": "Sign In",
		"Error": "",
	}, "layouts/main")
}

// Login handles POST /login.
//   - Reads "username" and "password" form fields.
//   - Calls AuthService.Login.
//   - On success: sets the session_token cookie and redirects to /dashboard.
//   - On failure:
//   - HTMX request (HX-Request: true): returns just the form partial with the
//     inline error so HTMX can swap it in.
//   - Normal request: re-renders the full login page with the error.
func (h *AuthHandler) Login(c *fiber.Ctx) error {
	username := c.FormValue("username")
	password := c.FormValue("password")

	token, user, err := h.authService.Login(username, password)
	if err != nil {
		observability.Auth.Warn("login failed", "username", username, "ip", c.IP(), "reason", "invalid credentials")

		// Use a fixed user-facing message; the service layer already logs the detail.
		errMsg := "Invalid username or password"
		data := fiber.Map{
			"Title":    "Sign In",
			"Error":    errMsg,
			"Username": username,
		}

		// HTMX-aware: return just the form partial so the client can swap it.
		if c.Get("HX-Request") == "true" {
			return c.Status(fiber.StatusUnauthorized).Render("partials/login_form", data)
		}
		return c.Status(fiber.StatusUnauthorized).Render("login", data, "layouts/main")
	}

	observability.Auth.Info("login success", "username", user.Username, "user_id", user.ID, "role", user.Role, "ip", c.IP())

	// Set the session cookie.
	c.Cookie(&fiber.Cookie{
		Name:     "session_token",
		Value:    token,
		Path:     "/",
		HTTPOnly: true,
		SameSite: "Strict",
		Expires:  time.Now().Add(24 * time.Hour),
	})

	return c.Redirect("/dashboard", fiber.StatusFound)
}

// Logout handles POST /logout.
// Clears the session_token cookie, deletes the server-side session, and
// redirects to /login.
func (h *AuthHandler) Logout(c *fiber.Ctx) error {
	token := c.Cookies("session_token")

	// Attempt to delete the session from the database; ignore errors so the
	// user is always logged out from the client's perspective.
	if token != "" {
		if err := h.authService.Logout(token); err != nil {
			observability.Auth.Warn("logout session delete failed", "ip", c.IP(), "error", err)
		}
	}

	// Expire the cookie immediately.
	c.Cookie(&fiber.Cookie{
		Name:     "session_token",
		Value:    "",
		Path:     "/",
		HTTPOnly: true,
		SameSite: "Strict",
		Expires:  time.Unix(0, 0),
	})

	return c.Redirect("/login", fiber.StatusFound)
}

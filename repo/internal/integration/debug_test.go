package integration_test

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gofiber/fiber/v2"
	"github.com/gofiber/template/html/v2"

	"w2t86/internal/config"
	"w2t86/internal/handlers"
	"w2t86/internal/middleware"
	"w2t86/internal/repository"
	"w2t86/internal/services"
	"w2t86/internal/testutil"
)

// TestModerationDebug isolates the moderator-access issue.
func TestModerationDebug(t *testing.T) {
	db := testutil.NewTestDB(t)

	userRepo := repository.NewUserRepository(db)
	sessionRepo := repository.NewSessionRepository(db)
	cfg := &config.Config{SessionSecret: "test"}
	authSvc := services.NewAuthService(userRepo, sessionRepo, cfg)

	moderationRepo := repository.NewModerationRepository(db)
	messagingRepo := repository.NewMessagingRepository(db)
	modSvc := services.NewModerationService(moderationRepo)
	msgSvc := services.NewMessagingService(messagingRepo)
	modHandler := handlers.NewModerationHandler(modSvc, msgSvc)
	authHandler := handlers.NewAuthHandler(authSvc)
	authMiddleware := middleware.NewAuthMiddleware(sessionRepo, userRepo)

	engine := html.New("../../web/templates", ".html")
	engine.AddFunc("mul", func(a, b float64) float64 { return a * b })
	engine.AddFunc("add", func(a, b int) int { return a + b })
	engine.AddFunc("sub", func(a, b float64) float64 { return a - b })
	engine.AddFunc("div", func(a, b float64) float64 {
		if b == 0 {
			return 0
		}
		return a / b
	})
	engine.AddFunc("float64", func(v interface{}) float64 {
		switch n := v.(type) {
		case int:
			return float64(n)
		case int64:
			return float64(n)
		case float64:
			return n
		}
		return 0
	})
	engine.AddFunc("dict", func(values ...interface{}) (map[string]interface{}, error) {
		if len(values)%2 != 0 {
			return nil, fmt.Errorf("dict requires even number of arguments")
		}
		d := make(map[string]interface{}, len(values)/2)
		for i := 0; i < len(values); i += 2 {
			key, ok := values[i].(string)
			if !ok {
				return nil, fmt.Errorf("dict keys must be strings")
			}
			d[key] = values[i+1]
		}
		return d, nil
	})
	engine.AddFunc("dec", func(n int) int { return n - 1 })
	engine.AddFunc("inc", func(n int) int { return n + 1 })
	engine.AddFunc("deref", func(v interface{}) interface{} { return v })
	engine.AddFunc("hourRange", func() []int {
		hours := make([]int, 24)
		for i := range hours {
			hours[i] = i
		}
		return hours
	})
	app := fiber.New(fiber.Config{
		Views: engine,
		ErrorHandler: func(c *fiber.Ctx, err error) error {
			code := fiber.StatusInternalServerError
			if e, ok := err.(*fiber.Error); ok {
				code = e.Code
			}
			return c.Status(code).SendString(fmt.Sprintf("ERR: %v", err))
		},
	})

	app.Post("/login", authHandler.Login)

	modGroup := app.Group("", authMiddleware.RequireAuth(), middleware.RequireRole("moderator", "admin"))
	modGroup.Get("/moderation", modHandler.Queue)

	// Create moderator user.
	u, err := authSvc.Register("moddebug", "moddebug@test.com", "TestPassword123!", "moderator")
	if err != nil {
		t.Fatalf("register: %v", err)
	}
	t.Logf("created user id=%d role=%q", u.ID, u.Role)

	// Verify role in DB.
	var dbRole string
	if err := db.QueryRow("SELECT role FROM users WHERE id=?", u.ID).Scan(&dbRole); err != nil {
		t.Fatalf("query role: %v", err)
	}
	t.Logf("DB role: %q", dbRole)

	// Login.
	loginBody := "username=moddebug&password=TestPassword123!"
	req := httptest.NewRequest(http.MethodPost, "/login", strings.NewReader(loginBody))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	resp, err := app.Test(req, -1)
	if err != nil {
		t.Fatalf("login test: %v", err)
	}
	t.Logf("Login status: %d", resp.StatusCode)

	var cookieVal string
	for _, c := range resp.Cookies() {
		if c.Name == "session_token" {
			cookieVal = c.Value
		}
	}
	if cookieVal == "" {
		b := readBody(resp)
		t.Fatalf("no session cookie; login status %d body: %s", resp.StatusCode, b)
	}
	t.Logf("Got session cookie (len=%d)", len(cookieVal))

	// Verify session in DB.
	var sessionCount int
	db.QueryRow("SELECT COUNT(*) FROM sessions").Scan(&sessionCount)
	t.Logf("Sessions in DB: %d", sessionCount)

	// Hit /moderation.
	req2 := httptest.NewRequest(http.MethodGet, "/moderation", nil)
	req2.Header.Set("Cookie", "session_token="+cookieVal)
	resp2, err := app.Test(req2, -1)
	if err != nil {
		t.Fatalf("moderation test: %v", err)
	}
	b2 := readBody(resp2)
	t.Logf("/moderation status: %d body: %s", resp2.StatusCode, b2[:min(len(b2), 200)])
}

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}

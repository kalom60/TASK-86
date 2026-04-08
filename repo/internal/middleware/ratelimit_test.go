package middleware_test

import (
	"io"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/gofiber/fiber/v2"

	"w2t86/internal/middleware"
)

// makeApp builds a tiny Fiber app that uses the given RateLimiter middleware
// and a fixed key function.  Every request counts against the same key "testkey".
func makeApp(rl *middleware.RateLimiter) *fiber.App {
	app := fiber.New(fiber.Config{DisableStartupMessage: true})
	app.Use(rl.Middleware(func(_ *fiber.Ctx) string { return "testkey" }))
	app.Get("/", func(c *fiber.Ctx) error {
		return c.SendString("ok")
	})
	return app
}

// doGet fires a GET / against app and returns the status code.
func doGet(t *testing.T, app *fiber.App) int {
	t.Helper()
	req := httptest.NewRequest("GET", "/", nil)
	resp, err := app.Test(req, -1)
	if err != nil {
		t.Fatalf("app.Test: %v", err)
	}
	io.Copy(io.Discard, resp.Body) //nolint:errcheck
	resp.Body.Close()
	return resp.StatusCode
}

func TestRateLimiter_AllowsUnderLimit(t *testing.T) {
	// limit=5, window=1min — four requests must all pass
	rl := middleware.NewRateLimiter(5, time.Minute)
	app := makeApp(rl)

	for i := 0; i < 4; i++ {
		if code := doGet(t, app); code != fiber.StatusOK {
			t.Errorf("request %d: expected 200, got %d", i+1, code)
		}
	}
}

func TestRateLimiter_BlocksAtLimit(t *testing.T) {
	// limit=5; the 6th request should be blocked
	rl := middleware.NewRateLimiter(5, time.Minute)
	app := makeApp(rl)

	for i := 0; i < 5; i++ {
		if code := doGet(t, app); code != fiber.StatusOK {
			t.Errorf("request %d: expected 200, got %d", i+1, code)
		}
	}
	if code := doGet(t, app); code != fiber.StatusTooManyRequests {
		t.Errorf("6th request: expected 429, got %d", code)
	}
}

func TestRateLimiter_WindowExpiry(t *testing.T) {
	// Use a very short window (50ms) so we can exhaust it and then start fresh.
	rl := middleware.NewRateLimiter(3, 50*time.Millisecond)
	app := makeApp(rl)

	// Fill the window.
	for i := 0; i < 3; i++ {
		doGet(t, app)
	}
	// 4th request should be blocked.
	if code := doGet(t, app); code != fiber.StatusTooManyRequests {
		t.Errorf("expected 429 when limit reached, got %d", code)
	}

	// Wait for the window to expire.
	time.Sleep(60 * time.Millisecond)

	// Now it should pass again.
	if code := doGet(t, app); code != fiber.StatusOK {
		t.Errorf("after window expiry: expected 200, got %d", code)
	}
}

func TestRateLimiter_DifferentKeys_Independent(t *testing.T) {
	rl := middleware.NewRateLimiter(3, time.Minute)

	// App A uses key "alpha"
	appA := fiber.New(fiber.Config{DisableStartupMessage: true})
	appA.Use(rl.Middleware(func(_ *fiber.Ctx) string { return "alpha" }))
	appA.Get("/", func(c *fiber.Ctx) error { return c.SendString("ok") })

	// App B uses key "beta"
	appB := fiber.New(fiber.Config{DisableStartupMessage: true})
	appB.Use(rl.Middleware(func(_ *fiber.Ctx) string { return "beta" }))
	appB.Get("/", func(c *fiber.Ctx) error { return c.SendString("ok") })

	// Exhaust key "alpha"
	for i := 0; i < 3; i++ {
		doGet(t, appA)
	}
	if code := doGet(t, appA); code != fiber.StatusTooManyRequests {
		t.Errorf("alpha: expected 429, got %d", code)
	}

	// Key "beta" is independent — first request should pass
	if code := doGet(t, appB); code != fiber.StatusOK {
		t.Errorf("beta: expected 200 (independent from alpha), got %d", code)
	}
}

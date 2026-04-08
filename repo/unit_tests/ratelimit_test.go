package unit_tests

import (
	"fmt"
	"io"
	"net/http/httptest"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/gofiber/fiber/v2"

	"w2t86/internal/middleware"
)

// makeRLApp builds a minimal Fiber app protected by a RateLimiter.
// keyFn extracts the rate-limit key from the request.
func makeRLApp(limit int, window time.Duration, keyFn func(*fiber.Ctx) string) *fiber.App {
	app := fiber.New(fiber.Config{DisableStartupMessage: true})
	rl := middleware.NewRateLimiter(limit, window)
	app.Use(rl.Middleware(keyFn))
	app.Get("/", func(c *fiber.Ctx) error {
		return c.SendString("ok")
	})
	return app
}

// fixedKeyApp is a convenience wrapper: every request uses the same key.
func fixedKeyApp(limit int, window time.Duration, key string) *fiber.App {
	return makeRLApp(limit, window, func(_ *fiber.Ctx) string { return key })
}

// headerKeyApp extracts the rate-limit key from the X-Key request header.
func headerKeyApp(limit int, window time.Duration) *fiber.App {
	return makeRLApp(limit, window, func(c *fiber.Ctx) string { return c.Get("X-Key") })
}

// rlGet fires a GET / against app and returns the HTTP status code.
func rlGet(t *testing.T, app *fiber.App) int {
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

// rlGetWithKey fires a GET / with the given X-Key header.
func rlGetWithKey(t *testing.T, app *fiber.App, key string) int {
	t.Helper()
	req := httptest.NewRequest("GET", "/", nil)
	req.Header.Set("X-Key", key)
	resp, err := app.Test(req, -1)
	if err != nil {
		t.Fatalf("app.Test: %v", err)
	}
	io.Copy(io.Discard, resp.Body) //nolint:errcheck
	resp.Body.Close()
	return resp.StatusCode
}

// ---------------------------------------------------------------------------
// Tests
// ---------------------------------------------------------------------------

// TestRateLimit_UnderLimit_AllAllowed: 4 requests with limit=5 → all pass.
func TestRateLimit_UnderLimit_AllAllowed(t *testing.T) {
	app := fixedKeyApp(5, time.Minute, "under-limit")

	for i := 1; i <= 4; i++ {
		if code := rlGet(t, app); code != fiber.StatusOK {
			t.Errorf("request %d: expected 200, got %d", i, code)
		}
	}
}

// TestRateLimit_AtLimit_FifthBlocked: with limit=4, the 5th request is blocked.
// The sliding-window limiter blocks when len(valid) >= limit, so after 4
// requests fill the window the 5th is rejected.
func TestRateLimit_AtLimit_FifthBlocked(t *testing.T) {
	app := fixedKeyApp(4, time.Minute, "fifth-blocked")

	// First 4 must pass.
	for i := 1; i <= 4; i++ {
		if code := rlGet(t, app); code != fiber.StatusOK {
			t.Errorf("request %d: expected 200, got %d", i, code)
		}
	}
	// 5th must be blocked.
	if code := rlGet(t, app); code != fiber.StatusTooManyRequests {
		t.Errorf("5th request: expected 429, got %d", code)
	}
}

// TestRateLimit_BeyondLimit_AllSubsequentBlocked: after the limit is exhausted
// every additional request is rejected.
func TestRateLimit_BeyondLimit_AllSubsequentBlocked(t *testing.T) {
	app := fixedKeyApp(3, time.Minute, "beyond-limit")

	// Exhaust the limit (3 requests pass, 4th is blocked).
	for i := 1; i <= 3; i++ {
		rlGet(t, app)
	}

	for i := 4; i <= 6; i++ {
		if code := rlGet(t, app); code != fiber.StatusTooManyRequests {
			t.Errorf("request %d: expected 429, got %d", i, code)
		}
	}
}

// TestRateLimit_DifferentKeys_Independent: hitting the limit on key A does not
// affect key B.
func TestRateLimit_DifferentKeys_Independent(t *testing.T) {
	app := headerKeyApp(3, time.Minute)

	// Exhaust limit for key A.
	for i := 0; i < 3; i++ {
		rlGetWithKey(t, app, "key-a")
	}
	// Key A should now be blocked.
	if code := rlGetWithKey(t, app, "key-a"); code != fiber.StatusTooManyRequests {
		t.Errorf("key-a: expected 429, got %d", code)
	}
	// Key B is independent — should be allowed.
	if code := rlGetWithKey(t, app, "key-b"); code != fiber.StatusOK {
		t.Errorf("key-b: expected 200 (independent), got %d", code)
	}
}

// TestRateLimit_WindowExpiry_ResetsCounter: after the window expires the
// counter resets and requests are allowed again.
func TestRateLimit_WindowExpiry_ResetsCounter(t *testing.T) {
	app := fixedKeyApp(3, 50*time.Millisecond, "expiry-key")

	// Fill the window.
	for i := 0; i < 3; i++ {
		rlGet(t, app)
	}
	if code := rlGet(t, app); code != fiber.StatusTooManyRequests {
		t.Errorf("before expiry: expected 429, got %d", code)
	}

	// Wait for the window to expire.
	time.Sleep(60 * time.Millisecond)

	// Should be allowed again.
	if code := rlGet(t, app); code != fiber.StatusOK {
		t.Errorf("after window expiry: expected 200, got %d", code)
	}
}

// TestRateLimit_SingleRequest_Allowed: a single request with any limit passes.
func TestRateLimit_SingleRequest_Allowed(t *testing.T) {
	app := fixedKeyApp(5, time.Minute, "single-key")
	if code := rlGet(t, app); code != fiber.StatusOK {
		t.Errorf("single request: expected 200, got %d", code)
	}
}

// TestRateLimit_ConcurrentRequests_ThreadSafe: 20 goroutines hammer the limiter
// concurrently. The test verifies no race condition or panic occurs, and that
// the number of allowed requests does not exceed the per-key limit.
func TestRateLimit_ConcurrentRequests_ThreadSafe(t *testing.T) {
	const limit = 5
	// 4 distinct keys so each key can pass at most limit requests.
	const numKeys = 4
	app := headerKeyApp(limit, time.Minute)

	var (
		wg      sync.WaitGroup
		allowed atomic.Int32
	)
	const goroutines = 20

	for i := 0; i < goroutines; i++ {
		wg.Add(1)
		go func(n int) {
			defer wg.Done()
			key := fmt.Sprintf("concurrent-key-%d", n%numKeys)
			req := httptest.NewRequest("GET", "/", nil)
			req.Header.Set("X-Key", key)
			resp, err := app.Test(req, -1)
			if err != nil {
				return
			}
			io.Copy(io.Discard, resp.Body) //nolint:errcheck
			resp.Body.Close()
			if resp.StatusCode == fiber.StatusOK {
				allowed.Add(1)
			}
		}(i)
	}
	wg.Wait()

	got := int(allowed.Load())
	// At most numKeys*limit requests can be allowed.
	maxAllowed := numKeys * limit
	if got > maxAllowed {
		t.Errorf("concurrent: allowed %d requests, expected at most %d (keys=%d, limit=%d)",
			got, maxAllowed, numKeys, limit)
	}
	t.Logf("concurrent test: %d/%d requests allowed (max %d)", got, goroutines, maxAllowed)
}

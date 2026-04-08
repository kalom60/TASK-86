package middleware

import (
	"sync"
	"time"

	"github.com/gofiber/fiber/v2"

	"w2t86/internal/models"
	"w2t86/internal/observability"
)

// RateLimiter implements an in-memory sliding-window rate limiter keyed on an
// arbitrary string derived from each request.
type RateLimiter struct {
	mu      sync.Mutex
	windows map[string][]time.Time
	limit   int
	window  time.Duration
}

// NewRateLimiter creates a RateLimiter that allows at most limit requests per
// window duration for each distinct key.
func NewRateLimiter(limit int, window time.Duration) *RateLimiter {
	return &RateLimiter{
		windows: make(map[string][]time.Time),
		limit:   limit,
		window:  window,
	}
}

// Middleware returns a Fiber handler that applies the sliding-window limit.
// keyFn extracts a string key from the request context (e.g. user ID, IP).
// Returns 429 JSON {"error": "rate limit exceeded"} when the limit is reached.
func (rl *RateLimiter) Middleware(keyFn func(*fiber.Ctx) string) fiber.Handler {
	return func(c *fiber.Ctx) error {
		key := keyFn(c)
		if key == "" {
			// No key — cannot rate-limit; pass through.
			return c.Next()
		}

		now := time.Now()
		cutoff := now.Add(-rl.window)

		rl.mu.Lock()
		// Evict timestamps outside the sliding window.
		times := rl.windows[key]
		valid := times[:0]
		for _, t := range times {
			if t.After(cutoff) {
				valid = append(valid, t)
			}
		}

		if len(valid) >= rl.limit {
			rl.mu.Unlock()
			observability.Security.Warn("rate limit exceeded", "key", key, "path", c.Path())
			return c.Status(fiber.StatusTooManyRequests).JSON(fiber.Map{"error": "rate limit exceeded"})
		}

		rl.windows[key] = append(valid, now)
		rl.mu.Unlock()

		return c.Next()
	}
}

// CommentRateLimit returns a ready-to-use Fiber middleware that enforces a
// limit of 5 comment requests per 10 minutes, keyed by the authenticated user's
// ID stored in c.Locals("user").
func CommentRateLimit() fiber.Handler {
	rl := NewRateLimiter(5, 10*time.Minute)
	return rl.Middleware(func(c *fiber.Ctx) string {
		user, ok := c.Locals("user").(*models.User)
		if !ok || user == nil {
			return ""
		}
		// Convert int64 ID to a string key without importing fmt (strconv is lighter).
		return int64ToStr(user.ID)
	})
}

// int64ToStr converts an int64 to its decimal string representation without
// importing fmt to keep this file dependency-free.
func int64ToStr(n int64) string {
	if n == 0 {
		return "0"
	}
	neg := n < 0
	if neg {
		n = -n
	}
	buf := make([]byte, 0, 20)
	for n > 0 {
		buf = append(buf, byte('0'+n%10))
		n /= 10
	}
	// Reverse.
	for i, j := 0, len(buf)-1; i < j; i, j = i+1, j-1 {
		buf[i], buf[j] = buf[j], buf[i]
	}
	if neg {
		return "-" + string(buf)
	}
	return string(buf)
}

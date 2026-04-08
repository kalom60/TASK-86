package observability

import (
	"crypto/rand"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"github.com/gofiber/fiber/v2"
)

// RequestID middleware generates a random hex request ID, stores it in
// c.Locals("request_id"), and sets the X-Request-ID response header.
func RequestID() fiber.Handler {
	return func(c *fiber.Ctx) error {
		// Reuse an existing header if forwarded by a proxy.
		id := c.Get("X-Request-ID")
		if id == "" {
			b := make([]byte, 16)
			if _, err := rand.Read(b); err == nil {
				id = fmt.Sprintf("%x", b)
			} else {
				id = fmt.Sprintf("%d", time.Now().UnixNano())
			}
		}
		c.Locals("request_id", id)
		c.Set("X-Request-ID", id)
		return c.Next()
	}
}

// RequestLogger returns a Fiber middleware that logs every request using the
// HTTP category logger.
//
// Log level rules:
//   - 2xx / 3xx → INFO
//   - 4xx        → WARN
//   - 5xx        → ERROR
//
// Paths /health and /static/ are skipped.
func RequestLogger() fiber.Handler {
	return func(c *fiber.Ctx) error {
		path := c.Path()
		if path == "/health" || strings.HasPrefix(path, "/static/") {
			return c.Next()
		}

		start := time.Now()

		// Count every request, regardless of outcome.
		M.RequestsTotal.Add(1)

		err := c.Next()

		status := c.Response().StatusCode()
		latencyMs := time.Since(start).Milliseconds()

		// Collect optional request ID and user ID.
		reqID, _ := c.Locals("request_id").(string)
		var userID int64
		if u := c.Locals("user"); u != nil {
			// Avoid importing models — use a type assertion via an interface.
			type hasID interface{ GetID() int64 }
			if h, ok := u.(hasID); ok {
				userID = h.GetID()
			}
		}

		attrs := []any{
			"method", c.Method(),
			"path", path,
			"status", status,
			"latency_ms", latencyMs,
			"request_id", reqID,
		}
		if userID != 0 {
			attrs = append(attrs, "user_id", userID)
		}

		switch {
		case status >= 500:
			M.RequestErrors.Add(1)
			HTTP.Log(c.Context(), slog.LevelError, "request", attrs...)
		case status >= 400:
			HTTP.Log(c.Context(), slog.LevelWarn, "request", attrs...)
		default:
			HTTP.Log(c.Context(), slog.LevelInfo, "request", attrs...)
		}

		return err
	}
}

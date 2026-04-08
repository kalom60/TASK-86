package handlers

import (
	"log/slog"

	"github.com/gofiber/fiber/v2"
)

// APIError is the standard JSON error body returned by all endpoints.
// Shape: {"code": 400, "msg": "Invalid material ID"}
type APIError struct {
	Code int    `json:"code"`
	Msg  string `json:"msg"`
}

// apiErr sends a JSON error response. Never call this with a raw DB/service error message.
func apiErr(c *fiber.Ctx, status int, msg string) error {
	return c.Status(status).JSON(APIError{Code: status, Msg: msg})
}

// htmxErr returns an error for HTMX requests: renders the "partials/error_inline"
// template if it is an HTMX request, otherwise returns a JSON APIError.
// Use this for all form submission errors.
func htmxErr(c *fiber.Ctx, status int, msg string) error {
	if c.Get("HX-Request") == "true" {
		return c.Status(status).Render("partials/error_inline", fiber.Map{"Msg": msg})
	}
	return apiErr(c, status, msg)
}

// internalErr logs the real error (with context attrs) and returns a generic 500.
// Never expose err.Error() to the client.
func internalErr(c *fiber.Ctx, logger *slog.Logger, msg string, err error, attrs ...any) error {
	args := append([]any{"error", err, "path", c.Path(), "method", c.Method()}, attrs...)
	logger.Error(msg, args...)
	if c.Get("HX-Request") == "true" {
		return c.Status(500).Render("partials/error_inline", fiber.Map{"Msg": "An unexpected error occurred. Please try again."})
	}
	return apiErr(c, 500, "An unexpected error occurred. Please try again.")
}

package middleware

import (
	"github.com/gofiber/fiber/v2"

	"w2t86/internal/observability"
)

// RequireRole returns a Fiber middleware that enforces role-based access
// control. It must be placed after RequireAuth in the handler chain so that
// c.Locals("user") is already populated.
//
// If the authenticated user's role is not in the allowed roles list, the
// handler responds with 403 JSON {"error": "forbidden"} and halts the chain.
func RequireRole(roles ...string) fiber.Handler {
	// Build a set for O(1) lookup.
	allowed := make(map[string]struct{}, len(roles))
	for _, r := range roles {
		allowed[r] = struct{}{}
	}

	return func(c *fiber.Ctx) error {
		user := GetUser(c)
		if user == nil {
			return c.Status(fiber.StatusForbidden).JSON(fiber.Map{"error": "forbidden"})
		}

		if _, ok := allowed[user.Role]; !ok {
			observability.Security.Warn("rbac denied",
				"user_id", user.ID,
				"role", user.Role,
				"required", roles,
				"path", c.Path(),
			)
			return c.Status(fiber.StatusForbidden).JSON(fiber.Map{"error": "forbidden"})
		}

		return c.Next()
	}
}

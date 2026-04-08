package middleware

import (
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"errors"
	"time"

	"github.com/gofiber/fiber/v2"

	"w2t86/internal/models"
	"w2t86/internal/repository"
)

// AuthMiddleware validates session cookies and loads the current user into
// request locals.
type AuthMiddleware struct {
	sessionRepo *repository.SessionRepository
	userRepo    *repository.UserRepository
}

// NewAuthMiddleware creates an AuthMiddleware from the provided repositories.
func NewAuthMiddleware(sr *repository.SessionRepository, ur *repository.UserRepository) *AuthMiddleware {
	return &AuthMiddleware{sessionRepo: sr, userRepo: ur}
}

// RequireAuth is a Fiber middleware handler that:
//  1. Reads the "session_token" cookie.
//  2. Hashes it with SHA-256.
//  3. Looks up the session in the database.
//  4. Verifies the session has not expired.
//  5. Loads the associated user and stores it in c.Locals("user").
//
// Returns 401 JSON {"error": "unauthorized"} on any failure.
func (m *AuthMiddleware) RequireAuth() fiber.Handler {
	return func(c *fiber.Ctx) error {
		token := c.Cookies("session_token")
		if token == "" {
			return c.Status(fiber.StatusUnauthorized).JSON(fiber.Map{"error": "unauthorized"})
		}

		// Hash the raw token the same way it was stored on login.
		hash := hashToken(token)

		session, err := m.sessionRepo.GetByTokenHash(hash)
		if err != nil {
			if errors.Is(err, sql.ErrNoRows) {
				return c.Status(fiber.StatusUnauthorized).JSON(fiber.Map{"error": "unauthorized"})
			}
			return c.Status(fiber.StatusUnauthorized).JSON(fiber.Map{"error": "unauthorized"})
		}

		// Parse expires_at stored as RFC3339 text.
		expiresAt, err := time.Parse(time.RFC3339, session.ExpiresAt)
		if err != nil {
			return c.Status(fiber.StatusUnauthorized).JSON(fiber.Map{"error": "unauthorized"})
		}
		if time.Now().UTC().After(expiresAt) {
			return c.Status(fiber.StatusUnauthorized).JSON(fiber.Map{"error": "unauthorized"})
		}

		user, err := m.userRepo.GetByID(session.UserID)
		if err != nil {
			if errors.Is(err, sql.ErrNoRows) {
				return c.Status(fiber.StatusUnauthorized).JSON(fiber.Map{"error": "unauthorized"})
			}
			return c.Status(fiber.StatusUnauthorized).JSON(fiber.Map{"error": "unauthorized"})
		}

		c.Locals("user", user)
		return c.Next()
	}
}

// GetUser extracts the authenticated *models.User from fiber.Ctx locals.
// Returns nil when called outside of a RequireAuth-protected route.
func GetUser(c *fiber.Ctx) *models.User {
	u, _ := c.Locals("user").(*models.User)
	return u
}

// hashToken returns the lowercase hex SHA-256 digest of token.
func hashToken(token string) string {
	sum := sha256.Sum256([]byte(token))
	return hex.EncodeToString(sum[:])
}

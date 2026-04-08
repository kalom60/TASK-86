package services

import (
	"crypto/rand"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"errors"
	"fmt"
	"time"

	"w2t86/internal/config"
	"w2t86/internal/crypto"
	"w2t86/internal/models"
	"w2t86/internal/observability"
	"w2t86/internal/repository"
)

const (
	maxFailedAttempts = 5
	lockDuration      = 15 * time.Minute
	sessionDuration   = 24 * time.Hour
	minPasswordLen    = 12
)

// AuthService handles user registration, login, and logout.
type AuthService struct {
	userRepo    *repository.UserRepository
	sessionRepo *repository.SessionRepository
	cfg         *config.Config
}

// NewAuthService creates an AuthService with the provided dependencies.
func NewAuthService(
	ur *repository.UserRepository,
	sr *repository.SessionRepository,
	cfg *config.Config,
) *AuthService {
	return &AuthService{userRepo: ur, sessionRepo: sr, cfg: cfg}
}

// Login authenticates a user by username and password.
//
// Rules:
//   - Account must exist and not be soft-deleted.
//   - If locked_until is in the future, return error "account locked".
//   - On password mismatch, increment failed_attempts.
//     On the 5th failure, lock for 15 minutes and return "account locked".
//   - On success, reset failed attempts, create a 24-hour session, and return
//     the raw 32-byte hex token.
func (s *AuthService) Login(username, password string) (token string, user *models.User, err error) {
	user, err = s.userRepo.GetByUsername(username)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return "", nil, errors.New("invalid username or password")
		}
		return "", nil, fmt.Errorf("auth: login: %w", err)
	}

	// Check if the account is currently locked.
	if user.LockedUntil != nil {
		until, parseErr := time.Parse(time.RFC3339, *user.LockedUntil)
		if parseErr == nil && time.Now().UTC().Before(until) {
			observability.Security.Warn("login attempt on locked account", "username", username)
			return "", nil, errors.New("account locked")
		}
	}

	// Verify password.
	if !crypto.CheckPassword(user.PasswordHash, password) {
		observability.Auth.Warn("login failed", "username", username, "reason", "wrong_password")
		observability.M.LoginFailures.Add(1)

		// Increment counter first so we can check the new value.
		if incErr := s.userRepo.IncrementFailedAttempts(user.ID); incErr != nil {
			return "", nil, fmt.Errorf("auth: login: increment attempts: %w", incErr)
		}

		newAttempts := user.FailedAttempts + 1
		if newAttempts >= maxFailedAttempts {
			lockUntil := time.Now().UTC().Add(lockDuration)
			if lockErr := s.userRepo.LockUntil(user.ID, lockUntil); lockErr != nil {
				return "", nil, fmt.Errorf("auth: login: lock account: %w", lockErr)
			}
			observability.Security.Warn("account locked", "username", username, "user_id", user.ID)
			return "", nil, errors.New("account locked")
		}

		return "", nil, errors.New("invalid username or password")
	}

	// Password matched — reset failure tracking.
	if resetErr := s.userRepo.ResetFailedAttempts(user.ID); resetErr != nil {
		return "", nil, fmt.Errorf("auth: login: reset attempts: %w", resetErr)
	}

	// Generate a 32-byte random token encoded as lowercase hex (64 chars).
	rawToken, err := generateToken()
	if err != nil {
		return "", nil, fmt.Errorf("auth: login: generate token: %w", err)
	}

	tokenHash := hashToken(rawToken)
	expiresAt := time.Now().UTC().Add(sessionDuration)

	if _, err = s.sessionRepo.Create(user.ID, tokenHash, expiresAt); err != nil {
		return "", nil, fmt.Errorf("auth: login: create session: %w", err)
	}

	// Reload user so caller sees the fresh state.
	user, err = s.userRepo.GetByID(user.ID)
	if err != nil {
		return "", nil, fmt.Errorf("auth: login: reload user: %w", err)
	}

	observability.Auth.Info("login success", "username", username, "user_id", user.ID)
	observability.M.LoginSuccess.Add(1)

	return rawToken, user, nil
}

// Logout invalidates the session identified by the raw token.
func (s *AuthService) Logout(token string) error {
	hash := hashToken(token)
	if err := s.sessionRepo.Delete(hash); err != nil {
		return fmt.Errorf("auth: logout: %w", err)
	}
	observability.Auth.Info("logout", "token_hash_prefix", hash[:8])
	return nil
}

// Register validates inputs, hashes the password, and creates a new user.
// Password must be at least 12 characters long.
func (s *AuthService) Register(username, email, password, role string) (*models.User, error) {
	if len(password) < minPasswordLen {
		return nil, fmt.Errorf("auth: register: password must be at least %d characters", minPasswordLen)
	}

	hash, err := crypto.HashPassword(password)
	if err != nil {
		return nil, fmt.Errorf("auth: register: %w", err)
	}

	user, err := s.userRepo.Create(username, email, hash, role)
	if err != nil {
		return nil, fmt.Errorf("auth: register: %w", err)
	}

	observability.Auth.Info("user registered", "username", username, "role", role)
	return user, nil
}

// ---------------------------------------------------------------
// helpers
// ---------------------------------------------------------------

// generateToken returns a cryptographically-random 32-byte value encoded as
// a 64-character lowercase hex string.
func generateToken() (string, error) {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return hex.EncodeToString(b), nil
}

// hashToken returns the SHA-256 hex digest of token.
func hashToken(token string) string {
	sum := sha256.Sum256([]byte(token))
	return hex.EncodeToString(sum[:])
}

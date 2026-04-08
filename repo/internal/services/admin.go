package services

import (
	"fmt"

	"w2t86/internal/crypto"
	"w2t86/internal/models"
	"w2t86/internal/observability"
	"w2t86/internal/repository"
)

// AdminService orchestrates admin-level operations: user management,
// custom fields, duplicate detection / merging, and audit log access.
type AdminService struct {
	adminRepo    *repository.AdminRepository
	userRepo     *repository.UserRepository
	materialRepo *repository.MaterialRepository
}

// NewAdminService creates an AdminService wired to the given repositories.
func NewAdminService(
	ar *repository.AdminRepository,
	ur *repository.UserRepository,
	mr *repository.MaterialRepository,
) *AdminService {
	return &AdminService{adminRepo: ar, userRepo: ur, materialRepo: mr}
}

// ---------------------------------------------------------------
// User admin
// ---------------------------------------------------------------

// ListUsers returns paginated users optionally filtered by role.
func (s *AdminService) ListUsers(role string, limit, offset int) ([]models.User, error) {
	return s.adminRepo.ListUsers(role, limit, offset)
}

// CreateUser registers a new user account.  fullName is the person's real
// name used for duplicate detection; pass an empty string to leave it unset.
func (s *AdminService) CreateUser(username, email, password, role, fullName string) (*models.User, error) {
	authSvc := &AuthService{userRepo: s.userRepo}
	user, err := authSvc.Register(username, email, password, role)
	if err != nil {
		return nil, fmt.Errorf("service: AdminService.CreateUser: %w", err)
	}
	if fullName != "" {
		if err := s.userRepo.SetFullName(user.ID, fullName); err != nil {
			// Non-fatal: user was created; log and continue.
			observability.App.Warn("set full_name failed", "user_id", user.ID, "error", err)
		} else {
			user.FullName = &fullName
		}
	}
	return user, nil
}

// UpdateUserRole changes a user's role and writes an audit log entry.
func (s *AdminService) UpdateUserRole(userID int64, role string, actorID int64, actorIP string) error {
	// Capture before state.
	before, _ := s.userRepo.GetByID(userID)

	if err := s.adminRepo.UpdateUserRole(userID, role); err != nil {
		return fmt.Errorf("service: AdminService.UpdateUserRole: %w", err)
	}

	// Write audit log (best-effort).
	after, _ := s.userRepo.GetByID(userID)
	_ = s.adminRepo.WriteAuditLog(actorID, "update_role", "user", userID, before, after, actorIP)
	observability.Security.Info("role changed", "target_user_id", userID, "new_role", role, "actor_id", actorID)
	return nil
}

// UnlockUser clears a user's account lockout.
func (s *AdminService) UnlockUser(userID int64, actorID int64) error {
	if err := s.adminRepo.UnlockUser(userID); err != nil {
		return fmt.Errorf("service: AdminService.UnlockUser: %w", err)
	}
	_ = s.adminRepo.WriteAuditLog(actorID, "unlock", "user", userID, nil, nil, "")
	observability.Security.Info("account unlocked", "target_user_id", userID, "actor_id", actorID)
	return nil
}

// SetCustomField stores a custom field for a user.
// If encrypt is true the value is AES-256-GCM encrypted with encKey before
// being stored; encKey must be exactly 32 bytes.  If encryption is requested
// but the key is absent or the wrong length, the call fails rather than
// silently falling back to plaintext storage.
func (s *AdminService) SetCustomField(userID int64, name, value string, encrypt bool, encKey []byte) error {
	stored := value
	if encrypt {
		if len(encKey) != 32 {
			return fmt.Errorf("service: AdminService.SetCustomField: encryption requested but key is invalid or missing (got %d bytes, need 32)", len(encKey))
		}
		enc, err := crypto.EncryptField(encKey, value)
		if err != nil {
			return fmt.Errorf("service: AdminService.SetCustomField: encrypt: %w", err)
		}
		stored = enc
	}
	return s.adminRepo.SetCustomField(userID, name, stored, encrypt)
}

// GetCustomFields returns all custom fields for a user.
// Encrypted fields have their FieldValue replaced with the plaintext if encKey
// is provided and 32 bytes; otherwise the raw (ciphertext) value is returned.
func (s *AdminService) GetCustomFields(userID int64, encKey []byte) ([]models.UserCustomField, error) {
	fields, err := s.adminRepo.GetCustomFields(userID)
	if err != nil {
		return nil, err
	}
	if len(encKey) == 32 {
		for i, f := range fields {
			if f.IsEncrypted == 1 && f.FieldValue != nil {
				plain, decErr := crypto.DecryptField(encKey, *f.FieldValue)
				if decErr == nil {
					fields[i].FieldValue = &plain
				}
				// If decryption fails leave the raw value; caller can decide.
			}
		}
	}
	return fields, nil
}

// DeleteCustomField removes a custom field for a user.
func (s *AdminService) DeleteCustomField(userID int64, name string) error {
	return s.adminRepo.DeleteCustomField(userID, name)
}

// ---------------------------------------------------------------
// Duplicate detection
// ---------------------------------------------------------------

// FindDuplicates returns potential duplicate user pairs.
func (s *AdminService) FindDuplicates() ([]repository.DuplicatePair, error) {
	return s.adminRepo.FindDuplicateUsers(100)
}

// MergeUsers merges duplicateID into primaryID and records the operation.
func (s *AdminService) MergeUsers(primaryID, duplicateID int64, actorID int64) error {
	if err := s.adminRepo.MergeUsers(primaryID, duplicateID, actorID); err != nil {
		return fmt.Errorf("service: AdminService.MergeUsers: %w", err)
	}
	_ = s.adminRepo.WriteAuditLog(actorID, "merge_user", "user", primaryID,
		map[string]int64{"duplicate_id": duplicateID}, nil, "")
	observability.Security.Warn("entities merged", "primary_id", primaryID, "duplicate_id", duplicateID, "actor_id", actorID)
	return nil
}

// ---------------------------------------------------------------
// Audit log
// ---------------------------------------------------------------

// GetAuditLog returns paginated audit log entries for a specific entity.
func (s *AdminService) GetAuditLog(entityType string, entityID int64, limit, offset int) ([]models.AuditLog, error) {
	return s.adminRepo.GetAuditLog(entityType, entityID, limit, offset)
}

// GetRecentAuditLog returns the most-recent limit audit log entries.
func (s *AdminService) GetRecentAuditLog(limit int) ([]models.AuditLog, error) {
	return s.adminRepo.GetRecentAuditLog(limit)
}

package repository_test

import (
	"database/sql"
	"testing"
	"time"

	"w2t86/internal/repository"
	"w2t86/internal/testutil"
)

func newUserRepo(t *testing.T) (*repository.UserRepository, *sql.DB) {
	t.Helper()
	db := testutil.NewTestDB(t)
	return repository.NewUserRepository(db), db
}

func TestUserRepository_Create_Success(t *testing.T) {
	repo, _ := newUserRepo(t)

	u, err := repo.Create("alice", "alice@example.com", "hash123", "student")
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if u.ID == 0 {
		t.Error("expected non-zero ID")
	}
	if u.Username != "alice" {
		t.Errorf("username mismatch: got %q", u.Username)
	}
	if u.Role != "student" {
		t.Errorf("role mismatch: got %q", u.Role)
	}
}

func TestUserRepository_Create_DuplicateUsername_Fails(t *testing.T) {
	repo, _ := newUserRepo(t)

	if _, err := repo.Create("bob", "bob@example.com", "hash", "student"); err != nil {
		t.Fatalf("first Create: %v", err)
	}
	_, err := repo.Create("bob", "bob2@example.com", "hash2", "student")
	if err == nil {
		t.Error("expected error for duplicate username, got nil")
	}
}

func TestUserRepository_GetByUsername_Found(t *testing.T) {
	repo, _ := newUserRepo(t)

	if _, err := repo.Create("carol", "carol@example.com", "hash", "student"); err != nil {
		t.Fatalf("Create: %v", err)
	}
	u, err := repo.GetByUsername("carol")
	if err != nil {
		t.Fatalf("GetByUsername: %v", err)
	}
	if u.Username != "carol" {
		t.Errorf("expected carol, got %q", u.Username)
	}
}

func TestUserRepository_GetByUsername_NotFound(t *testing.T) {
	repo, _ := newUserRepo(t)

	_, err := repo.GetByUsername("nobody")
	if err == nil {
		t.Error("expected error for missing user, got nil")
	}
}

func TestUserRepository_IncrementFailedAttempts(t *testing.T) {
	repo, db := newUserRepo(t)

	u, err := repo.Create("dave", "dave@example.com", "hash", "student")
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if err := repo.IncrementFailedAttempts(u.ID); err != nil {
		t.Fatalf("IncrementFailedAttempts: %v", err)
	}

	var attempts int
	if err := db.QueryRow(`SELECT failed_attempts FROM users WHERE id = ?`, u.ID).Scan(&attempts); err != nil {
		t.Fatalf("query: %v", err)
	}
	if attempts != 1 {
		t.Errorf("expected 1 failed attempt, got %d", attempts)
	}
}

func TestUserRepository_LockUntil_And_ResetFailedAttempts(t *testing.T) {
	repo, db := newUserRepo(t)

	u, err := repo.Create("eve", "eve@example.com", "hash", "student")
	if err != nil {
		t.Fatalf("Create: %v", err)
	}

	lockTime := time.Now().UTC().Add(time.Hour)
	if err := repo.LockUntil(u.ID, lockTime); err != nil {
		t.Fatalf("LockUntil: %v", err)
	}
	if err := repo.IncrementFailedAttempts(u.ID); err != nil {
		t.Fatalf("IncrementFailedAttempts: %v", err)
	}

	var locked *string
	var attempts int
	if err := db.QueryRow(`SELECT locked_until, failed_attempts FROM users WHERE id = ?`, u.ID).Scan(&locked, &attempts); err != nil {
		t.Fatalf("query: %v", err)
	}
	if locked == nil {
		t.Error("expected locked_until to be set")
	}
	if attempts != 1 {
		t.Errorf("expected 1 failed attempt, got %d", attempts)
	}

	if err := repo.ResetFailedAttempts(u.ID); err != nil {
		t.Fatalf("ResetFailedAttempts: %v", err)
	}
	var lockedAfter *string
	var attemptsAfter int
	if err := db.QueryRow(`SELECT locked_until, failed_attempts FROM users WHERE id = ?`, u.ID).Scan(&lockedAfter, &attemptsAfter); err != nil {
		t.Fatalf("query after reset: %v", err)
	}
	if lockedAfter != nil {
		t.Error("expected locked_until to be NULL after reset")
	}
	if attemptsAfter != 0 {
		t.Errorf("expected 0 failed attempts after reset, got %d", attemptsAfter)
	}
}

func TestUserRepository_SoftDelete_HidesFromQuery(t *testing.T) {
	repo, _ := newUserRepo(t)

	u, err := repo.Create("frank", "frank@example.com", "hash", "student")
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if err := repo.SoftDelete(u.ID); err != nil {
		t.Fatalf("SoftDelete: %v", err)
	}

	_, err = repo.GetByUsername("frank")
	if err == nil {
		t.Error("expected error after soft-delete, got nil")
	}
}

func TestUserRepository_List_Pagination(t *testing.T) {
	repo, _ := newUserRepo(t)

	names := []string{"u1", "u2", "u3", "u4", "u5"}
	for _, name := range names {
		if _, err := repo.Create(name, name+"@x.com", "hash", "student"); err != nil {
			t.Fatalf("Create %q: %v", name, err)
		}
	}

	// Note: admin user from seed data will also be in the list
	all, err := repo.List(100, 0)
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(all) < 5 {
		t.Errorf("expected at least 5 users, got %d", len(all))
	}

	// Page 1: limit=2, offset=0
	page1, err := repo.List(2, 0)
	if err != nil {
		t.Fatalf("List page1: %v", err)
	}
	if len(page1) != 2 {
		t.Errorf("expected 2 users on page1, got %d", len(page1))
	}

	// Page 2: limit=2, offset=2
	page2, err := repo.List(2, 2)
	if err != nil {
		t.Fatalf("List page2: %v", err)
	}
	if len(page2) != 2 {
		t.Errorf("expected 2 users on page2, got %d", len(page2))
	}

	if page1[0].ID == page2[0].ID {
		t.Error("pages overlap — pagination broken")
	}
}

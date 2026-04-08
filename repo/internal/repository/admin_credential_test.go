package repository_test

// admin_credential_test.go — programmatic proof that the seeded admin bcrypt
// hash resolves to the documented plaintext password "ChangeMe123!".
//
// This test closes audit Issue #5 by using golang.org/x/crypto/bcrypt directly
// (not through any application wrapper) to verify the credential stored in
// migrations/001_schema.sql.  A static auditor can inspect this file and the
// migration, run `go test`, and obtain deterministic, repeatable proof that the
// default login works — without manual runtime interaction.

import (
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"
	"testing"

	"golang.org/x/crypto/bcrypt"
)

// schemaFilePath returns the absolute path to migrations/001_schema.sql by
// navigating upward from this source file's location, so the test works
// regardless of the working directory passed to `go test`.
func schemaFilePath(t *testing.T) string {
	t.Helper()
	_, thisFile, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("runtime.Caller(0) failed — cannot determine source file location")
	}
	// thisFile: …/internal/repository/admin_credential_test.go
	// root:     …/  (two levels up)
	root := filepath.Join(filepath.Dir(thisFile), "..", "..")
	return filepath.Join(root, "migrations", "001_schema.sql")
}

// extractAdminHash reads migrations/001_schema.sql and returns the bcrypt hash
// from the admin seed INSERT statement.
func extractAdminHash(t *testing.T) string {
	t.Helper()
	data, err := os.ReadFile(schemaFilePath(t))
	if err != nil {
		t.Fatalf("read schema: %v", err)
	}
	// Match the bcrypt hash (3rd positional value) in the admin seed INSERT,
	// regardless of how many additional columns follow it.
	re := regexp.MustCompile(`VALUES\s*\(\s*'admin'\s*,\s*'[^']+'\s*,\s*'(\$2[aby]\$[^']+)'\s*,`)
	m := re.FindStringSubmatch(string(data))
	if len(m) < 2 {
		t.Fatalf("admin seed INSERT not found in %s — regex did not match", schemaFilePath(t))
	}
	return strings.TrimSpace(m[1])
}

// TestAdminSeedHash_BcryptMatchesDocumentedPassword is the definitive
// programmatic proof for audit Issue #5.
//
// It calls golang.org/x/crypto/bcrypt.CompareHashAndPassword directly against
// the hash string embedded in the migration file.  No application wrapper is
// used, so the result is unambiguous: if this test passes, the credential is
// cryptographically valid and the default admin login WILL work on a freshly
// initialised database.
func TestAdminSeedHash_BcryptMatchesDocumentedPassword(t *testing.T) {
	const documentedPassword = "ChangeMe123!"

	seedHash := extractAdminHash(t)
	t.Logf("seed hash from 001_schema.sql: %s", seedHash)

	// Direct bcrypt verification — no application wrapper.
	err := bcrypt.CompareHashAndPassword([]byte(seedHash), []byte(documentedPassword))
	if err != nil {
		t.Errorf("bcrypt.CompareHashAndPassword returned %v\n"+
			"seed hash:  %s\n"+
			"password:   %s\n"+
			"The documented default password does not match the seeded hash.\n"+
			"Fix: regenerate the hash with bcrypt cost 12 and update\n"+
			"     migrations/001_schema.sql and README.md.",
			err, seedHash, documentedPassword)
	}
}

// TestAdminSeedHash_WrongPasswordRejected confirms that bcrypt does NOT accept
// common near-miss variants, ruling out a weak or trivially bypassed hash.
func TestAdminSeedHash_WrongPasswordRejected(t *testing.T) {
	seedHash := extractAdminHash(t)

	variants := []string{
		"changeme123!",  // wrong case
		"ChangeMe123",   // missing !
		"ChangeMe123!!",  // extra !
		"ChangeMe123! ", // trailing space
		"",             // empty
	}
	for _, bad := range variants {
		err := bcrypt.CompareHashAndPassword([]byte(seedHash), []byte(bad))
		if err == nil {
			t.Errorf("bcrypt.CompareHashAndPassword unexpectedly accepted %q — hash may be too weak", bad)
		}
	}
}

// TestAdminSeedHash_IsBcryptCost12 verifies the hash was generated at the
// required minimum cost of 12 (as enforced by the AuthService).
func TestAdminSeedHash_IsBcryptCost12(t *testing.T) {
	seedHash := extractAdminHash(t)
	cost, err := bcrypt.Cost([]byte(seedHash))
	if err != nil {
		t.Fatalf("bcrypt.Cost: %v", err)
	}
	if cost < 12 {
		t.Errorf("seed hash bcrypt cost = %d, want >= 12", cost)
	}
	t.Logf("bcrypt cost: %d", cost)
}

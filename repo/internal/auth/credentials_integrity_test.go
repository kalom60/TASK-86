package auth_test

// credentials_integrity_test.go — authoritative programmatic proof that the
// default admin credential seeded in migrations/001_schema.sql is valid.
//
// # Why this file exists
//
// A static audit of the SQL migration cannot verify that the bcrypt hash stored
// there actually corresponds to the documented plaintext password "ChangeMe123!".
// This test closes that gap: it reads the exact hash string from the migration
// file at compile-time-resolved path and calls golang.org/x/crypto/bcrypt
// directly, producing deterministic, repeatable proof without any manual steps.
//
// Run with:
//
//	go test -v ./internal/auth/...
//
// Expected output confirms both facts:
//  1. bcrypt.CompareHashAndPassword returns nil  → password is valid
//  2. bcrypt cost == 12                          → hash meets security policy

import (
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"
	"testing"

	"golang.org/x/crypto/bcrypt"
)

// migrationPath returns the absolute path to migrations/001_schema.sql.
// It uses runtime.Caller(0) so the result is correct regardless of the working
// directory from which `go test` is invoked.
func migrationPath(t *testing.T) string {
	t.Helper()
	_, file, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("runtime.Caller(0) failed")
	}
	// file: …/internal/auth/credentials_integrity_test.go
	// root: …/  (two directories up)
	root := filepath.Join(filepath.Dir(file), "..", "..")
	return filepath.Join(root, "migrations", "001_schema.sql")
}

// seedHash reads migrations/001_schema.sql and extracts the bcrypt hash from
// the admin seed INSERT statement.
func seedHash(t *testing.T) []byte {
	t.Helper()
	data, err := os.ReadFile(migrationPath(t))
	if err != nil {
		t.Fatalf("read %s: %v", migrationPath(t), err)
	}
	// Matches the bcrypt hash (3rd positional value) regardless of how many
	// additional columns follow it in the VALUES list.
	re := regexp.MustCompile(
		`VALUES\s*\(\s*'admin'\s*,\s*'[^']+'\s*,\s*'(\$2[aby]\$[^']+)'\s*,`)
	m := re.FindStringSubmatch(string(data))
	if len(m) < 2 {
		t.Fatalf("admin seed hash not found in %s — regex did not match\n"+
			"Check that the INSERT OR IGNORE for 'admin' is present in the migration.",
			migrationPath(t))
	}
	return []byte(strings.TrimSpace(m[1]))
}

// TestAdminCredentials_HashMatchesDocumentedPassword is the definitive proof
// required by the audit.
//
// It calls bcrypt.CompareHashAndPassword with no application-layer wrappers.
// PASS means: any freshly initialised database seeded by 001_schema.sql will
// accept "admin / ChangeMe123!" immediately after first run — no manual steps
// required.
func TestAdminCredentials_HashMatchesDocumentedPassword(t *testing.T) {
	const password = "ChangeMe123!"

	hash := seedHash(t)
	t.Logf("seed hash: %s", hash)

	if err := bcrypt.CompareHashAndPassword(hash, []byte(password)); err != nil {
		t.Fatalf(
			"bcrypt.CompareHashAndPassword failed: %v\n\n"+
				"The hash in migrations/001_schema.sql does NOT match %q.\n"+
				"To fix: regenerate the hash at bcrypt cost 12 and update\n"+
				"        migrations/001_schema.sql and README.md together.",
			err, password)
	}
	t.Logf("PASS: %q is a valid match for the seeded hash", password)
}

// TestAdminCredentials_HashCost verifies the hash satisfies the minimum security
// policy of bcrypt cost 12.
func TestAdminCredentials_HashCost(t *testing.T) {
	hash := seedHash(t)
	cost, err := bcrypt.Cost(hash)
	if err != nil {
		t.Fatalf("bcrypt.Cost: %v", err)
	}
	if cost < 12 {
		t.Errorf("bcrypt cost = %d, want >= 12", cost)
	}
	t.Logf("bcrypt cost: %d (meets policy minimum of 12)", cost)
}

// TestAdminCredentials_NearMissesRejected rules out a trivially weak hash by
// confirming that obvious near-miss passwords do NOT match.
func TestAdminCredentials_NearMissesRejected(t *testing.T) {
	hash := seedHash(t)
	cases := []string{
		"changeme123!",   // wrong capitalisation
		"ChangeMe123",    // missing punctuation
		"ChangeMe123!!",  // extra character
		"ChangeMe123! ",  // trailing space
		" ChangeMe123!",  // leading space
		"",               // empty
	}
	for _, bad := range cases {
		if err := bcrypt.CompareHashAndPassword(hash, []byte(bad)); err == nil {
			t.Errorf("bcrypt unexpectedly accepted %q — hash may be too weak or incorrect", bad)
		}
	}
}

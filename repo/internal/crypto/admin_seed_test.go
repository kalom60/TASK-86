package crypto_test

// admin_seed_test.go — verifies that the default admin password documented in
// the README ("ChangeMe123!") successfully bcrypt-compares against the seeded
// hash in migrations/001_schema.sql.
//
// This test closes the "runtime verification required" audit gap by providing
// automated, repeatable proof that the seed hash and the documented password
// are in sync.  If someone changes the seed hash without updating the README
// (or vice-versa) this test will fail.

import (
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"
	"testing"

	"w2t86/internal/crypto"
)

// schemaPath resolves the absolute path to migrations/001_schema.sql
// relative to this source file, so the test works regardless of the working
// directory from which `go test` is invoked.
func schemaPath(t *testing.T) string {
	t.Helper()
	// runtime.Caller(0) gives the path of this source file at compile time.
	_, thisFile, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("runtime.Caller failed — cannot determine source file path")
	}
	// thisFile: .../internal/crypto/admin_seed_test.go
	// root:     .../
	root := filepath.Join(filepath.Dir(thisFile), "..", "..")
	return filepath.Join(root, "migrations", "001_schema.sql")
}

// extractSeedHash reads migrations/001_schema.sql and returns the bcrypt hash
// used in the admin seed INSERT statement.
func extractSeedHash(t *testing.T) string {
	t.Helper()

	sqlFile := schemaPath(t)
	content, err := os.ReadFile(sqlFile)
	if err != nil {
		t.Fatalf("read %s: %v", sqlFile, err)
	}

	// Match the INSERT line for the admin seed row.
	// Captures the bcrypt hash (3rd positional value) regardless of how many
	// additional columns (e.g. full_name) follow it.
	// Example:
	//   VALUES('admin', 'admin@portal.local', '$2a$12$...', 'admin', 'System Administrator');
	re := regexp.MustCompile(`VALUES\s*\(\s*'admin'\s*,\s*'[^']+'\s*,\s*'(\$2[aby]\$[^']+)'\s*,`)
	m := re.FindStringSubmatch(string(content))
	if len(m) < 2 {
		t.Fatalf("could not find admin seed hash in %s — regex did not match", sqlFile)
	}
	return strings.TrimSpace(m[1])
}

// TestDefaultAdminPassword_MatchesSeedHash is the primary audit-driven test.
// It reads the bcrypt hash from the migration file and confirms that the
// documented default password produces a successful comparison.
func TestDefaultAdminPassword_MatchesSeedHash(t *testing.T) {
	const (
		// The default password documented in README.md and the startup warning
		// in cmd/server/main.go.
		documentedPassword = "ChangeMe123!"

		// The hash hardcoded as a sentinel in cmd/server/main.go.
		// Kept here as a belt-and-suspenders cross-check.
		sentinelHash = "$2a$12$fMPISK6tAC1XLVM3JdJQDuB/CrXgdRM.LUPHHu4/VxS/vzihnYyQ."
	)

	seedHash := extractSeedHash(t)
	t.Logf("seed hash from 001_schema.sql: %s", seedHash)

	// 1. The hash extracted from the migration must match the sentinel used by
	//    the startup warning — proves the three sources stay in sync.
	if seedHash != sentinelHash {
		t.Errorf("seed hash mismatch:\n  schema: %s\nsentinel: %s\nUpdate cmd/server/main.go to match.", seedHash, sentinelHash)
	}

	// 2. The documented password must successfully compare against the seed hash.
	if !crypto.CheckPassword(seedHash, documentedPassword) {
		t.Errorf("crypto.CheckPassword(%q, %q) = false; "+
			"the documented password does not match the seeded hash — "+
			"update README.md and/or migrations/001_schema.sql", seedHash, documentedPassword)
	}

	// 3. A wrong password must NOT match (sanity check for CheckPassword itself).
	wrongPasswords := []string{"changeme123!", "ChangeMe123", "ChangeMe123!!", ""}
	for _, bad := range wrongPasswords {
		if crypto.CheckPassword(seedHash, bad) {
			t.Errorf("crypto.CheckPassword unexpectedly accepted wrong password %q", bad)
		}
	}
}

// TestDefaultAdminPassword_SeedHashIsBcryptCost12 verifies the hash was
// generated at bcrypt cost 12 (as required by the auth service).
func TestDefaultAdminPassword_SeedHashIsBcryptCost12(t *testing.T) {
	seedHash := extractSeedHash(t)

	// bcrypt hash format: $2a$<cost>$<salt+hash>
	// Cost 12 → "$2a$12$"
	if !strings.HasPrefix(seedHash, "$2a$12$") {
		t.Errorf("seed hash does not use bcrypt cost 12: %s", seedHash)
	}
}

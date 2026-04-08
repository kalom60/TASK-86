package testutil

import (
	"database/sql"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"testing"

	_ "github.com/mattn/go-sqlite3"
)

// NewTestDB creates an in-memory SQLite DB and runs all migrations.
// Uses ":memory:" DSN with foreign keys enabled. Call t.Cleanup to close.
func NewTestDB(t *testing.T) *sql.DB {
	t.Helper()
	return newTestDB(t, ":memory:?_foreign_keys=on&_journal_mode=WAL")
}

// NewTestDBNoFK creates an in-memory SQLite DB with foreign key enforcement
// disabled.  Use this for tests that exercise code paths that deliberately
// pass system-level actor IDs (e.g. 0) that have no corresponding user row.
func NewTestDBNoFK(t *testing.T) *sql.DB {
	t.Helper()
	return newTestDB(t, ":memory:?_journal_mode=WAL")
}

func newTestDB(t *testing.T, dsn string) *sql.DB {
	t.Helper()
	db, err := sql.Open("sqlite3", dsn)
	if err != nil {
		t.Fatalf("open test db: %v", err)
	}
	db.SetMaxOpenConns(1)
	runMigrations(t, db)
	t.Cleanup(func() { db.Close() })
	return db
}

// reStripFTS5 matches the FTS5 virtual table declaration, its associated
// triggers, and any single-line comment immediately before them so they can be
// removed when FTS5 is not compiled in.
var reStripFTS5 = regexp.MustCompile(
	`(?is)` +
		// Virtual table declaration
		`CREATE\s+VIRTUAL\s+TABLE\s+IF\s+NOT\s+EXISTS\s+materials_fts\b.*?;` +
		`|` +
		// Single-line comments that describe the FTS block
		`--[^\n]*fts[^\n]*\n` +
		`|` +
		// Trigger blocks (BEGIN … END;)
		`CREATE\s+TRIGGER\s+IF\s+NOT\s+EXISTS\s+materials_fts_\w+\b.*?END\s*;`,
)

// runMigrations reads all *.sql files from the migrations directory and
// executes them in lexicographic order.  If the SQLite driver does not support
// FTS5, the virtual table and its triggers are stripped from the SQL so the
// rest of the schema can still be created.
func runMigrations(t *testing.T, db *sql.DB) {
	t.Helper()
	dir := findMigrationsDir(t)

	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("read migrations dir %q: %v", dir, err)
	}

	// Collect and sort SQL files lexicographically (matches production order).
	var files []string
	for _, e := range entries {
		if !e.IsDir() && strings.HasSuffix(e.Name(), ".sql") {
			files = append(files, e.Name())
		}
	}
	sort.Strings(files)

	// Probe FTS5 support once before running migrations.
	hasFTS5 := true
	if _, probeErr := db.Exec("CREATE VIRTUAL TABLE IF NOT EXISTS _fts5_probe USING fts5(x)"); probeErr != nil {
		hasFTS5 = false
	} else {
		db.Exec("DROP TABLE IF EXISTS _fts5_probe") //nolint:errcheck
	}

	for _, name := range files {
		data, err := os.ReadFile(filepath.Join(dir, name))
		if err != nil {
			t.Fatalf("read migration %s: %v", name, err)
		}

		sql := string(data)
		if !hasFTS5 {
			sql = reStripFTS5.ReplaceAllString(sql, "")
		}

		if _, err := db.Exec(sql); err != nil {
			t.Fatalf("run migration %s: %v", name, err)
		}
	}
}

func findMigrationsDir(t *testing.T) string {
	t.Helper()
	if p := os.Getenv("MIGRATION_PATH"); p != "" {
		// Accept either a directory path or a legacy path to 001_schema.sql.
		if strings.HasSuffix(p, ".sql") {
			return filepath.Dir(p)
		}
		return p
	}
	candidates := []string{
		"../migrations",       // from API_tests/ (repo_root/API_tests)
		"../../migrations",    // from internal/* packages
		"../../../migrations", // from internal/sub/packages
		"migrations",          // from repo root
	}
	for _, c := range candidates {
		if info, err := os.Stat(c); err == nil && info.IsDir() {
			return c
		}
	}
	t.Fatal("cannot find migrations/ directory - set MIGRATION_PATH env var to the directory path")
	return ""
}

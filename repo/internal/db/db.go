package db

import (
	"database/sql"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	_ "github.com/mattn/go-sqlite3"
)

const driverDSN = "%s?_journal_mode=WAL&_foreign_keys=on&_busy_timeout=5000"

// Open opens (or creates) the SQLite database at dbPath, configures WAL mode,
// foreign-key enforcement, and a busy timeout, then runs all pending
// migrations.  It returns the ready-to-use *sql.DB or an error.
func Open(dbPath string) (*sql.DB, error) {
	// Ensure the parent directory exists so SQLite can create the file.
	if dir := filepath.Dir(dbPath); dir != "" && dir != "." {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return nil, fmt.Errorf("db: create directory %q: %w", dir, err)
		}
	}

	dsn := fmt.Sprintf(driverDSN, dbPath)
	db, err := sql.Open("sqlite3", dsn)
	if err != nil {
		return nil, fmt.Errorf("db: open %q: %w", dbPath, err)
	}

	// Verify connectivity.
	if err := db.Ping(); err != nil {
		db.Close()
		return nil, fmt.Errorf("db: ping %q: %w", dbPath, err)
	}

	// SQLite performs best with a single writer connection.
	db.SetMaxOpenConns(1)

	if err := RunMigrations(db); err != nil {
		db.Close()
		return nil, err
	}

	return db, nil
}

// RunMigrations discovers every *.sql file inside the "migrations/" directory
// (relative to the working directory), sorts them lexicographically so that
// numbered files like 001_schema.sql are applied in order, and executes each
// one inside its own transaction.  Files that have already been applied are
// tracked in the _migrations table to make the operation idempotent.
func RunMigrations(db *sql.DB) error {
	// Bootstrap the tracking table.
	const bootstrap = `
CREATE TABLE IF NOT EXISTS _migrations (
    filename TEXT PRIMARY KEY,
    applied_at TEXT DEFAULT (datetime('now'))
);`
	if _, err := db.Exec(bootstrap); err != nil {
		return fmt.Errorf("db: bootstrap migrations table: %w", err)
	}

	entries, err := os.ReadDir("migrations")
	if err != nil {
		if os.IsNotExist(err) {
			// No migrations directory is not an error during testing.
			return nil
		}
		return fmt.Errorf("db: read migrations dir: %w", err)
	}

	// Collect .sql files and sort them.
	var files []string
	for _, e := range entries {
		if !e.IsDir() && strings.HasSuffix(e.Name(), ".sql") {
			files = append(files, e.Name())
		}
	}
	sort.Strings(files)

	for _, name := range files {
		applied, err := isMigrationApplied(db, name)
		if err != nil {
			return err
		}
		if applied {
			continue
		}

		content, err := os.ReadFile(filepath.Join("migrations", name))
		if err != nil {
			return fmt.Errorf("db: read migration %q: %w", name, err)
		}

		if err := applyMigration(db, name, string(content)); err != nil {
			return err
		}
	}

	return nil
}

func isMigrationApplied(db *sql.DB, filename string) (bool, error) {
	var count int
	err := db.QueryRow(
		`SELECT COUNT(*) FROM _migrations WHERE filename = ?`, filename,
	).Scan(&count)
	if err != nil {
		return false, fmt.Errorf("db: check migration %q: %w", filename, err)
	}
	return count > 0, nil
}

func applyMigration(db *sql.DB, filename, sql string) error {
	tx, err := db.Begin()
	if err != nil {
		return fmt.Errorf("db: begin tx for migration %q: %w", filename, err)
	}
	defer tx.Rollback() //nolint:errcheck

	if _, err := tx.Exec(sql); err != nil {
		return fmt.Errorf("db: execute migration %q: %w", filename, err)
	}

	if _, err := tx.Exec(
		`INSERT INTO _migrations(filename) VALUES(?)`, filename,
	); err != nil {
		return fmt.Errorf("db: record migration %q: %w", filename, err)
	}

	if err := tx.Commit(); err != nil {
		return fmt.Errorf("db: commit migration %q: %w", filename, err)
	}

	return nil
}

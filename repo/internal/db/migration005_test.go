package db_test

import (
	"database/sql"
	"os"
	"path/filepath"
	"runtime"
	"testing"

	_ "github.com/mattn/go-sqlite3"
)

// migrationSQL reads a numbered migration file relative to the repo root.
func migrationSQL(t *testing.T, name string) string {
	t.Helper()
	_, thisFile, _, _ := runtime.Caller(0)
	p := filepath.Join(filepath.Dir(thisFile), "..", "..", "migrations", name)
	data, err := os.ReadFile(p)
	if err != nil {
		t.Fatalf("read %s: %v", name, err)
	}
	return string(data)
}

// openMem opens a foreign-key-enabled in-memory SQLite database.
func openMem(t *testing.T) *sql.DB {
	t.Helper()
	db, err := sql.Open("sqlite3", ":memory:?_foreign_keys=on")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	return db
}

// mustExec fails the test if the SQL statement returns an error.
func mustExec(t *testing.T, db *sql.DB, query string) {
	t.Helper()
	if _, err := db.Exec(query); err != nil {
		t.Fatalf("exec: %v\nSQL: %s", err, query)
	}
}

// ---------------------------------------------------------------
// Migration 005 — adding external_id to a non-empty users table
// ---------------------------------------------------------------

// TestMigration005_NonEmptyTable verifies that 005_add_external_id.sql applies
// cleanly to a users table that already contains rows — the scenario that caused
// "Cannot add a UNIQUE column" with the original single-statement form.
func TestMigration005_NonEmptyTable(t *testing.T) {
	db := openMem(t)
	mustExec(t, db, `
		CREATE TABLE users (
			id            INTEGER PRIMARY KEY,
			username      TEXT UNIQUE NOT NULL,
			email         TEXT NOT NULL,
			password_hash TEXT NOT NULL,
			role          TEXT NOT NULL DEFAULT 'student'
		);
		INSERT INTO users (username, email, password_hash) VALUES
			('alice', 'a@x.com', 'h1'),
			('bob',   'b@x.com', 'h2'),
			('carol', 'c@x.com', 'h3');
	`)

	if _, err := db.Exec(migrationSQL(t, "005_add_external_id.sql")); err != nil {
		t.Fatalf("migration 005 failed on non-empty table: %v", err)
	}

	mustExec(t, db, `UPDATE users SET external_id = 'EID-001' WHERE username = 'alice'`)
	if _, err := db.Exec(`UPDATE users SET external_id = 'EID-001' WHERE username = 'bob'`); err == nil {
		t.Fatal("expected UNIQUE violation for duplicate external_id; got nil")
	}
	mustExec(t, db, `UPDATE users SET external_id = NULL WHERE username IN ('bob', 'carol')`)
}

// ---------------------------------------------------------------
// Migration 007 — course_plans section_id across all three states
// ---------------------------------------------------------------

// baseSchema creates the minimum tables that 007 depends on (users + materials).
// It does NOT create courses/course_sections/course_plans, simulating State B.
const baseSchemaForCourses = `
CREATE TABLE users (
	id            INTEGER PRIMARY KEY,
	username      TEXT UNIQUE NOT NULL,
	email         TEXT NOT NULL,
	password_hash TEXT NOT NULL,
	role          TEXT NOT NULL DEFAULT 'student'
);
CREATE TABLE materials (
	id            INTEGER PRIMARY KEY,
	title         TEXT NOT NULL,
	total_qty     INTEGER DEFAULT 0,
	available_qty INTEGER DEFAULT 0,
	reserved_qty  INTEGER DEFAULT 0,
	status        TEXT DEFAULT 'active',
	deleted_at    TEXT
);
`

// TestMigration007_StateB verifies that 007 succeeds when course_plans does not
// exist at all — the "pre-courses" existing database state that caused the
// original "no such table: course_plans" error during docker compose up.
func TestMigration007_StateB(t *testing.T) {
	db := openMem(t)
	mustExec(t, db, baseSchemaForCourses) // courses tables intentionally absent

	if _, err := db.Exec(migrationSQL(t, "007_course_plans_section.sql")); err != nil {
		t.Fatalf("migration 007 (State B) failed: %v", err)
	}

	assertCoursePlanSchema(t, db)
}

// TestMigration007_StateA verifies that 007 succeeds when course_plans already
// exists with the new schema (section_id present) — the fresh-install state
// where 001_schema.sql created everything correctly.
func TestMigration007_StateA(t *testing.T) {
	db := openMem(t)
	mustExec(t, db, baseSchemaForCourses)
	// Simulate 001_schema.sql having already created the courses tables.
	mustExec(t, db, `
		CREATE TABLE courses (
			id            INTEGER PRIMARY KEY,
			instructor_id INTEGER NOT NULL REFERENCES users(id),
			name          TEXT NOT NULL,
			subject       TEXT, grade_level TEXT, academic_year TEXT,
			created_at    TEXT DEFAULT (datetime('now')),
			updated_at    TEXT DEFAULT (datetime('now'))
		);
		CREATE TABLE course_sections (
			id         INTEGER PRIMARY KEY,
			course_id  INTEGER NOT NULL REFERENCES courses(id) ON DELETE CASCADE,
			name       TEXT NOT NULL,
			period     TEXT, room TEXT,
			created_at TEXT DEFAULT (datetime('now'))
		);
		CREATE TABLE course_plans (
			id            INTEGER PRIMARY KEY,
			course_id     INTEGER NOT NULL REFERENCES courses(id) ON DELETE CASCADE,
			section_id    INTEGER REFERENCES course_sections(id),
			material_id   INTEGER NOT NULL REFERENCES materials(id),
			requested_qty INTEGER NOT NULL DEFAULT 1,
			approved_qty  INTEGER DEFAULT 0,
			status        TEXT DEFAULT 'pending',
			notes         TEXT,
			created_at    TEXT DEFAULT (datetime('now')),
			updated_at    TEXT DEFAULT (datetime('now'))
		);
		CREATE UNIQUE INDEX uq_course_plans_section
			ON course_plans(course_id, COALESCE(section_id, 0), material_id);
	`)

	if _, err := db.Exec(migrationSQL(t, "007_course_plans_section.sql")); err != nil {
		t.Fatalf("migration 007 (State A) failed: %v", err)
	}

	assertCoursePlanSchema(t, db)
}

// TestMigration007_StateC verifies that 007 succeeds when course_plans exists
// with the OLD schema (UNIQUE(course_id,material_id), no section_id) — the
// "courses feature existed before section granularity was added" state.
func TestMigration007_StateC(t *testing.T) {
	db := openMem(t)
	mustExec(t, db, baseSchemaForCourses)
	mustExec(t, db, `
		CREATE TABLE courses (
			id            INTEGER PRIMARY KEY,
			instructor_id INTEGER NOT NULL REFERENCES users(id),
			name          TEXT NOT NULL,
			subject       TEXT, grade_level TEXT, academic_year TEXT,
			created_at    TEXT DEFAULT (datetime('now')),
			updated_at    TEXT DEFAULT (datetime('now'))
		);
		CREATE TABLE course_sections (
			id         INTEGER PRIMARY KEY,
			course_id  INTEGER NOT NULL REFERENCES courses(id) ON DELETE CASCADE,
			name       TEXT NOT NULL,
			period     TEXT, room TEXT,
			created_at TEXT DEFAULT (datetime('now'))
		);
		-- OLD schema: no section_id, old inline UNIQUE constraint
		CREATE TABLE course_plans (
			id            INTEGER PRIMARY KEY,
			course_id     INTEGER NOT NULL REFERENCES courses(id) ON DELETE CASCADE,
			material_id   INTEGER NOT NULL REFERENCES materials(id),
			requested_qty INTEGER NOT NULL DEFAULT 1,
			approved_qty  INTEGER DEFAULT 0,
			status        TEXT DEFAULT 'pending',
			notes         TEXT,
			created_at    TEXT DEFAULT (datetime('now')),
			updated_at    TEXT DEFAULT (datetime('now')),
			UNIQUE(course_id, material_id)
		);
	`)

	// Insert a row to confirm data is preserved across the rebuild.
	mustExec(t, db, `
		INSERT INTO users (username, email, password_hash) VALUES ('prof', 'p@x.com', 'h');
		INSERT INTO materials (title) VALUES ('Textbook A');
		INSERT INTO courses (instructor_id, name) VALUES (1, 'Intro');
		INSERT INTO course_plans (course_id, material_id, requested_qty)
		VALUES (1, 1, 30);
	`)

	if _, err := db.Exec(migrationSQL(t, "007_course_plans_section.sql")); err != nil {
		t.Fatalf("migration 007 (State C) failed: %v", err)
	}

	assertCoursePlanSchema(t, db)

	// Existing row must survive the rebuild.
	var count int
	if err := db.QueryRow(`SELECT COUNT(*) FROM course_plans WHERE requested_qty = 30`).Scan(&count); err != nil {
		t.Fatalf("query: %v", err)
	}
	if count != 1 {
		t.Errorf("expected 1 preserved row, got %d", count)
	}
}

// assertCoursePlanSchema checks that course_plans has section_id and the
// correct unique index after the migration.
func assertCoursePlanSchema(t *testing.T, db *sql.DB) {
	t.Helper()

	// section_id column must exist (nullable).
	if _, err := db.Exec(`UPDATE course_plans SET section_id = NULL WHERE 1=0`); err != nil {
		t.Fatalf("section_id column missing or wrong type: %v", err)
	}

	// Unique index must be enforced.
	// Insert two plans for the same (course, nil section, material) — second must fail.
	mustExec(t, db, `
		INSERT OR IGNORE INTO users (username, email, password_hash) VALUES ('u007', 'u@x.com', 'h');
		INSERT OR IGNORE INTO materials (title) VALUES ('Book007');
		INSERT OR IGNORE INTO courses (instructor_id, name) VALUES (
			(SELECT id FROM users WHERE username='u007'), 'Course007'
		);
	`)

	var courseID, matID int64
	if err := db.QueryRow(`SELECT id FROM courses WHERE name='Course007'`).Scan(&courseID); err != nil {
		t.Fatalf("get course: %v", err)
	}
	if err := db.QueryRow(`SELECT id FROM materials WHERE title='Book007'`).Scan(&matID); err != nil {
		t.Fatalf("get material: %v", err)
	}

	if _, err := db.Exec(`DELETE FROM course_plans WHERE course_id = ? AND material_id = ?`, courseID, matID); err != nil {
		t.Fatalf("delete: %v", err)
	}
	if _, err := db.Exec(`INSERT INTO course_plans (course_id, material_id, requested_qty) VALUES (?,?,1)`, courseID, matID); err != nil {
		t.Fatalf("insert first plan: %v", err)
	}
	if _, err := db.Exec(`INSERT INTO course_plans (course_id, material_id, requested_qty) VALUES (?,?,2)`, courseID, matID); err == nil {
		t.Error("expected UNIQUE violation for duplicate (course, nil-section, material); got nil")
	}
}

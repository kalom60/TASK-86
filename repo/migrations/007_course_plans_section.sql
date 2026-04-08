-- 007_course_plans_section.sql
--
-- Goal: ensure the courses, course_sections, and course_plans tables exist
-- with the correct schema (course_plans.section_id, no old inline UNIQUE
-- constraint, unique index using COALESCE).
--
-- This migration must be idempotent across three possible database states:
--
--   State A — fresh install
--     001_schema.sql already created all three tables with the correct schema.
--     The CREATE TABLE IF NOT EXISTS statements below are no-ops.
--     The rename dance rebuilds the table identically and re-creates the index
--     (the old index name is freed when course_plans_old is dropped).
--
--   State B — existing database that predates the courses feature
--     001_schema.sql was applied before courses tables were added to it.
--     course_plans does not exist at all.
--     The CREATE TABLE IF NOT EXISTS for course_plans creates the table fresh
--     with section_id; the rename dance then rebuilds it and creates the index.
--
--   State C — existing database with courses tables but old course_plans schema
--     (UNIQUE(course_id, material_id) table constraint, no section_id column)
--     The CREATE TABLE IF NOT EXISTS for course_plans is a no-op (table exists).
--     The rename dance replaces the old table with the new schema and drops the
--     old inline UNIQUE constraint, adding the COALESCE-based unique index.

-- -------------------------------------------------------
-- Step 1: Prerequisite tables
-- -------------------------------------------------------

CREATE TABLE IF NOT EXISTS courses (
    id            INTEGER PRIMARY KEY,
    instructor_id INTEGER NOT NULL REFERENCES users(id),
    name          TEXT    NOT NULL,
    subject       TEXT,
    grade_level   TEXT,
    academic_year TEXT,
    created_at    TEXT    DEFAULT (datetime('now')),
    updated_at    TEXT    DEFAULT (datetime('now'))
);

CREATE TABLE IF NOT EXISTS course_sections (
    id         INTEGER PRIMARY KEY,
    course_id  INTEGER NOT NULL REFERENCES courses(id) ON DELETE CASCADE,
    name       TEXT    NOT NULL,
    period     TEXT,
    room       TEXT,
    created_at TEXT    DEFAULT (datetime('now'))
);

-- Create course_plans if it does not yet exist (State B).
-- On State A/C this is a no-op; the rename dance below always runs to
-- normalise the schema and index in both remaining states.
CREATE TABLE IF NOT EXISTS course_plans (
    id            INTEGER PRIMARY KEY,
    course_id     INTEGER NOT NULL REFERENCES courses(id) ON DELETE CASCADE,
    section_id    INTEGER REFERENCES course_sections(id),
    material_id   INTEGER NOT NULL REFERENCES materials(id),
    requested_qty INTEGER NOT NULL DEFAULT 1,
    approved_qty  INTEGER DEFAULT 0,
    status        TEXT    DEFAULT 'pending',
    notes         TEXT,
    created_at    TEXT    DEFAULT (datetime('now')),
    updated_at    TEXT    DEFAULT (datetime('now'))
);

-- -------------------------------------------------------
-- Step 2: Rebuild course_plans with the correct schema
--
-- By this point course_plans is guaranteed to exist (Step 1 ensures it).
-- The rename-recreate pattern:
--   * removes the old UNIQUE(course_id, material_id) table constraint (State C)
--   * is a schema-preserving no-op for States A and B (empty tables)
--   * preserves all existing plan rows
-- section_id is carried over as NULL because:
--   - State A/B: tables are empty (no rows to lose)
--   - State C: old schema had no section_id column; NULL is correct
-- -------------------------------------------------------

ALTER TABLE course_plans RENAME TO course_plans_old;

CREATE TABLE course_plans (
    id            INTEGER PRIMARY KEY,
    course_id     INTEGER NOT NULL REFERENCES courses(id) ON DELETE CASCADE,
    section_id    INTEGER REFERENCES course_sections(id),
    material_id   INTEGER NOT NULL REFERENCES materials(id),
    requested_qty INTEGER NOT NULL DEFAULT 1,
    approved_qty  INTEGER DEFAULT 0,
    status        TEXT    DEFAULT 'pending',
    notes         TEXT,
    created_at    TEXT    DEFAULT (datetime('now')),
    updated_at    TEXT    DEFAULT (datetime('now'))
);

INSERT INTO course_plans
    (id, course_id, section_id, material_id, requested_qty, approved_qty,
     status, notes, created_at, updated_at)
SELECT id, course_id, NULL, material_id, requested_qty, approved_qty,
       status, notes, created_at, updated_at
FROM   course_plans_old;

DROP TABLE course_plans_old;

-- -------------------------------------------------------
-- Step 3: Unique index
--
-- By the time we reach here the old index name (if any) has been freed by
-- DROP TABLE course_plans_old, so IF NOT EXISTS will always create it.
-- COALESCE(section_id, 0) lets the index treat NULL section as a sentinel 0,
-- preventing duplicate whole-course plans for the same material.
-- -------------------------------------------------------

CREATE UNIQUE INDEX IF NOT EXISTS uq_course_plans_section
    ON course_plans(course_id, COALESCE(section_id, 0), material_id);

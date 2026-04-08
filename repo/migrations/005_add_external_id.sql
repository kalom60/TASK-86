-- 005_add_external_id.sql
-- Adds a dedicated external_id column to the users table.
-- external_id holds a student/employee/institution-issued identifier that is
-- stable across account re-creation and is used as the "exact identifier" signal
-- in duplicate-user detection (score = 1.0 when two accounts share the same
-- non-null external_id).
--
-- SQLite prohibits ALTER TABLE ADD COLUMN with a UNIQUE constraint when the
-- table already contains rows ("Cannot add a UNIQUE column").  The two-step
-- approach below is functionally identical to a column-level UNIQUE declaration:
--   1. Add the column without any constraint (existing rows receive NULL).
--   2. Build a unique index.  SQLite allows multiple NULLs in a UNIQUE index
--      because NULL is not considered equal to NULL, which matches the
--      semantics of the inline UNIQUE keyword on the base schema column.

ALTER TABLE users ADD COLUMN external_id TEXT;

CREATE UNIQUE INDEX IF NOT EXISTS uq_users_external_id ON users(external_id);

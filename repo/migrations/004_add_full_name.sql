-- 004_add_full_name.sql
-- Adds the full_name column to the users table.
-- This provides a real-name field used for duplicate-user detection
-- (fuzzy name matching via Levenshtein distance + date_of_birth).
ALTER TABLE users ADD COLUMN full_name TEXT;

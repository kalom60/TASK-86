-- 002_add_date_of_birth.sql
-- Adds date_of_birth column to users for improved duplicate detection.

ALTER TABLE users ADD COLUMN date_of_birth TEXT;

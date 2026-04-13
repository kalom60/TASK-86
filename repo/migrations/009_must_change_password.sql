-- 009_must_change_password.sql
--
-- Adds must_change_password flag to users.  When set to 1 the auth handler
-- redirects the user to a mandatory password-reset page immediately after login
-- instead of the normal dashboard.
--
-- The seeded default admin account ships with a non-functional bootstrap
-- placeholder credential.  On first boot, the server auto-rotates it to a
-- cryptographically-random password and sets must_change_password = 1 so the
-- operator is forced to change it immediately after their first login.

ALTER TABLE users ADD COLUMN must_change_password INTEGER NOT NULL DEFAULT 0;

-- Flag the seeded admin account so the first login forces an immediate password reset.
UPDATE users SET must_change_password = 1 WHERE username = 'admin';

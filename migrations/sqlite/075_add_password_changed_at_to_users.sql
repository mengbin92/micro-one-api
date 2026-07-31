-- v0.11.x identity-M6: password-change epoch for session revocation.
-- See migrations/075_add_password_changed_at_to_users.sql for full rationale.
-- SQLite ALTER TABLE ... ADD COLUMN does not support IF NOT EXISTS prior to
-- 3.35; the migrator treats "duplicate column name" as a no-op. Additive only;
-- rollback = (SQLite cannot DROP COLUMN before 3.35, so rebuild the table
-- without the column).

ALTER TABLE users
  ADD COLUMN password_changed_at INTEGER NOT NULL DEFAULT 0;

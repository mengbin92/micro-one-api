-- v0.11.x identity-M6: password-change epoch for session revocation.
-- See migrations/075_add_password_changed_at_to_users.sql for full rationale.
-- Epoch stored in Unix milliseconds (review L1). Additive only;
-- rollback = DROP COLUMN.

ALTER TABLE users
	ADD COLUMN IF NOT EXISTS password_changed_at BIGINT NOT NULL DEFAULT 0;

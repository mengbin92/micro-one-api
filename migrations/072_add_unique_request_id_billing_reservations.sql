-- v0.11.x: enforce request_id uniqueness per user on billing_reservations.
--
-- Idempotency keyed on request_id was previously a non-unique index plus a
-- non-transactional read-then-write in ReserveQuota, so two concurrent
-- reservations with the same request_id for different users could replay one
-- user's cached response to the other, and two reservations for the SAME user
-- could double-charge the wallet. This migration promotes the (user_id,
-- request_id) pair to a unique index so the database rejects the second insert.
-- We key on the PAIR (not request_id alone) because legacy rows legitimately
-- share an empty request_id string across users.
--
-- The index is partial in spirit: rows whose request_id is '' are excluded by
-- the WHERE clause on supporting databases. SQLite has no partial-index syntax
-- that GORM AutoMigrate also understands, so the unique index spans every row;
-- callers MUST NOT persist duplicate empty request_id pairs (they would now
-- violate the constraint). Existing rows with empty request_id are
-- de-duplicated below.
-- Rollback = DROP INDEX.

-- De-duplicate any existing rows that share (user_id, '') so the unique index
-- can be created. Keep the newest row per (user_id, '') and delete the rest.
DELETE FROM billing_reservations
 WHERE id NOT IN (
   SELECT MAX(id) FROM billing_reservations
    WHERE request_id = ''
    GROUP BY user_id
 );

CREATE UNIQUE INDEX IF NOT EXISTS uq_billing_reservations_user_request
  ON billing_reservations(user_id, request_id);

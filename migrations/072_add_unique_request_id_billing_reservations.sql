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
--
-- CRITICAL: the outer DELETE is scoped to request_id = '' so that only legacy
-- empty-key duplicates are removed. Without this filter the NOT IN subquery
-- (which returns only empty-key ids) would match every non-empty request_id
-- row, deleting the entire table. The double-nested subquery (SELECT keep_id
-- FROM (SELECT ...)) materialises the id set before the delete, avoiding
-- MySQL error 1093 ("can't specify target table for update in FROM clause")
-- which some MySQL versions raise when the same table is read and modified in
-- a single statement.
DELETE FROM billing_reservations
 WHERE request_id = ''
   AND id NOT IN (
     SELECT keep_id FROM (
       SELECT MAX(id) AS keep_id
         FROM billing_reservations
        WHERE request_id = ''
        GROUP BY user_id
     ) AS keep_ids
   );

-- MySQL does not support CREATE UNIQUE INDEX IF NOT EXISTS, so the index is
-- created only when it is not already present.
SET @uq_billing_reservations_user_request_exists := (
  SELECT COUNT(*) FROM information_schema.STATISTICS
  WHERE TABLE_SCHEMA = DATABASE()
    AND TABLE_NAME = 'billing_reservations'
    AND INDEX_NAME = 'uq_billing_reservations_user_request'
);
SET @ddl_add_uq_billing_reservations_user_request := IF(
  @uq_billing_reservations_user_request_exists = 0,
  'CREATE UNIQUE INDEX uq_billing_reservations_user_request ON billing_reservations(user_id, request_id)',
  'SELECT 1'
);
PREPARE uq_billing_reservations_user_request_stmt FROM @ddl_add_uq_billing_reservations_user_request;
EXECUTE uq_billing_reservations_user_request_stmt;
DEALLOCATE PREPARE uq_billing_reservations_user_request_stmt;

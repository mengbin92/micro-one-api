-- v0.11.x: enforce request_id uniqueness per user on billing_reservations
-- (SQLite upgrade path).
--
-- New SQLite databases already include the unique index in the 000 baseline
-- schema. This migration adds it for EXISTING SQLite databases that were
-- created before the index existed. It also de-duplicates legacy empty-key
-- rows so the unique index can be created without a constraint violation.
--
-- See the MySQL migration of the same number for the full rationale.
-- Rollback = DROP INDEX.

-- De-duplicate existing rows that share (user_id, '') so the unique index
-- can be created. Keep the newest row per (user_id, '') and delete the rest.
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

CREATE UNIQUE INDEX IF NOT EXISTS uq_billing_reservations_user_request
  ON billing_reservations(user_id, request_id);

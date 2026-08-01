-- v0.11.x: enforce request_id uniqueness per user on billing_reservations.
-- See migrations/072_add_unique_request_id_billing_reservations.sql for full rationale.
--
-- Postgres supports partial indexes, so rows with an empty request_id are
-- excluded entirely (they are legacy / unkeyed reservations and must not block
-- each other).

CREATE UNIQUE INDEX IF NOT EXISTS uq_billing_reservations_user_request
  ON billing_reservations(user_id, request_id)
  WHERE request_id <> '';

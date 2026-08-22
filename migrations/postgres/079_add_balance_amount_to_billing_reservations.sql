-- See migrations/079_add_balance_amount_to_billing_reservations.sql.
-- Additive and rollback-safe while old binaries still read the legacy column.

ALTER TABLE billing_reservations
  ADD COLUMN IF NOT EXISTS balance_amount BIGINT NOT NULL DEFAULT 0;

UPDATE billing_reservations
SET balance_amount = balance_amount_quota
WHERE balance_amount = 0 AND balance_amount_quota <> 0;

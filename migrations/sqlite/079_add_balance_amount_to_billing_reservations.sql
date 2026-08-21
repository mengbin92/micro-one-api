-- See migrations/079_add_balance_amount_to_billing_reservations.sql.
-- The migrator treats a duplicate-column error as a no-op on replay.

ALTER TABLE billing_reservations
  ADD COLUMN balance_amount INTEGER NOT NULL DEFAULT 0;

UPDATE billing_reservations
SET balance_amount = balance_amount_quota
WHERE balance_amount = 0 AND balance_amount_quota <> 0;

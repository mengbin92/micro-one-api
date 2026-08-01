-- v0.11.x billing-L1: persist the real settlement cost on a reservation.
-- See migrations/074_add_actual_cost_to_billing_reservations.sql for the full
-- rationale. SQLite ALTER TABLE ... ADD COLUMN does not support IF NOT EXISTS
-- prior to 3.35; the migrator treats "duplicate column name" as a no-op.
-- Additive only; rollback = (SQLite cannot DROP COLUMN before 3.35, so rebuild
-- the table without the column).
ALTER TABLE billing_reservations
  ADD COLUMN actual_cost INTEGER NOT NULL DEFAULT 0;

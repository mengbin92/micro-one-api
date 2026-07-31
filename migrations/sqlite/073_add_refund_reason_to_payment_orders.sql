-- v0.11.x billing-L4: dedicated refund_reason column on payment_orders.
-- See migrations/073_add_refund_reason_to_payment_orders.sql for full rationale.
-- SQLite ALTER TABLE ... ADD COLUMN does not support IF NOT EXISTS prior to
-- 3.35; the migrator treats "duplicate column name" as a no-op. Additive only;
-- rollback = (SQLite cannot DROP COLUMN before 3.35, so rebuild the table
-- without the column).
ALTER TABLE payment_orders
  ADD COLUMN refund_reason TEXT;

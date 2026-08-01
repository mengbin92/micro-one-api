-- v0.11.x billing-L4: dedicated refund_reason column on payment_orders.
-- See migrations/073_add_refund_reason_to_payment_orders.sql for full rationale.
-- Additive only; rollback = DROP COLUMN.

ALTER TABLE payment_orders
  ADD COLUMN IF NOT EXISTS refund_reason TEXT;

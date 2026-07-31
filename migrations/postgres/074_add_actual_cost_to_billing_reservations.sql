-- v0.11.x billing-L1: persist the real settlement cost on a reservation.
-- See migrations/074_add_actual_cost_to_billing_reservations.sql for the full
-- rationale. Additive only; rollback = DROP COLUMN.

ALTER TABLE billing_reservations
  ADD COLUMN IF NOT EXISTS actual_cost BIGINT NOT NULL DEFAULT 0;

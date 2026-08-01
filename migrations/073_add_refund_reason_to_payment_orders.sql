-- v0.11.x billing-L4: dedicated refund_reason column on payment_orders.
--
-- MarkOrderRefunded previously overwrote provider_payload with the free-text
-- refund reason, destroying the original provider payload (and, for legacy
-- orders, the subscription_id encoded inside it that the refund path uses for
-- subscription traceability). This adds a dedicated column so the original
-- provider_payload is preserved. Additive only; rollback = DROP COLUMN.

ALTER TABLE `payment_orders`
  ADD COLUMN `refund_reason` text NULL;

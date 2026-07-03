-- Add group_id field to payment_orders for subscription purchase
-- This allows linking a payment order to a subscription group.
-- When payment is completed, the subscription will be automatically assigned.

ALTER TABLE `payment_orders`
  ADD COLUMN `group_id` bigint NOT NULL DEFAULT 0 AFTER `asset_issue_status`,
  ADD KEY `idx_payment_orders_group_id` (`group_id`);

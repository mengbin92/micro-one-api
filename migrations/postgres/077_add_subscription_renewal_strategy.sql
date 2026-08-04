-- code-review M2: record the renewal strategy on user_subscriptions.
-- See migrations/077_add_subscription_renewal_strategy.sql for full rationale.
-- Additive (column only); rollback = DROP COLUMN renewal_strategy.

ALTER TABLE user_subscriptions
  ADD COLUMN IF NOT EXISTS renewal_strategy TEXT NOT NULL DEFAULT '';

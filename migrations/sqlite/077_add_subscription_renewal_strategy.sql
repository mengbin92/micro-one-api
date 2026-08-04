-- code-review M2: record the renewal strategy on user_subscriptions.
-- See migrations/077_add_subscription_renewal_strategy.sql for full rationale.
-- SQLite ALTER TABLE ADD COLUMN does not support IF NOT EXISTS prior to 3.35;
-- the migrator treats "duplicate column name" as a no-op.
-- Additive; rollback = rebuild table without the column.

ALTER TABLE user_subscriptions
  ADD COLUMN renewal_strategy TEXT NOT NULL DEFAULT '';

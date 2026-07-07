ALTER TABLE payment_orders
  ADD COLUMN IF NOT EXISTS plan_snapshot TEXT DEFAULT NULL;

CREATE TABLE IF NOT EXISTS subscription_account_quota_reset_runs (
  id BIGSERIAL PRIMARY KEY,
  subscription_account_id BIGINT NOT NULL,
  scope TEXT NOT NULL,
  window_start BIGINT NOT NULL,
  strategy TEXT NOT NULL DEFAULT 'fixed',
  timezone TEXT NOT NULL DEFAULT 'UTC',
  reset_at BIGINT NOT NULL
);

CREATE UNIQUE INDEX IF NOT EXISTS idx_subscription_account_quota_reset_runs_dedupe
  ON subscription_account_quota_reset_runs(subscription_account_id, scope, window_start);
CREATE INDEX IF NOT EXISTS idx_subscription_account_quota_reset_runs_account_time
  ON subscription_account_quota_reset_runs(subscription_account_id, reset_at);

ALTER TABLE billing_reservations ADD COLUMN request_snapshot TEXT NULL,
  ADD COLUMN request_snapshot_hash VARCHAR(64) NULL,
  ADD COLUMN subscription_accounting_usd DOUBLE PRECISION NULL;
CREATE TABLE subscription_window_charges (
  reservation_id VARCHAR(64) PRIMARY KEY,
  subscription_id BIGINT NOT NULL,
  quota_policy_id BIGINT NOT NULL,
  daily_window_start BIGINT NOT NULL,
  weekly_window_start BIGINT NOT NULL,
  monthly_window_start BIGINT NOT NULL,
  accounting_usd DOUBLE PRECISION NOT NULL,
  created_at BIGINT NOT NULL
);
CREATE INDEX idx_subscription_window_charge ON subscription_window_charges(subscription_id);

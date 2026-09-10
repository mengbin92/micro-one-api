-- Billing-owned evidence; no historical group or price is inferred.
ALTER TABLE billing_reservations ADD COLUMN request_snapshot LONGTEXT NULL,
  ADD COLUMN request_snapshot_hash VARCHAR(64) NULL,
  ADD COLUMN subscription_accounting_usd DOUBLE NULL;
CREATE TABLE subscription_window_charges (
  reservation_id VARCHAR(64) NOT NULL PRIMARY KEY,
  subscription_id BIGINT NOT NULL,
  quota_policy_id BIGINT NOT NULL,
  daily_window_start BIGINT NOT NULL,
  weekly_window_start BIGINT NOT NULL,
  monthly_window_start BIGINT NOT NULL,
  accounting_usd DOUBLE NOT NULL,
  created_at BIGINT NOT NULL,
  KEY idx_subscription_window_charge (subscription_id)
) ENGINE=InnoDB;

ALTER TABLE billing_reservations ADD COLUMN request_snapshot TEXT NULL;
ALTER TABLE billing_reservations ADD COLUMN request_snapshot_hash TEXT NULL;
ALTER TABLE billing_reservations ADD COLUMN subscription_accounting_usd REAL NULL;
CREATE TABLE subscription_window_charges (
  reservation_id TEXT PRIMARY KEY,
  subscription_id INTEGER NOT NULL,
  quota_policy_id INTEGER NOT NULL,
  daily_window_start INTEGER NOT NULL,
  weekly_window_start INTEGER NOT NULL,
  monthly_window_start INTEGER NOT NULL,
  accounting_usd REAL NOT NULL,
  created_at INTEGER NOT NULL
);
CREATE INDEX idx_subscription_window_charge ON subscription_window_charges(subscription_id);

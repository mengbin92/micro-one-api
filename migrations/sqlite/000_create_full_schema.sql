-- Consolidated baseline schema for SQLite (Lite deployment).
--
-- This migration creates the full schema in one shot. It is intentionally
-- not split into per-change files the way the MySQL migrations are, because
-- many of those migrations use MySQL-specific syntax (AUTO_INCREMENT,
-- ENGINE=InnoDB, DEFAULT CHARSET, COMMENT, MODIFY COLUMN, prefix indexes,
-- ON UPDATE CURRENT_TIMESTAMP, PARTITION BY RANGE) that does not translate
-- verbatim. Splitting into per-change files would either:
--
--   - duplicate MySQL's structure and risk drift, or
--   - require per-driver conditional SQL inside the migration runner.
--
-- Instead, the SQLite baseline is a single hand-written snapshot. Future
-- schema changes should add a new numbered .sql file alongside this one
-- in migrations/sqlite/ and the runner will apply them in order.
--
-- The schema mirrors the MySQL baseline (migrations/000_..038_) so the
-- application code is dialect-agnostic; the data layer in
-- internal/pkg/xdb/db.go selects the right migrator at runtime.

-- ============================================================
-- Core identity: users / tokens / oauth identities
-- ============================================================

CREATE TABLE IF NOT EXISTS users (
  id INTEGER PRIMARY KEY AUTOINCREMENT,
  username TEXT NOT NULL UNIQUE,
  display_name TEXT DEFAULT '',
  email TEXT DEFAULT '',
  "group" TEXT DEFAULT 'default',
  status INTEGER DEFAULT 0,
  password_hash TEXT DEFAULT '',
  oauth_provider TEXT DEFAULT '',
  oauth_id TEXT DEFAULT '',
  balance INTEGER DEFAULT 0,
  used_amount INTEGER DEFAULT 0,
  request_count INTEGER DEFAULT 0,
  frozen_amount INTEGER DEFAULT 0,
  aff_code TEXT DEFAULT '',
  inviter_id INTEGER DEFAULT 0,
  role INTEGER NOT NULL DEFAULT 1
);

CREATE INDEX IF NOT EXISTS idx_users_oauth_provider ON users(oauth_provider);
CREATE INDEX IF NOT EXISTS idx_users_oauth_id       ON users(oauth_id);
CREATE INDEX IF NOT EXISTS idx_users_aff_code       ON users(aff_code);
CREATE INDEX IF NOT EXISTS idx_users_inviter_id     ON users(inviter_id);

CREATE TABLE IF NOT EXISTS tokens (
  id INTEGER PRIMARY KEY AUTOINCREMENT,
  user_id INTEGER NOT NULL,
  "key" TEXT NOT NULL,
  status INTEGER DEFAULT 0,
  expired_time INTEGER DEFAULT 0,
  remain_quota INTEGER DEFAULT 0,
  unlimited_quota INTEGER DEFAULT 0,
  models TEXT DEFAULT NULL,
  name TEXT DEFAULT '',
  created_at INTEGER DEFAULT 0,
  created_time INTEGER DEFAULT 0,
  accessed_time INTEGER DEFAULT 0,
  used_quota INTEGER DEFAULT 0,
  subnet TEXT DEFAULT ''
);

CREATE INDEX IF NOT EXISTS idx_tokens_user_id ON tokens(user_id);
CREATE INDEX IF NOT EXISTS idx_tokens_key     ON tokens("key");

CREATE TABLE IF NOT EXISTS user_oauth_identities (
  id INTEGER PRIMARY KEY AUTOINCREMENT,
  user_id INTEGER NOT NULL,
  provider TEXT NOT NULL,
  provider_id TEXT NOT NULL,
  created_at INTEGER NOT NULL DEFAULT 0,
  updated_at INTEGER NOT NULL DEFAULT 0,
  UNIQUE (provider, provider_id),
  UNIQUE (user_id, provider)
);

CREATE INDEX IF NOT EXISTS idx_user_oauth_identities_user_id ON user_oauth_identities(user_id);

-- ============================================================
-- Channels + abilities
-- ============================================================

CREATE TABLE IF NOT EXISTS channels (
  id INTEGER PRIMARY KEY AUTOINCREMENT,
  type INTEGER DEFAULT 0,
  "key" TEXT DEFAULT NULL,
  status INTEGER DEFAULT 0,
  name TEXT DEFAULT '',
  base_url TEXT DEFAULT NULL,
  models TEXT DEFAULT NULL,
  "group" TEXT DEFAULT 'default',
  priority INTEGER DEFAULT 0,
  config TEXT DEFAULT NULL,
  weight INTEGER DEFAULT 0,
  created_time INTEGER DEFAULT 0,
  test_time INTEGER DEFAULT 0,
  response_time INTEGER DEFAULT 0,
  balance REAL DEFAULT 0,
  balance_updated_time INTEGER DEFAULT 0,
  used_quota INTEGER DEFAULT 0,
  models_text TEXT DEFAULT '',
  group_col TEXT DEFAULT 'default',
  proxy_url TEXT DEFAULT '',
  open_ai_organization TEXT DEFAULT '',
  open_ai_limit_rps INTEGER DEFAULT 0,
  channel_balance_warning_threshold REAL DEFAULT 0,
  last_test_run_at INTEGER DEFAULT 0,
  balance_refresh_status TEXT DEFAULT '',
  balance_refresh_last_error TEXT,
  balance_refresh_last_success_time INTEGER DEFAULT 0,
  consecutive_balance_refresh_failures INTEGER DEFAULT 0,
  health_status TEXT NOT NULL DEFAULT 'healthy',
  health_last_error TEXT,
  health_last_success_time INTEGER DEFAULT 0,
  health_last_failure_time INTEGER DEFAULT 0,
  health_consecutive_failures INTEGER DEFAULT 0,
  circuit_opened_until INTEGER DEFAULT 0
);

CREATE INDEX IF NOT EXISTS idx_channels_health_status       ON channels(health_status);
CREATE INDEX IF NOT EXISTS idx_channels_circuit_opened_until ON channels(circuit_opened_until);

CREATE TABLE IF NOT EXISTS abilities (
  id INTEGER PRIMARY KEY AUTOINCREMENT,
  "group" TEXT NOT NULL DEFAULT 'default',
  model TEXT NOT NULL,
  channel_id INTEGER NOT NULL,
  enabled INTEGER DEFAULT 1,
  priority INTEGER DEFAULT 0
);

CREATE INDEX IF NOT EXISTS idx_abilities_channel_id  ON abilities(channel_id);
CREATE INDEX IF NOT EXISTS idx_abilities_model_group  ON abilities(model, "group");

-- ============================================================
-- Configs
-- ============================================================

CREATE TABLE IF NOT EXISTS configs (
  id INTEGER PRIMARY KEY AUTOINCREMENT,
  namespace TEXT NOT NULL DEFAULT '',
  "key" TEXT NOT NULL,
  value TEXT NOT NULL,
  comment TEXT DEFAULT '',
  updated_at INTEGER DEFAULT 0
);

CREATE INDEX IF NOT EXISTS idx_configs_namespace ON configs(namespace);
CREATE INDEX IF NOT EXISTS idx_configs_key       ON configs("key");

-- ============================================================
-- Logs
-- ============================================================

CREATE TABLE IF NOT EXISTS logs (
  id INTEGER PRIMARY KEY AUTOINCREMENT,
  level TEXT NOT NULL DEFAULT 'info',
  message TEXT NOT NULL,
  source TEXT DEFAULT '',
  request_id TEXT DEFAULT '',
  user_id INTEGER DEFAULT 0,
  created_at INTEGER DEFAULT 0,
  username TEXT DEFAULT '',
  token_name TEXT DEFAULT '',
  model_name TEXT DEFAULT '',
  quota INTEGER DEFAULT 0,
  prompt_tokens INTEGER DEFAULT 0,
  completion_tokens INTEGER DEFAULT 0,
  cache_read_tokens INTEGER DEFAULT 0,
  channel_id INTEGER DEFAULT 0,
  subscription_account_id INTEGER NOT NULL DEFAULT 0,
  elapsed_time INTEGER DEFAULT 0,
  is_stream INTEGER DEFAULT 0
);

CREATE INDEX IF NOT EXISTS idx_logs_level         ON logs(level);
CREATE INDEX IF NOT EXISTS idx_logs_source        ON logs(source);
CREATE INDEX IF NOT EXISTS idx_logs_created_at    ON logs(created_at);
CREATE INDEX IF NOT EXISTS idx_logs_user_id       ON logs(user_id);
CREATE INDEX IF NOT EXISTS idx_logs_user_created  ON logs(user_id, created_at);
CREATE INDEX IF NOT EXISTS idx_logs_request_id    ON logs(request_id);
CREATE INDEX IF NOT EXISTS idx_logs_model_name    ON logs(model_name);
CREATE INDEX IF NOT EXISTS idx_logs_subscription_account_created
  ON logs(subscription_account_id, created_at);

-- ============================================================
-- Notifications
-- ============================================================

CREATE TABLE IF NOT EXISTS notifications (
  id INTEGER PRIMARY KEY AUTOINCREMENT,
  type TEXT NOT NULL DEFAULT '',
  recipient TEXT NOT NULL DEFAULT '',
  subject TEXT DEFAULT '',
  content TEXT DEFAULT NULL,
  status TEXT NOT NULL DEFAULT 'pending',
  retry_count INTEGER DEFAULT 0,
  last_error TEXT NOT NULL DEFAULT '',
  created_at INTEGER DEFAULT 0,
  sent_at INTEGER DEFAULT 0
);

CREATE INDEX IF NOT EXISTS idx_notifications_type        ON notifications(type);
CREATE INDEX IF NOT EXISTS idx_notifications_status      ON notifications(status);
CREATE INDEX IF NOT EXISTS idx_notifications_created_at  ON notifications(created_at);

-- ============================================================
-- Health checks / alert rules
-- ============================================================

CREATE TABLE IF NOT EXISTS health_checks (
  id INTEGER PRIMARY KEY AUTOINCREMENT,
  service_name TEXT NOT NULL,
  status TEXT NOT NULL DEFAULT 'unknown',
  response_time INTEGER DEFAULT 0,
  checked_at INTEGER DEFAULT 0
);

CREATE INDEX IF NOT EXISTS idx_health_checks_service_name ON health_checks(service_name);
CREATE INDEX IF NOT EXISTS idx_health_checks_checked_at   ON health_checks(checked_at);

CREATE TABLE IF NOT EXISTS alert_rules (
  id INTEGER PRIMARY KEY AUTOINCREMENT,
  name TEXT NOT NULL DEFAULT '',
  service_name TEXT NOT NULL DEFAULT '',
  metric TEXT NOT NULL DEFAULT '',
  threshold REAL DEFAULT 0,
  operator TEXT NOT NULL DEFAULT '>',
  duration INTEGER DEFAULT 0,
  enabled INTEGER DEFAULT 1,
  created_at INTEGER DEFAULT 0
);

-- ============================================================
-- System options (admin API)
-- ============================================================

CREATE TABLE IF NOT EXISTS system_options (
  id INTEGER PRIMARY KEY AUTOINCREMENT,
  option_key TEXT NOT NULL UNIQUE,
  option_value TEXT NOT NULL,
  updated_at INTEGER DEFAULT 0
);

-- ============================================================
-- Billing
-- ============================================================

CREATE TABLE IF NOT EXISTS billing_reservations (
  id INTEGER PRIMARY KEY AUTOINCREMENT,
  reservation_id TEXT NOT NULL UNIQUE,
  user_id TEXT NOT NULL,
  request_id TEXT NOT NULL,
  amount INTEGER NOT NULL,
  status TEXT NOT NULL,
  model TEXT DEFAULT NULL,
  channel_id TEXT DEFAULT NULL,
  created_at INTEGER DEFAULT 0,
  updated_at INTEGER DEFAULT 0,
  expired_at INTEGER DEFAULT 0,
  subscription_account_id TEXT DEFAULT '0'
);

CREATE INDEX IF NOT EXISTS idx_billing_reservations_user_id       ON billing_reservations(user_id);
CREATE INDEX IF NOT EXISTS idx_billing_reservations_request_id     ON billing_reservations(request_id);
CREATE INDEX IF NOT EXISTS idx_billing_reservations_status_expired ON billing_reservations(status, expired_at);

CREATE TABLE IF NOT EXISTS billing_ledgers (
  id INTEGER PRIMARY KEY AUTOINCREMENT,
  user_id TEXT NOT NULL,
  amount INTEGER NOT NULL,
  balance_after INTEGER NOT NULL,
  type TEXT NOT NULL,
  reference_id TEXT DEFAULT NULL,
  remark TEXT,
  created_at INTEGER DEFAULT 0,
  token_name TEXT DEFAULT '',
  model_name TEXT DEFAULT '',
  quota INTEGER DEFAULT 0,
  prompt_tokens INTEGER DEFAULT 0,
  completion_tokens INTEGER DEFAULT 0,
  cache_read_tokens INTEGER DEFAULT 0,
  channel_id INTEGER DEFAULT 0,
  subscription_account_id INTEGER NOT NULL DEFAULT 0,
  elapsed_time INTEGER DEFAULT 0,
  is_stream INTEGER DEFAULT 0,
  endpoint TEXT DEFAULT '',
  upstream_cost INTEGER DEFAULT 0
);

CREATE INDEX IF NOT EXISTS idx_billing_ledgers_user_id           ON billing_ledgers(user_id);
CREATE INDEX IF NOT EXISTS idx_billing_ledgers_type              ON billing_ledgers(type);
CREATE INDEX IF NOT EXISTS idx_billing_ledgers_created_at        ON billing_ledgers(created_at);
CREATE INDEX IF NOT EXISTS idx_billing_ledgers_reference_id      ON billing_ledgers(reference_id);
CREATE INDEX IF NOT EXISTS idx_billing_ledgers_user_created_model ON billing_ledgers(user_id, created_at, model_name);
CREATE INDEX IF NOT EXISTS idx_billing_ledgers_type_created       ON billing_ledgers(type, created_at);
CREATE INDEX IF NOT EXISTS idx_billing_ledgers_channel_created    ON billing_ledgers(channel_id, created_at);
CREATE INDEX IF NOT EXISTS idx_billing_ledgers_model_created      ON billing_ledgers(model_name, created_at);
CREATE INDEX IF NOT EXISTS idx_billing_ledgers_upstream_cost_created
  ON billing_ledgers(upstream_cost, created_at);
CREATE INDEX IF NOT EXISTS idx_billing_ledgers_subscription_account_created
  ON billing_ledgers(subscription_account_id, created_at);

CREATE TABLE IF NOT EXISTS billing_redeem_codes (
  id INTEGER PRIMARY KEY AUTOINCREMENT,
  code TEXT NOT NULL UNIQUE,
  name TEXT DEFAULT NULL,
  amount INTEGER NOT NULL,
  count INTEGER NOT NULL,
  status INTEGER NOT NULL DEFAULT 1,
  created_by TEXT DEFAULT NULL,
  created_at INTEGER DEFAULT 0,
  updated_at INTEGER DEFAULT 0
);

CREATE INDEX IF NOT EXISTS idx_billing_redeem_codes_name              ON billing_redeem_codes(name);
CREATE INDEX IF NOT EXISTS idx_billing_redeem_codes_status            ON billing_redeem_codes(status);
CREATE INDEX IF NOT EXISTS idx_billing_redeem_codes_created_at        ON billing_redeem_codes(created_at);
CREATE INDEX IF NOT EXISTS idx_billing_redeem_codes_status_created_at ON billing_redeem_codes(status, created_at);

CREATE TABLE IF NOT EXISTS billing_redeem_records (
  id INTEGER PRIMARY KEY AUTOINCREMENT,
  user_id TEXT NOT NULL,
  code TEXT NOT NULL,
  amount INTEGER NOT NULL,
  balance_before INTEGER NOT NULL,
  balance_after INTEGER NOT NULL,
  created_at INTEGER DEFAULT 0
);

CREATE INDEX IF NOT EXISTS idx_billing_redeem_records_user_id    ON billing_redeem_records(user_id);
CREATE INDEX IF NOT EXISTS idx_billing_redeem_records_code       ON billing_redeem_records(code);
CREATE INDEX IF NOT EXISTS idx_billing_redeem_records_created_at ON billing_redeem_records(created_at);

-- ============================================================
-- Payment orders
-- ============================================================

CREATE TABLE IF NOT EXISTS payment_orders (
  id INTEGER PRIMARY KEY AUTOINCREMENT,
  user_id INTEGER NOT NULL,
  trade_no TEXT NOT NULL UNIQUE,
  channel TEXT NOT NULL,
  asset_type TEXT NOT NULL,
  asset_amount INTEGER NOT NULL,
  money_cents INTEGER NOT NULL,
  currency TEXT NOT NULL DEFAULT 'CNY',
  status TEXT NOT NULL,
  provider_trade_no TEXT DEFAULT '',
  provider_payload TEXT,
  pay_url TEXT,
  paid_at INTEGER DEFAULT 0,
  created_at INTEGER NOT NULL DEFAULT 0,
  updated_at INTEGER NOT NULL DEFAULT 0,
  asset_issue_status TEXT NOT NULL DEFAULT 'pending',
  group_id INTEGER NOT NULL DEFAULT 0
);

CREATE INDEX IF NOT EXISTS idx_payment_orders_user_id                ON payment_orders(user_id);
CREATE INDEX IF NOT EXISTS idx_payment_orders_channel               ON payment_orders(channel);
CREATE INDEX IF NOT EXISTS idx_payment_orders_asset_type            ON payment_orders(asset_type);
CREATE INDEX IF NOT EXISTS idx_payment_orders_status                ON payment_orders(status);
CREATE INDEX IF NOT EXISTS idx_payment_orders_provider_trade_no     ON payment_orders(provider_trade_no);
CREATE INDEX IF NOT EXISTS idx_payment_orders_paid_at               ON payment_orders(paid_at);
CREATE INDEX IF NOT EXISTS idx_payment_orders_asset_issue_status    ON payment_orders(asset_issue_status);
CREATE INDEX IF NOT EXISTS idx_payment_orders_group_id              ON payment_orders(group_id);

-- ============================================================
-- Reconciliation runs
-- ============================================================

CREATE TABLE IF NOT EXISTS reconciliation_runs (
  id INTEGER PRIMARY KEY AUTOINCREMENT,
  run_at INTEGER NOT NULL DEFAULT 0,
  expired_cleaned INTEGER NOT NULL DEFAULT 0,
  total_accounts INTEGER NOT NULL DEFAULT 0,
  total_channels INTEGER NOT NULL DEFAULT 0,
  total_reservations INTEGER NOT NULL DEFAULT 0,
  discrepancy_count INTEGER NOT NULL DEFAULT 0,
  discrepancies TEXT,
  created_at INTEGER NOT NULL DEFAULT 0
);

CREATE INDEX IF NOT EXISTS idx_reconciliation_runs_run_at ON reconciliation_runs(run_at);

-- ============================================================
-- Subscription accounts
-- ============================================================

CREATE TABLE IF NOT EXISTS subscription_accounts (
  id INTEGER PRIMARY KEY AUTOINCREMENT,
  name TEXT NOT NULL DEFAULT '',
  platform TEXT NOT NULL,
  account_type TEXT NOT NULL DEFAULT 'oauth',
  status INTEGER DEFAULT 1,
  "group" TEXT DEFAULT 'default',
  models TEXT DEFAULT NULL,
  priority INTEGER DEFAULT 0,
  base_url TEXT DEFAULT NULL,
  access_token TEXT DEFAULT NULL,
  refresh_token TEXT DEFAULT NULL,
  expires_at INTEGER DEFAULT 0,
  account_id TEXT DEFAULT '',
  fingerprint TEXT DEFAULT NULL,
  metadata TEXT DEFAULT NULL,
  created_at INTEGER DEFAULT 0,
  updated_at INTEGER DEFAULT 0,
  concurrency INTEGER NOT NULL DEFAULT 1,
  rate_limited_until INTEGER NOT NULL DEFAULT 0,
  quota_used_percent REAL NOT NULL DEFAULT 0,
  quota_reset_at INTEGER NOT NULL DEFAULT 0,
  last_used_at INTEGER NOT NULL DEFAULT 0
);

CREATE INDEX IF NOT EXISTS idx_subscription_platform_status ON subscription_accounts(platform, status);
CREATE INDEX IF NOT EXISTS idx_subscription_group          ON subscription_accounts("group");
CREATE INDEX IF NOT EXISTS idx_subscription_expires        ON subscription_accounts(expires_at);
CREATE INDEX IF NOT EXISTS idx_subscription_rate_limited  ON subscription_accounts(rate_limited_until);

CREATE TABLE IF NOT EXISTS subscription_account_abilities (
  id INTEGER PRIMARY KEY AUTOINCREMENT,
  "group" TEXT NOT NULL DEFAULT 'default',
  model TEXT NOT NULL,
  platform TEXT NOT NULL,
  account_id INTEGER NOT NULL,
  enabled INTEGER DEFAULT 1,
  priority INTEGER DEFAULT 0
);

CREATE INDEX IF NOT EXISTS idx_subscription_account_abilities_account_id
  ON subscription_account_abilities(account_id);
CREATE INDEX IF NOT EXISTS idx_subscription_account_abilities_model_group_platform
  ON subscription_account_abilities(model, "group", platform);

-- ============================================================
-- User subscriptions / groups
-- ============================================================

CREATE TABLE IF NOT EXISTS user_subscriptions (
  id INTEGER PRIMARY KEY AUTOINCREMENT,
  user_id INTEGER NOT NULL,
  group_id INTEGER NOT NULL,
  subscription_name TEXT NOT NULL DEFAULT '',
  status TEXT NOT NULL DEFAULT 'active',
  starts_at INTEGER NOT NULL DEFAULT 0,
  expires_at INTEGER NOT NULL DEFAULT 0,
  daily_usage_usd REAL NOT NULL DEFAULT 0,
  weekly_usage_usd REAL NOT NULL DEFAULT 0,
  monthly_usage_usd REAL NOT NULL DEFAULT 0,
  daily_window_start INTEGER NOT NULL DEFAULT 0,
  weekly_window_start INTEGER NOT NULL DEFAULT 0,
  monthly_window_start INTEGER NOT NULL DEFAULT 0,
  metadata TEXT,
  created_at INTEGER NOT NULL DEFAULT 0,
  updated_at INTEGER NOT NULL DEFAULT 0
);

CREATE INDEX IF NOT EXISTS idx_user_subs_user_id ON user_subscriptions(user_id);
CREATE INDEX IF NOT EXISTS idx_user_subs_group_id ON user_subscriptions(group_id);
CREATE INDEX IF NOT EXISTS idx_user_subs_status ON user_subscriptions(status);
CREATE INDEX IF NOT EXISTS idx_user_subs_expires_at ON user_subscriptions(expires_at);

CREATE TABLE IF NOT EXISTS subscription_groups (
  id INTEGER PRIMARY KEY AUTOINCREMENT,
  name TEXT NOT NULL UNIQUE,
  display_name TEXT NOT NULL DEFAULT '',
  platform TEXT NOT NULL,
  subscription_type TEXT NOT NULL DEFAULT 'standard',
  daily_limit_usd REAL DEFAULT NULL,
  weekly_limit_usd REAL DEFAULT NULL,
  monthly_limit_usd REAL DEFAULT NULL,
  rate_multiplier REAL NOT NULL DEFAULT 1.0,
  status INTEGER NOT NULL DEFAULT 1,
  created_at INTEGER NOT NULL DEFAULT 0,
  updated_at INTEGER NOT NULL DEFAULT 0
);

CREATE INDEX IF NOT EXISTS idx_sub_groups_platform ON subscription_groups(platform);

CREATE TABLE IF NOT EXISTS account_quota_snapshots (
  account_id INTEGER PRIMARY KEY,
  primary_used_percent REAL,
  primary_reset_after_seconds INTEGER,
  primary_window_minutes INTEGER,
  secondary_used_percent REAL,
  secondary_reset_after_seconds INTEGER,
  secondary_window_minutes INTEGER,
  primary_over_secondary_percent REAL,
  updated_at DATETIME,
  snapshot_paused INTEGER NOT NULL DEFAULT 0
);

CREATE INDEX IF NOT EXISTS idx_account_quota_snapshot_updated
  ON account_quota_snapshots(updated_at);

-- ============================================================
-- Schema migrations bookkeeping (matches internal/pkg/migrate)
-- ============================================================

CREATE TABLE IF NOT EXISTS schema_migrations (
  version TEXT NOT NULL PRIMARY KEY,
  applied_at INTEGER NOT NULL DEFAULT 0
);

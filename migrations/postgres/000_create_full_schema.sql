-- Consolidated baseline schema for Postgres (Lite / Production deployment).
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
-- Instead, the Postgres baseline is a single hand-written snapshot. Future
-- schema changes should add a new numbered .sql file alongside this one
-- in migrations/postgres/ and the runner will apply them in order.
--
-- Conventions:
--   * BIGSERIAL/GENERATED ALWAYS AS IDENTITY for auto-incrementing PKs.
--   * TEXT for variable-length strings; VARCHAR(N) only when an index needs
--     a length cap.
--   * Foreign keys must be enabled; Postgres enables them by default but
--     pragma-style enforcement is not required.
--   * No MySQL-only DDL (PARTITION BY, ENGINE=, COMMENT, etc.).
--   * Booleans use BOOLEAN, not TINYINT(1).
--   * Timestamps use BIGINT (epoch seconds or ms) to match application
--     code that stores time.Time via Unix().

-- ============================================================
-- Core identity: users / tokens / oauth identities
-- ============================================================

CREATE TABLE IF NOT EXISTS users (
  id BIGSERIAL PRIMARY KEY,
  username TEXT NOT NULL UNIQUE,
  display_name TEXT DEFAULT '',
  email TEXT DEFAULT '',
  "group" TEXT DEFAULT 'default',
  status INTEGER DEFAULT 0,
  password_hash TEXT DEFAULT '',
  oauth_provider TEXT DEFAULT '',
  oauth_id TEXT DEFAULT '',
  quota BIGINT DEFAULT 0,
  used_quota BIGINT DEFAULT 0,
  request_count BIGINT DEFAULT 0,
  frozen_quota BIGINT DEFAULT 0,
  aff_code TEXT DEFAULT '',
  inviter_id BIGINT DEFAULT 0,
  role INTEGER NOT NULL DEFAULT 1
);

CREATE INDEX IF NOT EXISTS idx_users_oauth_provider ON users(oauth_provider);
CREATE INDEX IF NOT EXISTS idx_users_oauth_id       ON users(oauth_id);
CREATE INDEX IF NOT EXISTS idx_users_aff_code       ON users(aff_code);
CREATE INDEX IF NOT EXISTS idx_users_inviter_id     ON users(inviter_id);

CREATE TABLE IF NOT EXISTS tokens (
  id BIGSERIAL PRIMARY KEY,
  user_id BIGINT NOT NULL,
  "key" TEXT NOT NULL,
  status INTEGER DEFAULT 0,
  expired_time BIGINT DEFAULT 0,
  remain_quota BIGINT DEFAULT 0,
  unlimited_quota INTEGER DEFAULT 0,
  models TEXT DEFAULT NULL,
  name TEXT DEFAULT '',
  created_at BIGINT DEFAULT 0,
  created_time BIGINT DEFAULT 0,
  accessed_time BIGINT DEFAULT 0,
  used_quota BIGINT DEFAULT 0,
  subnet TEXT DEFAULT ''
);

CREATE INDEX IF NOT EXISTS idx_tokens_user_id ON tokens(user_id);
CREATE INDEX IF NOT EXISTS idx_tokens_key     ON tokens("key");

CREATE TABLE IF NOT EXISTS user_oauth_identities (
  id BIGSERIAL PRIMARY KEY,
  user_id BIGINT NOT NULL,
  provider TEXT NOT NULL,
  provider_id TEXT NOT NULL,
  created_at BIGINT NOT NULL DEFAULT 0,
  updated_at BIGINT NOT NULL DEFAULT 0,
  CONSTRAINT uk_provider_provider_id UNIQUE (provider, provider_id),
  CONSTRAINT uk_user_provider UNIQUE (user_id, provider)
);

CREATE INDEX IF NOT EXISTS idx_user_oauth_identities_user_id ON user_oauth_identities(user_id);

-- ============================================================
-- Channels + abilities
-- ============================================================

CREATE TABLE IF NOT EXISTS channels (
  id BIGSERIAL PRIMARY KEY,
  type INTEGER DEFAULT 0,
  "key" TEXT DEFAULT NULL,
  status INTEGER DEFAULT 0,
  name TEXT DEFAULT '',
  base_url TEXT DEFAULT NULL,
  models TEXT DEFAULT NULL,
  "group" TEXT DEFAULT 'default',
  priority BIGINT DEFAULT 0,
  config TEXT DEFAULT NULL,
  weight INTEGER DEFAULT 0,
  created_time BIGINT DEFAULT 0,
  test_time BIGINT DEFAULT 0,
  response_time BIGINT DEFAULT 0,
  balance DOUBLE PRECISION DEFAULT 0,
  balance_updated_time BIGINT DEFAULT 0,
  used_quota BIGINT DEFAULT 0,
  models_text TEXT DEFAULT '',
  group_col TEXT DEFAULT 'default',
  proxy_url TEXT DEFAULT '',
  open_ai_organization TEXT DEFAULT '',
  open_ai_limit_rps INTEGER DEFAULT 0,
  channel_balance_warning_threshold DOUBLE PRECISION DEFAULT 0,
  last_test_run_at BIGINT DEFAULT 0,
  balance_refresh_status TEXT DEFAULT '',
  balance_refresh_last_error TEXT,
  balance_refresh_last_success_time BIGINT DEFAULT 0,
  consecutive_balance_refresh_failures INTEGER DEFAULT 0,
  health_status TEXT NOT NULL DEFAULT 'healthy',
  health_last_error TEXT,
  health_last_success_time BIGINT DEFAULT 0,
  health_last_failure_time BIGINT DEFAULT 0,
  health_consecutive_failures INTEGER DEFAULT 0,
  circuit_opened_until BIGINT DEFAULT 0
);

CREATE INDEX IF NOT EXISTS idx_channels_health_status        ON channels(health_status);
CREATE INDEX IF NOT EXISTS idx_channels_circuit_opened_until ON channels(circuit_opened_until);

CREATE TABLE IF NOT EXISTS abilities (
  id BIGSERIAL PRIMARY KEY,
  "group" TEXT NOT NULL DEFAULT 'default',
  model TEXT NOT NULL,
  channel_id BIGINT NOT NULL,
  enabled INTEGER DEFAULT 1,
  priority BIGINT DEFAULT 0
);

CREATE INDEX IF NOT EXISTS idx_abilities_channel_id  ON abilities(channel_id);
CREATE INDEX IF NOT EXISTS idx_abilities_model_group ON abilities(model, "group");

-- ============================================================
-- Configs
-- ============================================================

CREATE TABLE IF NOT EXISTS configs (
  id BIGSERIAL PRIMARY KEY,
  namespace TEXT NOT NULL DEFAULT '',
  "key" TEXT NOT NULL,
  value TEXT NOT NULL,
  comment TEXT DEFAULT '',
  updated_at BIGINT DEFAULT 0
);

CREATE INDEX IF NOT EXISTS idx_configs_namespace ON configs(namespace);
CREATE INDEX IF NOT EXISTS idx_configs_key       ON configs("key");

-- ============================================================
-- Logs
-- ============================================================

CREATE TABLE IF NOT EXISTS logs (
  id BIGSERIAL PRIMARY KEY,
  level TEXT NOT NULL DEFAULT 'info',
  message TEXT NOT NULL,
  source TEXT DEFAULT '',
  request_id TEXT DEFAULT '',
  user_id BIGINT DEFAULT 0,
  created_at BIGINT DEFAULT 0,
  username TEXT DEFAULT '',
  token_name TEXT DEFAULT '',
  model_name TEXT DEFAULT '',
  quota BIGINT DEFAULT 0,
  prompt_tokens BIGINT DEFAULT 0,
  completion_tokens BIGINT DEFAULT 0,
  cache_read_tokens BIGINT DEFAULT 0,
  channel_id BIGINT DEFAULT 0,
  subscription_account_id BIGINT NOT NULL DEFAULT 0,
  elapsed_time BIGINT DEFAULT 0,
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
  id BIGSERIAL PRIMARY KEY,
  type TEXT NOT NULL DEFAULT '',
  recipient TEXT NOT NULL DEFAULT '',
  subject TEXT DEFAULT '',
  content TEXT DEFAULT NULL,
  status TEXT NOT NULL DEFAULT 'pending',
  retry_count INTEGER DEFAULT 0,
  last_error TEXT NOT NULL DEFAULT '',
  created_at BIGINT DEFAULT 0,
  sent_at BIGINT DEFAULT 0
);

CREATE INDEX IF NOT EXISTS idx_notifications_type        ON notifications(type);
CREATE INDEX IF NOT EXISTS idx_notifications_status      ON notifications(status);
CREATE INDEX IF NOT EXISTS idx_notifications_created_at  ON notifications(created_at);

-- ============================================================
-- Health checks / alert rules
-- ============================================================

CREATE TABLE IF NOT EXISTS health_checks (
  id BIGSERIAL PRIMARY KEY,
  service_name TEXT NOT NULL,
  status TEXT NOT NULL DEFAULT 'unknown',
  response_time BIGINT DEFAULT 0,
  checked_at BIGINT DEFAULT 0
);

CREATE INDEX IF NOT EXISTS idx_health_checks_service_name ON health_checks(service_name);
CREATE INDEX IF NOT EXISTS idx_health_checks_checked_at   ON health_checks(checked_at);

CREATE TABLE IF NOT EXISTS alert_rules (
  id BIGSERIAL PRIMARY KEY,
  name TEXT NOT NULL DEFAULT '',
  service_name TEXT NOT NULL DEFAULT '',
  metric TEXT NOT NULL DEFAULT '',
  threshold DOUBLE PRECISION DEFAULT 0,
  operator TEXT NOT NULL DEFAULT '>',
  duration INTEGER DEFAULT 0,
  enabled INTEGER DEFAULT 1,
  created_at BIGINT DEFAULT 0
);

-- ============================================================
-- System options (admin API)
-- ============================================================

CREATE TABLE IF NOT EXISTS system_options (
  id BIGSERIAL PRIMARY KEY,
  option_key TEXT NOT NULL UNIQUE,
  option_value TEXT NOT NULL,
  updated_at BIGINT DEFAULT 0
);

-- ============================================================
-- Billing
-- ============================================================

CREATE TABLE IF NOT EXISTS billing_reservations (
  id BIGSERIAL PRIMARY KEY,
  reservation_id TEXT NOT NULL UNIQUE,
  user_id TEXT NOT NULL,
  request_id TEXT NOT NULL,
  amount BIGINT NOT NULL,
  status TEXT NOT NULL,
  model TEXT DEFAULT NULL,
  channel_id TEXT DEFAULT NULL,
  created_at BIGINT DEFAULT 0,
  updated_at BIGINT DEFAULT 0,
  expired_at BIGINT DEFAULT 0,
  subscription_account_id TEXT DEFAULT '0'
);

CREATE INDEX IF NOT EXISTS idx_billing_reservations_user_id       ON billing_reservations(user_id);
CREATE INDEX IF NOT EXISTS idx_billing_reservations_request_id     ON billing_reservations(request_id);
CREATE INDEX IF NOT EXISTS idx_billing_reservations_status_expired ON billing_reservations(status, expired_at);

CREATE TABLE IF NOT EXISTS billing_ledgers (
  id BIGSERIAL PRIMARY KEY,
  user_id TEXT NOT NULL,
  amount BIGINT NOT NULL,
  balance_after BIGINT NOT NULL,
  type TEXT NOT NULL,
  reference_id TEXT DEFAULT NULL,
  remark TEXT,
  created_at BIGINT DEFAULT 0,
  token_name TEXT DEFAULT '',
  model_name TEXT DEFAULT '',
  quota BIGINT DEFAULT 0,
  prompt_tokens BIGINT DEFAULT 0,
  completion_tokens BIGINT DEFAULT 0,
  cache_read_tokens BIGINT DEFAULT 0,
  channel_id BIGINT DEFAULT 0,
  subscription_account_id BIGINT NOT NULL DEFAULT 0,
  elapsed_time BIGINT DEFAULT 0,
  is_stream INTEGER DEFAULT 0,
  endpoint TEXT DEFAULT '',
  upstream_cost BIGINT DEFAULT 0
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
  id BIGSERIAL PRIMARY KEY,
  code TEXT NOT NULL UNIQUE,
  name TEXT DEFAULT NULL,
  amount BIGINT NOT NULL,
  count INTEGER NOT NULL,
  status INTEGER NOT NULL DEFAULT 1,
  created_by TEXT DEFAULT NULL,
  created_at BIGINT DEFAULT 0,
  updated_at BIGINT DEFAULT 0
);

CREATE INDEX IF NOT EXISTS idx_billing_redeem_codes_name              ON billing_redeem_codes(name);
CREATE INDEX IF NOT EXISTS idx_billing_redeem_codes_status            ON billing_redeem_codes(status);
CREATE INDEX IF NOT EXISTS idx_billing_redeem_codes_created_at        ON billing_redeem_codes(created_at);
CREATE INDEX IF NOT EXISTS idx_billing_redeem_codes_status_created_at ON billing_redeem_codes(status, created_at);

CREATE TABLE IF NOT EXISTS billing_redeem_records (
  id BIGSERIAL PRIMARY KEY,
  user_id TEXT NOT NULL,
  code TEXT NOT NULL,
  amount BIGINT NOT NULL,
  quota_before BIGINT NOT NULL,
  quota_after BIGINT NOT NULL,
  created_at BIGINT DEFAULT 0
);

CREATE INDEX IF NOT EXISTS idx_billing_redeem_records_user_id    ON billing_redeem_records(user_id);
CREATE INDEX IF NOT EXISTS idx_billing_redeem_records_code       ON billing_redeem_records(code);
CREATE INDEX IF NOT EXISTS idx_billing_redeem_records_created_at ON billing_redeem_records(created_at);

-- ============================================================
-- Payment orders
-- ============================================================

CREATE TABLE IF NOT EXISTS payment_orders (
  id BIGSERIAL PRIMARY KEY,
  user_id BIGINT NOT NULL,
  trade_no TEXT NOT NULL UNIQUE,
  channel TEXT NOT NULL,
  asset_type TEXT NOT NULL,
  asset_amount BIGINT NOT NULL,
  money_cents BIGINT NOT NULL,
  currency TEXT NOT NULL DEFAULT 'CNY',
  status TEXT NOT NULL,
  provider_trade_no TEXT DEFAULT '',
  provider_payload TEXT,
  pay_url TEXT,
  paid_at BIGINT DEFAULT 0,
  created_at BIGINT NOT NULL DEFAULT 0,
  updated_at BIGINT NOT NULL DEFAULT 0,
  asset_issue_status TEXT NOT NULL DEFAULT 'pending'
);

CREATE INDEX IF NOT EXISTS idx_payment_orders_user_id            ON payment_orders(user_id);
CREATE INDEX IF NOT EXISTS idx_payment_orders_channel           ON payment_orders(channel);
CREATE INDEX IF NOT EXISTS idx_payment_orders_asset_type        ON payment_orders(asset_type);
CREATE INDEX IF NOT EXISTS idx_payment_orders_status            ON payment_orders(status);
CREATE INDEX IF NOT EXISTS idx_payment_orders_provider_trade_no ON payment_orders(provider_trade_no);
CREATE INDEX IF NOT EXISTS idx_payment_orders_paid_at           ON payment_orders(paid_at);
CREATE INDEX IF NOT EXISTS idx_payment_orders_asset_issue_status ON payment_orders(asset_issue_status);

-- ============================================================
-- Reconciliation runs
-- ============================================================

CREATE TABLE IF NOT EXISTS reconciliation_runs (
  id BIGSERIAL PRIMARY KEY,
  run_at BIGINT NOT NULL DEFAULT 0,
  expired_cleaned INTEGER NOT NULL DEFAULT 0,
  total_accounts INTEGER NOT NULL DEFAULT 0,
  total_channels INTEGER NOT NULL DEFAULT 0,
  total_reservations INTEGER NOT NULL DEFAULT 0,
  discrepancy_count INTEGER NOT NULL DEFAULT 0,
  discrepancies TEXT,
  created_at BIGINT NOT NULL DEFAULT 0
);

CREATE INDEX IF NOT EXISTS idx_reconciliation_runs_run_at ON reconciliation_runs(run_at);

-- ============================================================
-- Subscription accounts
-- ============================================================

CREATE TABLE IF NOT EXISTS subscription_accounts (
  id BIGSERIAL PRIMARY KEY,
  name TEXT NOT NULL DEFAULT '',
  platform TEXT NOT NULL,
  account_type TEXT NOT NULL DEFAULT 'oauth',
  status INTEGER DEFAULT 1,
  "group" TEXT DEFAULT 'default',
  models TEXT DEFAULT NULL,
  priority BIGINT DEFAULT 0,
  base_url TEXT DEFAULT NULL,
  access_token TEXT DEFAULT NULL,
  refresh_token TEXT DEFAULT NULL,
  expires_at BIGINT DEFAULT 0,
  account_id TEXT DEFAULT '',
  fingerprint TEXT DEFAULT NULL,
  metadata TEXT DEFAULT NULL,
  created_at BIGINT DEFAULT 0,
  updated_at BIGINT DEFAULT 0,
  concurrency INTEGER NOT NULL DEFAULT 1,
  rate_limited_until BIGINT NOT NULL DEFAULT 0,
  quota_used_percent DOUBLE PRECISION NOT NULL DEFAULT 0,
  quota_reset_at BIGINT NOT NULL DEFAULT 0,
  last_used_at BIGINT NOT NULL DEFAULT 0
);

CREATE INDEX IF NOT EXISTS idx_subscription_platform_status ON subscription_accounts(platform, status);
CREATE INDEX IF NOT EXISTS idx_subscription_group          ON subscription_accounts("group");
CREATE INDEX IF NOT EXISTS idx_subscription_expires        ON subscription_accounts(expires_at);
CREATE INDEX IF NOT EXISTS idx_subscription_rate_limited  ON subscription_accounts(rate_limited_until);

CREATE TABLE IF NOT EXISTS subscription_account_abilities (
  id BIGSERIAL PRIMARY KEY,
  "group" TEXT NOT NULL DEFAULT 'default',
  model TEXT NOT NULL,
  platform TEXT NOT NULL,
  account_id BIGINT NOT NULL,
  enabled INTEGER DEFAULT 1,
  priority BIGINT DEFAULT 0
);

CREATE INDEX IF NOT EXISTS idx_subscription_account_abilities_account_id
  ON subscription_account_abilities(account_id);
CREATE INDEX IF NOT EXISTS idx_subscription_account_abilities_model_group_platform
  ON subscription_account_abilities(model, "group", platform);

-- ============================================================
-- Schema migrations bookkeeping (matches internal/pkg/migrate)
-- ============================================================

CREATE TABLE IF NOT EXISTS schema_migrations (
  version TEXT NOT NULL PRIMARY KEY,
  applied_at BIGINT NOT NULL DEFAULT 0
);

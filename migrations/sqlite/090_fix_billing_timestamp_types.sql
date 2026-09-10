-- Repair legacy INTEGER declarations for GORM time.Time fields. SQLite requires
-- a table rebuild to change a declared type. The migration runner executes this
-- entire file in one transaction. No amounts, identifiers or ledger claims change.
-- Strings written by GORM are copied verbatim, preserving sub-second precision.
-- Numeric timestamps are Unix seconds; nullable zero timestamps remain unset.
-- These tables have no incoming foreign keys. All existing repository indexes
-- are recreated below; application services must be stopped during upgrade.


-- Preserve AUTOINCREMENT high-water marks, including deleted rows.
CREATE TEMP TABLE lite_timestamp_sequences AS
SELECT name, seq FROM sqlite_sequence WHERE name IN (
  'billing_reservations', 'billing_ledgers', 'billing_redeem_codes',
  'billing_redeem_records', 'payment_orders', 'account_receivables'
);

CREATE TABLE billing_reservations_timestamp_v090 (
  id INTEGER PRIMARY KEY AUTOINCREMENT,
  reservation_id TEXT NOT NULL UNIQUE,
  user_id TEXT NOT NULL,
  request_id TEXT NOT NULL,
  amount INTEGER NOT NULL,
  status TEXT NOT NULL,
  model TEXT DEFAULT NULL,
  channel_id TEXT DEFAULT NULL,


  created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
  updated_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
  expired_at DATETIME DEFAULT NULL,
  subscription_account_id TEXT DEFAULT '0',
  subscription_id INTEGER NOT NULL DEFAULT 0,
  subscription_amount_usd REAL NOT NULL DEFAULT 0,
  subscription_daily_window_start INTEGER NOT NULL DEFAULT 0,
  subscription_weekly_window_start INTEGER NOT NULL DEFAULT 0,
  subscription_monthly_window_start INTEGER NOT NULL DEFAULT 0,
  balance_amount_quota INTEGER NOT NULL DEFAULT 0,
  balance_amount INTEGER NOT NULL DEFAULT 0,
  actual_cost INTEGER NOT NULL DEFAULT 0
);

INSERT INTO "billing_reservations_timestamp_v090" ("id", "reservation_id", "user_id", "request_id", "amount", "status", "model", "channel_id", "created_at", "updated_at", "expired_at", "subscription_account_id", "subscription_id", "subscription_amount_usd", "subscription_daily_window_start", "subscription_weekly_window_start", "subscription_monthly_window_start", "balance_amount_quota", "balance_amount", "actual_cost")
SELECT "id",
       "reservation_id",
       "user_id",
       "request_id",
       "amount",
       "status",
       "model",
       "channel_id",
       COALESCE(CASE WHEN typeof("created_at") IN ('integer', 'real') THEN strftime('%Y-%m-%d %H:%M:%f', "created_at", 'unixepoch') ELSE "created_at" END, '1970-01-01 00:00:00.000'),
       COALESCE(CASE WHEN typeof("updated_at") IN ('integer', 'real') THEN strftime('%Y-%m-%d %H:%M:%f', "updated_at", 'unixepoch') ELSE "updated_at" END, '1970-01-01 00:00:00.000'),
       CASE WHEN "expired_at" = 0 THEN NULL ELSE CASE WHEN typeof("expired_at") IN ('integer', 'real') THEN strftime('%Y-%m-%d %H:%M:%f', "expired_at", 'unixepoch') ELSE "expired_at" END END,
       "subscription_account_id",
       "subscription_id",
       "subscription_amount_usd",
       "subscription_daily_window_start",
       "subscription_weekly_window_start",
       "subscription_monthly_window_start",
       "balance_amount_quota",
       "balance_amount",
       "actual_cost"
FROM "billing_reservations";

DROP TABLE "billing_reservations";

ALTER TABLE "billing_reservations_timestamp_v090" RENAME TO "billing_reservations";

CREATE INDEX idx_billing_reservations_request_id     ON billing_reservations(request_id);

CREATE INDEX idx_billing_reservations_status_expired ON billing_reservations(status, expired_at);

CREATE INDEX idx_billing_reservations_subscription  ON billing_reservations(subscription_id, status);

CREATE INDEX idx_billing_reservations_user_id       ON billing_reservations(user_id);

CREATE INDEX idx_billing_reservations_user_status   ON billing_reservations(user_id, status);

CREATE UNIQUE INDEX uq_billing_reservations_user_request
  ON billing_reservations(user_id, request_id);

CREATE TABLE billing_ledgers_timestamp_v090 (
  id INTEGER PRIMARY KEY AUTOINCREMENT,
  user_id TEXT NOT NULL,
  amount INTEGER NOT NULL,
  balance_after INTEGER NOT NULL,
  type TEXT NOT NULL,
  reference_id TEXT DEFAULT NULL,
  remark TEXT,
  created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
  token_name TEXT DEFAULT '',
  model_name TEXT DEFAULT '',
  quota INTEGER DEFAULT 0,
  prompt_tokens INTEGER DEFAULT 0,
  completion_tokens INTEGER DEFAULT 0,
  cache_read_tokens INTEGER DEFAULT 0,
  cache_creation_5m_tokens INTEGER DEFAULT 0,
  cache_creation_1h_tokens INTEGER DEFAULT 0,
  channel_id INTEGER DEFAULT 0,
  subscription_account_id INTEGER NOT NULL DEFAULT 0,
  elapsed_time INTEGER DEFAULT 0,
  is_stream INTEGER DEFAULT 0,
  endpoint TEXT DEFAULT '',
  upstream_cost INTEGER DEFAULT 0,
  cost_source TEXT NOT NULL DEFAULT 'balance',
  subscription_cost INTEGER NOT NULL DEFAULT 0,
  balance_cost INTEGER NOT NULL DEFAULT 0,
  ledger_dedupe_key TEXT NOT NULL DEFAULT ''
, prompt_cost INTEGER NOT NULL DEFAULT 0, completion_cost INTEGER NOT NULL DEFAULT 0, cache_read_cost INTEGER NOT NULL DEFAULT 0, cache_creation_5m_cost INTEGER NOT NULL DEFAULT 0, cache_creation_1h_cost INTEGER NOT NULL DEFAULT 0, shadow_cost INTEGER NOT NULL DEFAULT 0, source_kind TEXT NOT NULL DEFAULT '', upstream_model_id TEXT NOT NULL DEFAULT '', cost_audit_status TEXT NOT NULL DEFAULT 'legacy', uncached_input_tokens INTEGER NOT NULL DEFAULT 0, reported_prompt_tokens INTEGER NOT NULL DEFAULT 0, reported_total_tokens INTEGER NOT NULL DEFAULT 0, billable_total_tokens INTEGER NOT NULL DEFAULT 0, usage_semantics TEXT NOT NULL DEFAULT '', usage_protocol TEXT NOT NULL DEFAULT '', usage_field_shape TEXT NOT NULL DEFAULT '', usage_parse_status TEXT NOT NULL DEFAULT 'legacy', usage_contract_version INTEGER NOT NULL DEFAULT 0, canonical_present INTEGER NOT NULL DEFAULT 0, usage_decision_reason TEXT NOT NULL DEFAULT '', subset_candidate_cost INTEGER NOT NULL DEFAULT 0, exclusive_candidate_cost INTEGER NOT NULL DEFAULT 0, pricing_config_hash TEXT NOT NULL DEFAULT '');

INSERT INTO "billing_ledgers_timestamp_v090" ("id", "user_id", "amount", "balance_after", "type", "reference_id", "remark", "created_at", "token_name", "model_name", "quota", "prompt_tokens", "completion_tokens", "cache_read_tokens", "cache_creation_5m_tokens", "cache_creation_1h_tokens", "channel_id", "subscription_account_id", "elapsed_time", "is_stream", "endpoint", "upstream_cost", "cost_source", "subscription_cost", "balance_cost", "ledger_dedupe_key", "prompt_cost", "completion_cost", "cache_read_cost", "cache_creation_5m_cost", "cache_creation_1h_cost", "shadow_cost", "source_kind", "upstream_model_id", "cost_audit_status", "uncached_input_tokens", "reported_prompt_tokens", "reported_total_tokens", "billable_total_tokens", "usage_semantics", "usage_protocol", "usage_field_shape", "usage_parse_status", "usage_contract_version", "canonical_present", "usage_decision_reason", "subset_candidate_cost", "exclusive_candidate_cost", "pricing_config_hash")
SELECT "id",
       "user_id",
       "amount",
       "balance_after",
       "type",
       "reference_id",
       "remark",
       COALESCE(CASE WHEN typeof("created_at") IN ('integer', 'real') THEN strftime('%Y-%m-%d %H:%M:%f', "created_at", 'unixepoch') ELSE "created_at" END, '1970-01-01 00:00:00.000'),
       "token_name",
       "model_name",
       "quota",
       "prompt_tokens",
       "completion_tokens",
       "cache_read_tokens",
       "cache_creation_5m_tokens",
       "cache_creation_1h_tokens",
       "channel_id",
       "subscription_account_id",
       "elapsed_time",
       "is_stream",
       "endpoint",
       "upstream_cost",
       "cost_source",
       "subscription_cost",
       "balance_cost",
       "ledger_dedupe_key",
       "prompt_cost",
       "completion_cost",
       "cache_read_cost",
       "cache_creation_5m_cost",
       "cache_creation_1h_cost",
       "shadow_cost",
       "source_kind",
       "upstream_model_id",
       "cost_audit_status",
       "uncached_input_tokens",
       "reported_prompt_tokens",
       "reported_total_tokens",
       "billable_total_tokens",
       "usage_semantics",
       "usage_protocol",
       "usage_field_shape",
       "usage_parse_status",
       "usage_contract_version",
       "canonical_present",
       "usage_decision_reason",
       "subset_candidate_cost",
       "exclusive_candidate_cost",
       "pricing_config_hash"
FROM "billing_ledgers";

DROP TABLE "billing_ledgers";

ALTER TABLE "billing_ledgers_timestamp_v090" RENAME TO "billing_ledgers";

CREATE INDEX idx_billing_ledgers_channel_created    ON billing_ledgers(channel_id, created_at);

CREATE INDEX idx_billing_ledgers_cost_source_created ON billing_ledgers(cost_source, created_at);

CREATE INDEX idx_billing_ledgers_created_at        ON billing_ledgers(created_at);

CREATE INDEX idx_billing_ledgers_model_created      ON billing_ledgers(model_name, created_at);

CREATE INDEX idx_billing_ledgers_reference_id      ON billing_ledgers(reference_id);

CREATE INDEX idx_billing_ledgers_subscription_account_created
  ON billing_ledgers(subscription_account_id, created_at);

CREATE INDEX idx_billing_ledgers_type              ON billing_ledgers(type);

CREATE INDEX idx_billing_ledgers_type_created       ON billing_ledgers(type, created_at);

CREATE INDEX idx_billing_ledgers_upstream_cost_created
  ON billing_ledgers(upstream_cost, created_at);

CREATE INDEX idx_billing_ledgers_user_created_model ON billing_ledgers(user_id, created_at, model_name);

CREATE INDEX idx_billing_ledgers_user_id           ON billing_ledgers(user_id);

CREATE INDEX idx_billing_ledgers_user_type_created
  ON billing_ledgers (user_id, type, created_at);

CREATE UNIQUE INDEX idx_ledger_dedupe_key ON billing_ledgers(ledger_dedupe_key);

CREATE TABLE billing_redeem_codes_timestamp_v090 (
  id INTEGER PRIMARY KEY AUTOINCREMENT,
  code TEXT NOT NULL UNIQUE,
  name TEXT DEFAULT NULL,
  amount INTEGER NOT NULL,
  count INTEGER NOT NULL,
  status INTEGER NOT NULL DEFAULT 1,
  created_by TEXT DEFAULT NULL,
  created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
  updated_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP
);

INSERT INTO "billing_redeem_codes_timestamp_v090" ("id", "code", "name", "amount", "count", "status", "created_by", "created_at", "updated_at")
SELECT "id",
       "code",
       "name",
       "amount",
       "count",
       "status",
       "created_by",
       COALESCE(CASE WHEN typeof("created_at") IN ('integer', 'real') THEN strftime('%Y-%m-%d %H:%M:%f', "created_at", 'unixepoch') ELSE "created_at" END, '1970-01-01 00:00:00.000'),
       COALESCE(CASE WHEN typeof("updated_at") IN ('integer', 'real') THEN strftime('%Y-%m-%d %H:%M:%f', "updated_at", 'unixepoch') ELSE "updated_at" END, '1970-01-01 00:00:00.000')
FROM "billing_redeem_codes";

DROP TABLE "billing_redeem_codes";

ALTER TABLE "billing_redeem_codes_timestamp_v090" RENAME TO "billing_redeem_codes";

CREATE INDEX idx_billing_redeem_codes_created_at        ON billing_redeem_codes(created_at);

CREATE INDEX idx_billing_redeem_codes_name              ON billing_redeem_codes(name);

CREATE INDEX idx_billing_redeem_codes_status            ON billing_redeem_codes(status);

CREATE INDEX idx_billing_redeem_codes_status_created_at ON billing_redeem_codes(status, created_at);

CREATE TABLE billing_redeem_records_timestamp_v090 (
  id INTEGER PRIMARY KEY AUTOINCREMENT,
  user_id TEXT NOT NULL,
  code TEXT NOT NULL,
  amount INTEGER NOT NULL,
  balance_before INTEGER NOT NULL,
  balance_after INTEGER NOT NULL,
  created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP
);

INSERT INTO "billing_redeem_records_timestamp_v090" ("id", "user_id", "code", "amount", "balance_before", "balance_after", "created_at")
SELECT "id",
       "user_id",
       "code",
       "amount",
       "balance_before",
       "balance_after",
       COALESCE(CASE WHEN typeof("created_at") IN ('integer', 'real') THEN strftime('%Y-%m-%d %H:%M:%f', "created_at", 'unixepoch') ELSE "created_at" END, '1970-01-01 00:00:00.000')
FROM "billing_redeem_records";

DROP TABLE "billing_redeem_records";

ALTER TABLE "billing_redeem_records_timestamp_v090" RENAME TO "billing_redeem_records";

CREATE INDEX idx_billing_redeem_records_code       ON billing_redeem_records(code);

CREATE INDEX idx_billing_redeem_records_created_at ON billing_redeem_records(created_at);

CREATE INDEX idx_billing_redeem_records_user_id    ON billing_redeem_records(user_id);

CREATE TABLE payment_orders_timestamp_v090 (
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
  paid_at DATETIME DEFAULT NULL,
  created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
  updated_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
  asset_issue_status TEXT NOT NULL DEFAULT 'pending',
  group_id INTEGER NOT NULL DEFAULT 0,
  plan_id INTEGER NOT NULL DEFAULT 0,
  plan_snapshot TEXT DEFAULT NULL,
  subscription_id INTEGER NOT NULL DEFAULT 0,
  refund_reason TEXT
);

INSERT INTO "payment_orders_timestamp_v090" ("id", "user_id", "trade_no", "channel", "asset_type", "asset_amount", "money_cents", "currency", "status", "provider_trade_no", "provider_payload", "pay_url", "paid_at", "created_at", "updated_at", "asset_issue_status", "group_id", "plan_id", "plan_snapshot", "subscription_id", "refund_reason")
SELECT "id",
       "user_id",
       "trade_no",
       "channel",
       "asset_type",
       "asset_amount",
       "money_cents",
       "currency",
       "status",
       "provider_trade_no",
       "provider_payload",
       "pay_url",
       CASE WHEN "paid_at" = 0 THEN NULL ELSE CASE WHEN typeof("paid_at") IN ('integer', 'real') THEN strftime('%Y-%m-%d %H:%M:%f', "paid_at", 'unixepoch') ELSE "paid_at" END END,
       COALESCE(CASE WHEN typeof("created_at") IN ('integer', 'real') THEN strftime('%Y-%m-%d %H:%M:%f', "created_at", 'unixepoch') ELSE "created_at" END, '1970-01-01 00:00:00.000'),
       COALESCE(CASE WHEN typeof("updated_at") IN ('integer', 'real') THEN strftime('%Y-%m-%d %H:%M:%f', "updated_at", 'unixepoch') ELSE "updated_at" END, '1970-01-01 00:00:00.000'),
       "asset_issue_status",
       "group_id",
       "plan_id",
       "plan_snapshot",
       "subscription_id",
       "refund_reason"
FROM "payment_orders";

DROP TABLE "payment_orders";

ALTER TABLE "payment_orders_timestamp_v090" RENAME TO "payment_orders";

CREATE INDEX idx_payment_orders_asset_issue_status    ON payment_orders(asset_issue_status);

CREATE INDEX idx_payment_orders_asset_type            ON payment_orders(asset_type);

CREATE INDEX idx_payment_orders_channel               ON payment_orders(channel);

CREATE INDEX idx_payment_orders_group_id              ON payment_orders(group_id);

CREATE INDEX idx_payment_orders_paid_at               ON payment_orders(paid_at);

CREATE INDEX idx_payment_orders_plan_id               ON payment_orders(plan_id);

CREATE INDEX idx_payment_orders_provider_trade_no     ON payment_orders(provider_trade_no);

CREATE INDEX idx_payment_orders_status                ON payment_orders(status);

CREATE INDEX idx_payment_orders_user_id                ON payment_orders(user_id);

CREATE TABLE account_receivables_timestamp_v090 (
  id INTEGER PRIMARY KEY AUTOINCREMENT,
  user_id TEXT NOT NULL,
  reservation_id TEXT NOT NULL,
  overdue_quota INTEGER NOT NULL,
  overdue_usd REAL NOT NULL,
  status TEXT NOT NULL DEFAULT 'pending',


  created_at DATETIME DEFAULT NULL,
  updated_at DATETIME DEFAULT NULL,
  settled_at DATETIME DEFAULT NULL,
  settled_quota INTEGER NOT NULL DEFAULT 0,
  remark TEXT DEFAULT NULL
);

INSERT INTO "account_receivables_timestamp_v090" ("id", "user_id", "reservation_id", "overdue_quota", "overdue_usd", "status", "created_at", "updated_at", "settled_at", "settled_quota", "remark")
SELECT "id",
       "user_id",
       "reservation_id",
       "overdue_quota",
       "overdue_usd",
       "status",
       CASE WHEN "created_at" = 0 THEN NULL ELSE CASE WHEN typeof("created_at") IN ('integer', 'real') THEN strftime('%Y-%m-%d %H:%M:%f', "created_at", 'unixepoch') ELSE "created_at" END END,
       CASE WHEN "updated_at" = 0 THEN NULL ELSE CASE WHEN typeof("updated_at") IN ('integer', 'real') THEN strftime('%Y-%m-%d %H:%M:%f', "updated_at", 'unixepoch') ELSE "updated_at" END END,
       CASE WHEN "settled_at" = 0 THEN NULL ELSE CASE WHEN typeof("settled_at") IN ('integer', 'real') THEN strftime('%Y-%m-%d %H:%M:%f', "settled_at", 'unixepoch') ELSE "settled_at" END END,
       "settled_quota",
       "remark"
FROM "account_receivables";

DROP TABLE "account_receivables";

ALTER TABLE "account_receivables_timestamp_v090" RENAME TO "account_receivables";

CREATE UNIQUE INDEX idx_account_receivable_reservation ON account_receivables(reservation_id);

CREATE INDEX idx_account_receivable_status_created ON account_receivables(status, created_at);

CREATE INDEX idx_account_receivable_user ON account_receivables(user_id, status);

UPDATE sqlite_sequence SET seq = MAX(seq, COALESCE(
  (SELECT seq FROM lite_timestamp_sequences WHERE name = sqlite_sequence.name), seq
)) WHERE name IN (SELECT name FROM lite_timestamp_sequences);
INSERT INTO sqlite_sequence (name, seq)
SELECT name, seq FROM lite_timestamp_sequences
WHERE name NOT IN (SELECT name FROM sqlite_sequence);
DROP TABLE lite_timestamp_sequences;

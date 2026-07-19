-- ────────────────────────────────────────────────────────────────────────────
-- Phase 2.4 — Per-service schema isolation.
--
-- This file is NOT applied automatically. It is a reference DDL that an
-- operator runs once against an existing MySQL instance to split the
-- shared `oneapi` database into one schema per backend service:
--
--   oneapi_identity, oneapi_channel, oneapi_billing, oneapi_log,
--   oneapi_admin, oneapi_config, oneapi_notify, oneapi_monitor
--
-- Each backend service is then pointed at its own schema via the
-- <SVC>_SCHEMA environment variable (see docs/deployment.md §10). Tables
-- are copied (CREATE TABLE ... LIKE + INSERT ... SELECT) so the source
-- database is left untouched — a rollback is simply re-pointing the env
-- var back at `oneapi`.
--
-- Run with:
--   mysql -h <host> -u root -p < migrations/schema_split.sql
--
-- Then run the per-service migrations to bring each schema up to date:
--   MIGRATIONS_DSN='root:pw@tcp(host:3306)/oneapi_identity' \
--     go run ./cmd/migrate -ownership identity
--   MIGRATIONS_DSN='root:pw@tcp(host:3306)/oneapi_billing' \
--     go run ./cmd/migrate -ownership billing
--   …
-- ────────────────────────────────────────────────────────────────────────────

CREATE DATABASE IF NOT EXISTS oneapi_identity   DEFAULT CHARACTER SET utf8mb4 COLLATE utf8mb4_unicode_ci;
CREATE DATABASE IF NOT EXISTS oneapi_channel    DEFAULT CHARACTER SET utf8mb4 COLLATE utf8mb4_unicode_ci;
CREATE DATABASE IF NOT EXISTS oneapi_billing    DEFAULT CHARACTER SET utf8mb4 COLLATE utf8mb4_unicode_ci;
CREATE DATABASE IF NOT EXISTS oneapi_log        DEFAULT CHARACTER SET utf8mb4 COLLATE utf8mb4_unicode_ci;
CREATE DATABASE IF NOT EXISTS oneapi_admin      DEFAULT CHARACTER SET utf8mb4 COLLATE utf8mb4_unicode_ci;
CREATE DATABASE IF NOT EXISTS oneapi_config     DEFAULT CHARACTER SET utf8mb4 COLLATE utf8mb4_unicode_ci;
CREATE DATABASE IF NOT EXISTS oneapi_notify     DEFAULT CHARACTER SET utf8mb4 COLLATE utf8mb4_unicode_ci;
CREATE DATABASE IF NOT EXISTS oneapi_monitor    DEFAULT CHARACTER SET utf8mb4 COLLATE utf8mb4_unicode_ci;

-- Create schema_migrations table in each schema first
CREATE TABLE IF NOT EXISTS oneapi_identity.schema_migrations (version varchar(255) NOT NULL PRIMARY KEY, applied_at bigint NOT NULL);
CREATE TABLE IF NOT EXISTS oneapi_channel.schema_migrations  (version varchar(255) NOT NULL PRIMARY KEY, applied_at bigint NOT NULL);
CREATE TABLE IF NOT EXISTS oneapi_billing.schema_migrations  (version varchar(255) NOT NULL PRIMARY KEY, applied_at bigint NOT NULL);
CREATE TABLE IF NOT EXISTS oneapi_log.schema_migrations       (version varchar(255) NOT NULL PRIMARY KEY, applied_at bigint NOT NULL);
CREATE TABLE IF NOT EXISTS oneapi_admin.schema_migrations     (version varchar(255) NOT NULL PRIMARY KEY, applied_at bigint NOT NULL);
CREATE TABLE IF NOT EXISTS oneapi_config.schema_migrations    (version varchar(255) NOT NULL PRIMARY KEY, applied_at bigint NOT NULL);
CREATE TABLE IF NOT EXISTS oneapi_notify.schema_migrations    (version varchar(255) NOT NULL PRIMARY KEY, applied_at bigint NOT NULL);
CREATE TABLE IF NOT EXISTS oneapi_monitor.schema_migrations   (version varchar(255) NOT NULL PRIMARY KEY, applied_at bigint NOT NULL);

-- identity schema: users, tokens, user_oauth_identities, user_aff_records
CREATE TABLE IF NOT EXISTS oneapi_identity.users                  LIKE oneapi.users;
CREATE TABLE IF NOT EXISTS oneapi_identity.tokens                 LIKE oneapi.tokens;
CREATE TABLE IF NOT EXISTS oneapi_identity.user_oauth_identities  LIKE oneapi.user_oauth_identities;
INSERT IGNORE INTO oneapi_identity.users                     SELECT * FROM oneapi.users;
INSERT IGNORE INTO oneapi_identity.tokens                    SELECT * FROM oneapi.tokens;
INSERT IGNORE INTO oneapi_identity.user_oauth_identities     SELECT * FROM oneapi.user_oauth_identities;

-- channel schema: channels, abilities, subscription_accounts,
--                 subscription_account_quota_events, subscription_account_quota_reset_runs
CREATE TABLE IF NOT EXISTS oneapi_channel.channels                                   LIKE oneapi.channels;
CREATE TABLE IF NOT EXISTS oneapi_channel.abilities                                  LIKE oneapi.abilities;
CREATE TABLE IF NOT EXISTS oneapi_channel.subscription_accounts                      LIKE oneapi.subscription_accounts;
CREATE TABLE IF NOT EXISTS oneapi_channel.subscription_account_quota_events          LIKE oneapi.subscription_account_quota_events;
CREATE TABLE IF NOT EXISTS oneapi_channel.subscription_account_quota_reset_runs      LIKE oneapi.subscription_account_quota_reset_runs;
INSERT IGNORE INTO oneapi_channel.channels                                  SELECT * FROM oneapi.channels;
INSERT IGNORE INTO oneapi_channel.abilities                                 SELECT * FROM oneapi.abilities;
INSERT IGNORE INTO oneapi_channel.subscription_accounts                     SELECT * FROM oneapi.subscription_accounts;
INSERT IGNORE INTO oneapi_channel.subscription_account_quota_events         SELECT * FROM oneapi.subscription_account_quota_events;
INSERT IGNORE INTO oneapi_channel.subscription_account_quota_reset_runs     SELECT * FROM oneapi.subscription_account_quota_reset_runs;

-- billing schema: billing_*, payment_orders, reconciliation_runs,
--                 account_receivables, user_subscriptions, subscription_groups,
--                 subscription_plans, account_quota_snapshots
CREATE TABLE IF NOT EXISTS oneapi_billing.billing_reservations                 LIKE oneapi.billing_reservations;
CREATE TABLE IF NOT EXISTS oneapi_billing.billing_ledgers                      LIKE oneapi.billing_ledgers;
CREATE TABLE IF NOT EXISTS oneapi_billing.billing_redeem_codes                 LIKE oneapi.billing_redeem_codes;
CREATE TABLE IF NOT EXISTS oneapi_billing.billing_redeem_records               LIKE oneapi.billing_redeem_records;
CREATE TABLE IF NOT EXISTS oneapi_billing.payment_orders                       LIKE oneapi.payment_orders;
CREATE TABLE IF NOT EXISTS oneapi_billing.reconciliation_runs                  LIKE oneapi.reconciliation_runs;
CREATE TABLE IF NOT EXISTS oneapi_billing.account_receivables                  LIKE oneapi.account_receivables;
CREATE TABLE IF NOT EXISTS oneapi_billing.user_subscriptions                   LIKE oneapi.user_subscriptions;
CREATE TABLE IF NOT EXISTS oneapi_billing.subscription_groups                  LIKE oneapi.subscription_groups;
CREATE TABLE IF NOT EXISTS oneapi_billing.subscription_plans                   LIKE oneapi.subscription_plans;
CREATE TABLE IF NOT EXISTS oneapi_billing.account_quota_snapshots              LIKE oneapi.account_quota_snapshots;
INSERT IGNORE INTO oneapi_billing.billing_reservations        SELECT * FROM oneapi.billing_reservations;
INSERT IGNORE INTO oneapi_billing.billing_ledgers             SELECT * FROM oneapi.billing_ledgers;
INSERT IGNORE INTO oneapi_billing.billing_redeem_codes        SELECT * FROM oneapi.billing_redeem_codes;
INSERT IGNORE INTO oneapi_billing.billing_redeem_records      SELECT * FROM oneapi.billing_redeem_records;
INSERT IGNORE INTO oneapi_billing.payment_orders              SELECT * FROM oneapi.payment_orders;
INSERT IGNORE INTO oneapi_billing.reconciliation_runs         SELECT * FROM oneapi.reconciliation_runs;
INSERT IGNORE INTO oneapi_billing.account_receivables         SELECT * FROM oneapi.account_receivables;
-- user_subscriptions has a generated column (active_user_id), so we must
-- explicitly list only the non-generated columns.
INSERT IGNORE INTO oneapi_billing.user_subscriptions
  (id, user_id, group_id, subscription_name, status, starts_at, expires_at,
   daily_usage_usd, weekly_usage_usd, monthly_usage_usd,
   daily_window_start, weekly_window_start, monthly_window_start,
   metadata, created_at, updated_at)
SELECT id, user_id, group_id, subscription_name, status, starts_at, expires_at,
       daily_usage_usd, weekly_usage_usd, monthly_usage_usd,
       daily_window_start, weekly_window_start, monthly_window_start,
       metadata, created_at, updated_at
FROM oneapi.user_subscriptions;
INSERT IGNORE INTO oneapi_billing.subscription_groups         SELECT * FROM oneapi.subscription_groups;
INSERT IGNORE INTO oneapi_billing.subscription_plans          SELECT * FROM oneapi.subscription_plans;
INSERT IGNORE INTO oneapi_billing.account_quota_snapshots     SELECT * FROM oneapi.account_quota_snapshots;

-- log schema: logs
CREATE TABLE IF NOT EXISTS oneapi_log.logs LIKE oneapi.logs;
-- logs is typically high-volume; copy only recent rows to keep the cutover fast.
INSERT IGNORE INTO oneapi_log.logs SELECT * FROM oneapi.logs WHERE created_at >= UNIX_TIMESTAMP() - 7*24*3600;

-- admin schema: system_options
CREATE TABLE IF NOT EXISTS oneapi_admin.system_options LIKE oneapi.system_options;
INSERT IGNORE INTO oneapi_admin.system_options SELECT * FROM oneapi.system_options;

-- config schema: configs
CREATE TABLE IF NOT EXISTS oneapi_config.configs LIKE oneapi.configs;
INSERT IGNORE INTO oneapi_config.configs SELECT * FROM oneapi.configs;

-- notify schema: notifications
CREATE TABLE IF NOT EXISTS oneapi_notify.notifications LIKE oneapi.notifications;
INSERT IGNORE INTO oneapi_notify.notifications SELECT * FROM oneapi.notifications;

-- monitor schema: health_checks, alert_rules
CREATE TABLE IF NOT EXISTS oneapi_monitor.health_checks LIKE oneapi.health_checks;
CREATE TABLE IF NOT EXISTS oneapi_monitor.alert_rules   LIKE oneapi.alert_rules;
INSERT IGNORE INTO oneapi_monitor.health_checks SELECT * FROM oneapi.health_checks;
INSERT IGNORE INTO oneapi_monitor.alert_rules   SELECT * FROM oneapi.alert_rules;

-- Cross-schema views for billing reconciliation.
--
-- billing-service's reconciliation logic (reconciliation_repo.go) reads from
-- channels, logs, and users tables. Since billing runs on its own schema
-- (oneapi_billing) after the split, we create views pointing at the source
-- tables in their respective schemas. This is the transitional path documented
-- in docs/deployment.md §10.4 (Option A).
--
-- Note: user_subscriptions has been copied to oneapi_billing, no view needed.

-- Channels view (oneapi_billing.channels → oneapi_channel.channels)
CREATE OR REPLACE VIEW oneapi_billing.channels AS SELECT * FROM oneapi_channel.channels;

-- Logs view (oneapi_billing.logs → oneapi_log.logs)
CREATE OR REPLACE VIEW oneapi_billing.logs AS SELECT * FROM oneapi_log.logs;

-- Users view (oneapi_billing.users → oneapi_identity.users)
-- Used by SumOverdraftBalances for wallet overdraft reconciliation.
CREATE OR REPLACE VIEW oneapi_billing.users AS SELECT * FROM oneapi_identity.users;

-- Populate schema_migrations for each service to mark their owned migrations as applied.
-- Since tables were copied with LIKE, they include all columns from migrations,
-- so we record those migrations as already applied to avoid re-running them.
-- Note: applied_at uses DEFAULT CURRENT_TIMESTAMP

-- identity schema migrations
INSERT IGNORE INTO oneapi_identity.schema_migrations (version) VALUES
  ('000_create_core_tables'),
  ('007_create_system_options'),
  ('014_add_token_management_fields'),
  ('015_add_user_aff_fields'),
  ('016_add_usage_log_fields'),
  ('017_add_token_oneapi_fields'),
  ('019_create_user_oauth_identities'),
  ('022_create_schema_migrations'),
  ('025_add_user_role'),
  ('026_fix_users_status_comment'),
  ('027_remove_placeholder_seed_data');

-- channel schema migrations
INSERT IGNORE INTO oneapi_channel.schema_migrations (version) VALUES
  ('000_create_core_tables'),
  ('018_add_channel_oneapi_fields'),
  ('020_add_channel_balance_refresh_tracking'),
  ('032_add_channel_health_fields'),
  ('034_create_subscription_accounts'),
  ('035_add_subscription_account_quota_fields'),
  ('051_add_subscription_account_local_quota'),
  ('052_create_subscription_account_quota_events'),
  ('053_add_subscription_account_5h_quota'),
  ('054_add_subscription_account_rpm_limit'),
  ('055_add_subscription_account_session_window_limit'),
  ('056_add_subscription_account_quota_reset_config'),
  ('057_create_subscription_account_quota_reset_runs'),
  ('022_create_schema_migrations');

-- billing schema migrations
INSERT IGNORE INTO oneapi_billing.schema_migrations (version) VALUES
  ('000_create_core_tables'),
  ('008_create_billing_reservations'),
  ('009_create_billing_ledgers'),
  ('010_create_billing_redeem_codes'),
  ('011_create_billing_redeem_records'),
  ('013_add_redeem_code_indexes'),
  ('021_create_reconciliation_runs'),
  ('022_create_schema_migrations'),
  ('023_create_payment_orders'),
  ('028_add_billing_ledger_aggregation_indexes'),
  ('029_add_billing_ledger_upstream_cost'),
  ('030_add_reconciliation_run_phase3_fields'),
  ('036_add_subscription_account_id_to_billing_ledgers'),
  ('037_add_subscription_account_id_to_logs'),
  ('038_add_subscription_account_id_to_billing_reservations'),
  ('039_create_user_subscriptions'),
  ('040_create_subscription_groups'),
  ('041_create_account_quota_snapshots'),
  ('042_add_subscription_group_pricing'),
  ('043_add_group_id_to_payment_orders'),
  ('044_add_reservation_subscription_fields'),
  ('045_add_ledger_cost_source_and_dedupe_key'),
  ('046_create_account_receivables'),
  ('047_backfill_empty_ledger_dedupe_keys'),
  ('048_increase_subscription_usage_precision'),
  ('049_backfill_subscription_usage_from_ledgers'),
  ('050_create_subscription_plans'),
  ('057_add_plan_snapshot_to_payment_orders'),
  ('058_add_subscription_id_to_payment_orders'),
  ('059_enforce_single_active_subscription');

-- log schema migrations
INSERT IGNORE INTO oneapi_log.schema_migrations (version) VALUES
  ('000_create_core_tables'),
  ('022_create_schema_migrations'),
  ('031_add_cache_read_token_usage_fields'),
  ('037_add_subscription_account_id_to_logs');

-- admin schema migrations
INSERT IGNORE INTO oneapi_admin.schema_migrations (version) VALUES
  ('007_create_system_options'),
  ('022_create_schema_migrations');

-- config schema migrations
INSERT IGNORE INTO oneapi_config.schema_migrations (version) VALUES
  ('000_create_core_tables'),
  ('022_create_schema_migrations');

-- notify schema migrations
INSERT IGNORE INTO oneapi_notify.schema_migrations (version) VALUES
  ('000_create_core_tables'),
  ('022_create_schema_migrations'),
  ('033_add_notification_last_error');

-- monitor schema migrations
INSERT IGNORE INTO oneapi_monitor.schema_migrations (version) VALUES
  ('000_create_core_tables'),
  ('022_create_schema_migrations');

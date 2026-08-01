-- v0.11.0 Phase 1 §1.2: cache_creation token usage fields (ADR §2).
-- Additive only: two new BIGINT columns defaulting to 0 on logs and
-- billing_ledgers. Rollback = keep the columns (no destructive down migration).
-- Mirrors 031_add_cache_read_token_usage_fields.sql for the 5m/1h TTL split.

-- Each table is only owned by some schemas: `logs` lives on the legacy shared
-- DB and log-service, `billing_ledgers` on the legacy shared DB and
-- billing-service. Per-service schemas that do not own a table get it as a
-- cross-schema view (or not at all), so the ALTER must be a no-op there
-- instead of failing the migration. Prepared statements let the guard be
-- evaluated at run time.
SET @moa_has_logs := (
  SELECT COUNT(*) FROM information_schema.TABLES
  WHERE TABLE_SCHEMA = DATABASE() AND TABLE_NAME = 'logs'
);
SET @moa_logs_has_col := (
  SELECT COUNT(*) FROM information_schema.COLUMNS
  WHERE TABLE_SCHEMA = DATABASE() AND TABLE_NAME = 'logs'
    AND COLUMN_NAME = 'cache_creation_5m_tokens'
);
SET @moa_067_logs_sql := IF(
  @moa_has_logs > 0 AND @moa_logs_has_col = 0,
  'ALTER TABLE `logs`
    ADD COLUMN `cache_creation_5m_tokens` bigint DEFAULT 0 AFTER `cache_read_tokens`,
    ADD COLUMN `cache_creation_1h_tokens` bigint DEFAULT 0 AFTER `cache_creation_5m_tokens`',
  'SELECT 1'
);
PREPARE moa_067_logs_stmt FROM @moa_067_logs_sql;
EXECUTE moa_067_logs_stmt;
DEALLOCATE PREPARE moa_067_logs_stmt;

SET @moa_has_ledgers := (
  SELECT COUNT(*) FROM information_schema.TABLES
  WHERE TABLE_SCHEMA = DATABASE() AND TABLE_NAME = 'billing_ledgers'
);
SET @moa_ledgers_has_col := (
  SELECT COUNT(*) FROM information_schema.COLUMNS
  WHERE TABLE_SCHEMA = DATABASE() AND TABLE_NAME = 'billing_ledgers'
    AND COLUMN_NAME = 'cache_creation_5m_tokens'
);
SET @moa_067_ledgers_sql := IF(
  @moa_has_ledgers > 0 AND @moa_ledgers_has_col = 0,
  'ALTER TABLE `billing_ledgers`
    ADD COLUMN `cache_creation_5m_tokens` bigint DEFAULT 0 AFTER `cache_read_tokens`,
    ADD COLUMN `cache_creation_1h_tokens` bigint DEFAULT 0 AFTER `cache_creation_5m_tokens`',
  'SELECT 1'
);
PREPARE moa_067_ledgers_stmt FROM @moa_067_ledgers_sql;
EXECUTE moa_067_ledgers_stmt;
DEALLOCATE PREPARE moa_067_ledgers_stmt;

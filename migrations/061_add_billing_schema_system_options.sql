-- ────────────────────────────────────────────────────────────────────────────
-- Fix for Phase 2.4 schema isolation: billing service needs access to
-- system_options table for pricing configuration (ModelPrice, ModelRatio, etc.)
--
-- Problem: billing service connects to oneapi_billing schema, but system_options
-- was only copied to oneapi_admin schema in the original schema_split.sql
--
-- Solution: Create system_options table in oneapi_billing with a view pointing
-- to the canonical source in oneapi_admin, ensuring:
-- 1. Single source of truth for pricing config (oneapi_admin.system_options)
-- 2. Billing service can access pricing config via view
-- 3. No data duplication issues
-- ────────────────────────────────────────────────────────────────────────────

-- The view only makes sense in schema-isolated (split) mode, where the
-- canonical system_options lives in oneapi_admin. In the legacy single-DB
-- layout (or a clean-room provisioning run) oneapi_admin/oneapi_billing do
-- not exist, so the DDL must be a no-op instead of failing the migration.
-- We use a prepared statement so the guard is evaluated at run time.

-- Drop view if it exists from previous manual fixes (IF EXISTS keeps this a
-- warning-only no-op when the schema does not exist).
DROP VIEW IF EXISTS oneapi_billing.system_options;

SET @moa_has_admin_options := (
  SELECT COUNT(*) FROM information_schema.TABLES
  WHERE TABLE_SCHEMA = 'oneapi_admin' AND TABLE_NAME = 'system_options'
);
SET @moa_has_billing_schema := (
  SELECT COUNT(*) FROM information_schema.SCHEMATA
  WHERE SCHEMA_NAME = 'oneapi_billing'
);
SET @moa_061_sql := IF(
  @moa_has_admin_options > 0 AND @moa_has_billing_schema > 0,
  'CREATE OR REPLACE VIEW oneapi_billing.system_options AS SELECT * FROM oneapi_admin.system_options',
  'SELECT 1'
);
PREPARE moa_061_stmt FROM @moa_061_sql;
EXECUTE moa_061_stmt;
DEALLOCATE PREPARE moa_061_stmt;

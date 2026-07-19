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

-- Drop view if it exists from previous manual fixes
DROP VIEW IF EXISTS oneapi_billing.system_options;

-- Create view pointing to the canonical system_options in admin schema
CREATE VIEW oneapi_billing.system_options AS
SELECT * FROM oneapi_admin.system_options;

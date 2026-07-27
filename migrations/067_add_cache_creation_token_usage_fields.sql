-- v0.11.0 Phase 1 §1.2: cache_creation token usage fields (ADR §2).
-- Additive only: two new BIGINT columns defaulting to 0 on logs and
-- billing_ledgers. Rollback = keep the columns (no destructive down migration).
-- Mirrors 031_add_cache_read_token_usage_fields.sql for the 5m/1h TTL split.

ALTER TABLE `logs`
  ADD COLUMN `cache_creation_5m_tokens` bigint DEFAULT 0 AFTER `cache_read_tokens`,
  ADD COLUMN `cache_creation_1h_tokens` bigint DEFAULT 0 AFTER `cache_creation_5m_tokens`;

ALTER TABLE `billing_ledgers`
  ADD COLUMN `cache_creation_5m_tokens` bigint DEFAULT 0 AFTER `cache_read_tokens`,
  ADD COLUMN `cache_creation_1h_tokens` bigint DEFAULT 0 AFTER `cache_creation_5m_tokens`;

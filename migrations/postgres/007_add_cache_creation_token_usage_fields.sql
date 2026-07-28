-- v0.11.0 Phase 1 §1.2: cache_creation token usage fields (ADR §2).
-- Mirrors MySQL migration 067_add_cache_creation_token_usage_fields.sql.
-- Additive only: two new BIGINT columns defaulting to 0 on logs and
-- billing_ledgers. IF NOT EXISTS makes the migration idempotent.

ALTER TABLE logs
  ADD COLUMN IF NOT EXISTS cache_creation_5m_tokens BIGINT NOT NULL DEFAULT 0,
  ADD COLUMN IF NOT EXISTS cache_creation_1h_tokens BIGINT NOT NULL DEFAULT 0;

ALTER TABLE billing_ledgers
  ADD COLUMN IF NOT EXISTS cache_creation_5m_tokens BIGINT NOT NULL DEFAULT 0,
  ADD COLUMN IF NOT EXISTS cache_creation_1h_tokens BIGINT NOT NULL DEFAULT 0;

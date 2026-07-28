-- v0.11.0 Phase 1 §1.2: cache_creation token usage fields (ADR §2).
-- Mirrors MySQL migration 067_add_cache_creation_token_usage_fields.sql.
-- Additive only: two new INTEGER columns defaulting to 0 on logs and
-- billing_ledgers. SQLite ALTER TABLE ADD COLUMN is idempotent-safe when
-- wrapped in a try/catch by the migration runner; if the column already
-- exists the runner ignores the "duplicate column" error.

ALTER TABLE logs ADD COLUMN cache_creation_5m_tokens INTEGER NOT NULL DEFAULT 0;
ALTER TABLE logs ADD COLUMN cache_creation_1h_tokens INTEGER NOT NULL DEFAULT 0;

ALTER TABLE billing_ledgers ADD COLUMN cache_creation_5m_tokens INTEGER NOT NULL DEFAULT 0;
ALTER TABLE billing_ledgers ADD COLUMN cache_creation_1h_tokens INTEGER NOT NULL DEFAULT 0;

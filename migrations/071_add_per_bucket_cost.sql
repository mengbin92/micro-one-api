-- v0.11.0 Phase 2 sub2api #9: per-bucket costs for audit and vendor-invoice reconciliation.
ALTER TABLE billing_ledgers
  ADD COLUMN prompt_cost BIGINT NOT NULL DEFAULT 0 AFTER cache_creation_1h_tokens,
  ADD COLUMN completion_cost BIGINT NOT NULL DEFAULT 0 AFTER prompt_cost,
  ADD COLUMN cache_read_cost BIGINT NOT NULL DEFAULT 0 AFTER completion_cost,
  ADD COLUMN cache_creation_5m_cost BIGINT NOT NULL DEFAULT 0 AFTER cache_read_cost,
  ADD COLUMN cache_creation_1h_cost BIGINT NOT NULL DEFAULT 0 AFTER cache_creation_5m_cost,
  ADD COLUMN shadow_cost BIGINT NOT NULL DEFAULT 0 AFTER cache_creation_1h_cost;

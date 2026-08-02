-- v0.11.0 Phase 2 sub2api #9: per-bucket costs for audit and vendor-invoice reconciliation.
-- SQLite only allows one ADD COLUMN per ALTER TABLE statement, so the
-- MySQL-style multi-column ALTER is split into individual statements.
ALTER TABLE billing_ledgers ADD COLUMN prompt_cost INTEGER NOT NULL DEFAULT 0;
ALTER TABLE billing_ledgers ADD COLUMN completion_cost INTEGER NOT NULL DEFAULT 0;
ALTER TABLE billing_ledgers ADD COLUMN cache_read_cost INTEGER NOT NULL DEFAULT 0;
ALTER TABLE billing_ledgers ADD COLUMN cache_creation_5m_cost INTEGER NOT NULL DEFAULT 0;
ALTER TABLE billing_ledgers ADD COLUMN cache_creation_1h_cost INTEGER NOT NULL DEFAULT 0;
ALTER TABLE billing_ledgers ADD COLUMN shadow_cost INTEGER NOT NULL DEFAULT 0;

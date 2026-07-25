-- Add per-account model mapping for subscription accounts (SQLite).
-- Mirrors the MySQL migration 063. See docs/model-management-design.md §10.1.
ALTER TABLE subscription_accounts
  ADD COLUMN model_mapping TEXT NOT NULL DEFAULT '';

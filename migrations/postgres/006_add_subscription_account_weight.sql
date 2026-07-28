-- v0.11.0 Phase 3 §3.1: explicit within-tier weight for subscription accounts.
-- See migrations/069_add_subscription_account_weight.sql for the full rationale.
ALTER TABLE subscription_accounts
  ADD COLUMN IF NOT EXISTS weight INTEGER NOT NULL DEFAULT 0;

-- See migrations/088_create_billing_pricing_snapshots.sql.
-- Pricing snapshot table + ledger hash reference; claimed in the same
-- transaction as the ledger insert, deduped by config_hash.

CREATE TABLE IF NOT EXISTS billing_pricing_snapshots (
  id bigserial PRIMARY KEY,
  config_hash varchar(64) NOT NULL,
  model_name varchar(191) NOT NULL,
  input_price numeric(32,17) NOT NULL DEFAULT 0,
  output_price numeric(32,17) NOT NULL DEFAULT 0,
  cache_read_price numeric(32,17) NOT NULL DEFAULT 0,
  cache_creation_5m_price numeric(32,17) NOT NULL DEFAULT 0,
  cache_creation_1h_price numeric(32,17) NOT NULL DEFAULT 0,
  group_ratio numeric(32,17) NOT NULL DEFAULT 1,
  cache_creation_mode varchar(16) NOT NULL DEFAULT 'observe',
  snapshot_version integer NOT NULL DEFAULT 1,
  created_at timestamp(3) NOT NULL DEFAULT CURRENT_TIMESTAMP
);

CREATE UNIQUE INDEX IF NOT EXISTS uk_billing_pricing_snapshots_hash
  ON billing_pricing_snapshots (config_hash);

ALTER TABLE billing_ledgers
  ADD COLUMN IF NOT EXISTS pricing_config_hash varchar(64) NOT NULL DEFAULT '';

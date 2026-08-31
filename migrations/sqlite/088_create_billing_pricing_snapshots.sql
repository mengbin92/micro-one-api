-- See migrations/088_create_billing_pricing_snapshots.sql.
-- Pricing snapshot table + ledger hash reference; claimed in the same
-- transaction as the ledger insert, deduped by config_hash. REAL columns
-- round-trip float64 prices exactly.

CREATE TABLE IF NOT EXISTS billing_pricing_snapshots (
  id INTEGER PRIMARY KEY AUTOINCREMENT,
  config_hash TEXT NOT NULL,
  model_name TEXT NOT NULL,
  input_price REAL NOT NULL DEFAULT 0,
  output_price REAL NOT NULL DEFAULT 0,
  cache_read_price REAL NOT NULL DEFAULT 0,
  cache_creation_5m_price REAL NOT NULL DEFAULT 0,
  cache_creation_1h_price REAL NOT NULL DEFAULT 0,
  group_ratio REAL NOT NULL DEFAULT 1,
  cache_creation_mode TEXT NOT NULL DEFAULT 'observe',
  snapshot_version INTEGER NOT NULL DEFAULT 1,
  created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP
);

CREATE UNIQUE INDEX IF NOT EXISTS uk_billing_pricing_snapshots_hash
  ON billing_pricing_snapshots (config_hash);

ALTER TABLE billing_ledgers ADD COLUMN pricing_config_hash TEXT NOT NULL DEFAULT '';

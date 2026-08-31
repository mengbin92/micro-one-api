-- See migrations/087_create_usage_semantic_source_blocks.sql.
-- Usage-semantics quarantine keyed by execution source + upstream model +
-- adapter protocol (control-plane signal, separate from transport health).

CREATE TABLE IF NOT EXISTS usage_semantic_source_blocks (
  id INTEGER PRIMARY KEY AUTOINCREMENT,
  source_kind TEXT NOT NULL,
  source_id INTEGER NOT NULL,
  upstream_model_id TEXT NOT NULL,
  adapter_protocol TEXT NOT NULL DEFAULT '',
  status TEXT NOT NULL DEFAULT 'active',
  reason TEXT NOT NULL DEFAULT '',
  window_started_at DATETIME,
  consecutive_ambiguous INTEGER NOT NULL DEFAULT 0,
  blocked_until DATETIME,
  last_verified_at DATETIME,
  created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
  updated_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP
);

CREATE UNIQUE INDEX IF NOT EXISTS uk_usage_semantic_block_key
  ON usage_semantic_source_blocks (source_kind, source_id, upstream_model_id, adapter_protocol);

CREATE INDEX IF NOT EXISTS idx_usage_semantic_block_status_until
  ON usage_semantic_source_blocks (status, blocked_until);

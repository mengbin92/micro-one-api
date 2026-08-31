-- See migrations/087_create_usage_semantic_source_blocks.sql.
-- Usage-semantics quarantine keyed by execution source + upstream model +
-- adapter protocol (control-plane signal, separate from transport health).

CREATE TABLE IF NOT EXISTS usage_semantic_source_blocks (
  id bigserial PRIMARY KEY,
  source_kind varchar(32) NOT NULL,
  source_id bigint NOT NULL,
  upstream_model_id varchar(191) NOT NULL,
  adapter_protocol varchar(32) NOT NULL DEFAULT '',
  status varchar(16) NOT NULL DEFAULT 'active',
  reason varchar(64) NOT NULL DEFAULT '',
  window_started_at timestamp(3),
  consecutive_ambiguous integer NOT NULL DEFAULT 0,
  blocked_until timestamp(3),
  last_verified_at timestamp(3),
  created_at timestamp(3) NOT NULL DEFAULT CURRENT_TIMESTAMP,
  updated_at timestamp(3) NOT NULL DEFAULT CURRENT_TIMESTAMP
);

CREATE UNIQUE INDEX IF NOT EXISTS uk_usage_semantic_block_key
  ON usage_semantic_source_blocks (source_kind, source_id, upstream_model_id, adapter_protocol);

CREATE INDEX IF NOT EXISTS idx_usage_semantic_block_status_until
  ON usage_semantic_source_blocks (status, blocked_until);

-- See migrations/086_add_log_usage_semantics.sql.
-- logs gains the reported/canonical usage split and parse verdict (no
-- candidate costs / pricing hash). Existing rows keep usage_semantics='' /
-- usage_parse_status='legacy'; no semantics backfill from token arithmetic.

ALTER TABLE logs
  ADD COLUMN IF NOT EXISTS uncached_input_tokens bigint NOT NULL DEFAULT 0,
  ADD COLUMN IF NOT EXISTS reported_prompt_tokens bigint NOT NULL DEFAULT 0,
  ADD COLUMN IF NOT EXISTS reported_total_tokens bigint NOT NULL DEFAULT 0,
  ADD COLUMN IF NOT EXISTS billable_total_tokens bigint NOT NULL DEFAULT 0,
  ADD COLUMN IF NOT EXISTS usage_semantics varchar(32) NOT NULL DEFAULT '',
  ADD COLUMN IF NOT EXISTS usage_protocol varchar(32) NOT NULL DEFAULT '',
  ADD COLUMN IF NOT EXISTS usage_field_shape varchar(64) NOT NULL DEFAULT '',
  ADD COLUMN IF NOT EXISTS usage_parse_status varchar(16) NOT NULL DEFAULT 'legacy',
  ADD COLUMN IF NOT EXISTS usage_contract_version integer NOT NULL DEFAULT 0,
  ADD COLUMN IF NOT EXISTS canonical_present boolean NOT NULL DEFAULT FALSE,
  ADD COLUMN IF NOT EXISTS usage_decision_reason varchar(64) NOT NULL DEFAULT '';

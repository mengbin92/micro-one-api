-- See migrations/086_add_log_usage_semantics.sql.
-- The migrator treats duplicate-column errors as no-ops on replay.
-- logs gains the reported/canonical usage split and parse verdict; existing
-- rows keep usage_semantics='' / usage_parse_status='legacy'.

ALTER TABLE logs ADD COLUMN uncached_input_tokens INTEGER NOT NULL DEFAULT 0;
ALTER TABLE logs ADD COLUMN reported_prompt_tokens INTEGER NOT NULL DEFAULT 0;
ALTER TABLE logs ADD COLUMN reported_total_tokens INTEGER NOT NULL DEFAULT 0;
ALTER TABLE logs ADD COLUMN billable_total_tokens INTEGER NOT NULL DEFAULT 0;
ALTER TABLE logs ADD COLUMN usage_semantics TEXT NOT NULL DEFAULT '';
ALTER TABLE logs ADD COLUMN usage_protocol TEXT NOT NULL DEFAULT '';
ALTER TABLE logs ADD COLUMN usage_field_shape TEXT NOT NULL DEFAULT '';
ALTER TABLE logs ADD COLUMN usage_parse_status TEXT NOT NULL DEFAULT 'legacy';
ALTER TABLE logs ADD COLUMN usage_contract_version INTEGER NOT NULL DEFAULT 0;
ALTER TABLE logs ADD COLUMN canonical_present INTEGER NOT NULL DEFAULT 0;
ALTER TABLE logs ADD COLUMN usage_decision_reason TEXT NOT NULL DEFAULT '';

-- See migrations/085_add_billing_usage_semantics.sql.
-- The migrator treats duplicate-column errors as no-ops on replay.
-- Reported vs canonical usage separation + parse verdict on billing_ledgers;
-- existing rows keep usage_semantics='' / usage_parse_status='legacy'.

ALTER TABLE billing_ledgers ADD COLUMN uncached_input_tokens INTEGER NOT NULL DEFAULT 0;
ALTER TABLE billing_ledgers ADD COLUMN reported_prompt_tokens INTEGER NOT NULL DEFAULT 0;
ALTER TABLE billing_ledgers ADD COLUMN reported_total_tokens INTEGER NOT NULL DEFAULT 0;
ALTER TABLE billing_ledgers ADD COLUMN billable_total_tokens INTEGER NOT NULL DEFAULT 0;
ALTER TABLE billing_ledgers ADD COLUMN usage_semantics TEXT NOT NULL DEFAULT '';
ALTER TABLE billing_ledgers ADD COLUMN usage_protocol TEXT NOT NULL DEFAULT '';
ALTER TABLE billing_ledgers ADD COLUMN usage_field_shape TEXT NOT NULL DEFAULT '';
ALTER TABLE billing_ledgers ADD COLUMN usage_parse_status TEXT NOT NULL DEFAULT 'legacy';
ALTER TABLE billing_ledgers ADD COLUMN usage_contract_version INTEGER NOT NULL DEFAULT 0;
ALTER TABLE billing_ledgers ADD COLUMN canonical_present INTEGER NOT NULL DEFAULT 0;
ALTER TABLE billing_ledgers ADD COLUMN usage_decision_reason TEXT NOT NULL DEFAULT '';
ALTER TABLE billing_ledgers ADD COLUMN subset_candidate_cost INTEGER NOT NULL DEFAULT 0;
ALTER TABLE billing_ledgers ADD COLUMN exclusive_candidate_cost INTEGER NOT NULL DEFAULT 0;

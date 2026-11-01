-- B1 immutable long-context evidence. Historical charges are unchanged.
ALTER TABLE billing_ledgers ADD COLUMN long_context_policy VARCHAR(64) NOT NULL DEFAULT '';
ALTER TABLE billing_ledgers ADD COLUMN long_context_threshold BIGINT NOT NULL DEFAULT 0;
ALTER TABLE billing_ledgers ADD COLUMN long_context_tier VARCHAR(16) NOT NULL DEFAULT '';
ALTER TABLE billing_ledgers ADD COLUMN long_context_input_tokens BIGINT NOT NULL DEFAULT 0;
ALTER TABLE billing_pricing_snapshots ADD COLUMN long_context_policy VARCHAR(64) NOT NULL DEFAULT '';
ALTER TABLE billing_pricing_snapshots ADD COLUMN long_context_threshold BIGINT NOT NULL DEFAULT 0;
ALTER TABLE billing_pricing_snapshots ADD COLUMN long_context_tier VARCHAR(16) NOT NULL DEFAULT '';
ALTER TABLE billing_ledgers ADD COLUMN pricing_time_band VARCHAR(64) NOT NULL DEFAULT '';
ALTER TABLE billing_ledgers ADD COLUMN pricing_timezone VARCHAR(64) NOT NULL DEFAULT '';
ALTER TABLE billing_ledgers ADD COLUMN pricing_time_unix BIGINT NOT NULL DEFAULT 0;
ALTER TABLE billing_pricing_snapshots ADD COLUMN pricing_time_band VARCHAR(64) NOT NULL DEFAULT '';
ALTER TABLE billing_pricing_snapshots ADD COLUMN pricing_timezone VARCHAR(64) NOT NULL DEFAULT '';

-- A3 additive evidence only. Historical amounts and old v1 snapshots remain unchanged.
ALTER TABLE billing_ledgers ADD COLUMN requested_service_tier VARCHAR(32) NOT NULL DEFAULT '';
ALTER TABLE billing_ledgers ADD COLUMN actual_service_tier VARCHAR(64) NOT NULL DEFAULT '';
ALTER TABLE billing_ledgers ADD COLUMN service_tier_status VARCHAR(16) NOT NULL DEFAULT '';
ALTER TABLE billing_ledgers ADD COLUMN billed_service_tier VARCHAR(16) NOT NULL DEFAULT '';
ALTER TABLE billing_ledgers ADD COLUMN service_tier_policy VARCHAR(64) NOT NULL DEFAULT '';
ALTER TABLE billing_ledgers ADD COLUMN service_tier_config_hash VARCHAR(64) NOT NULL DEFAULT '';
ALTER TABLE billing_pricing_snapshots ADD COLUMN service_tier VARCHAR(16) NOT NULL DEFAULT '';
ALTER TABLE billing_pricing_snapshots ADD COLUMN service_tier_policy VARCHAR(64) NOT NULL DEFAULT '';
ALTER TABLE billing_pricing_snapshots ADD COLUMN service_tier_config_hash VARCHAR(64) NOT NULL DEFAULT '';

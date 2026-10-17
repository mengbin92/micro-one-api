-- Telemetry envelope columns do not participate in financial payload digests.
ALTER TABLE billing_settlement_tasks ADD COLUMN observation_id VARCHAR(36) NULL;
ALTER TABLE billing_settlement_tasks ADD COLUMN trace_parent VARCHAR(128) NULL;
ALTER TABLE billing_settlement_tasks ADD COLUMN trace_state VARCHAR(512) NULL;
ALTER TABLE billing_settlement_tasks ADD COLUMN telemetry_accepted_at_ms BIGINT NULL;
ALTER TABLE billing_settlement_tasks ADD COLUMN telemetry_queued_at_ms BIGINT NULL;

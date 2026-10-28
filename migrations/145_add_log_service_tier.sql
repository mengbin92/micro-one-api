-- Tier observations are append-only evidence; historical rows remain unknown.
ALTER TABLE logs ADD COLUMN requested_service_tier VARCHAR(32) NOT NULL DEFAULT '';
ALTER TABLE logs ADD COLUMN actual_service_tier VARCHAR(32) NOT NULL DEFAULT '';
ALTER TABLE logs ADD COLUMN service_tier_status VARCHAR(32) NOT NULL DEFAULT '';

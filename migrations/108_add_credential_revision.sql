-- Fence OAuth rotation writes against reauthorization and concurrent updates.
ALTER TABLE subscription_accounts ADD COLUMN credential_revision BIGINT NOT NULL DEFAULT 0;

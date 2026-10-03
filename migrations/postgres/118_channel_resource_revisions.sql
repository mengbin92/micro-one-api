ALTER TABLE channels ADD COLUMN authorization_revision BIGINT NOT NULL DEFAULT 1;
ALTER TABLE models ADD COLUMN authorization_revision BIGINT NOT NULL DEFAULT 1;

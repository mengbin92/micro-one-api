ALTER TABLE configs ADD COLUMN revision INTEGER NOT NULL DEFAULT 0;
CREATE INDEX IF NOT EXISTS idx_configs_revision ON configs(revision);

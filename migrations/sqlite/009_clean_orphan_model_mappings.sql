-- v0.11.0 review L4: remove orphaned model mappings whose parent channel or
-- subscription account no longer exists, then add ON DELETE CASCADE foreign
-- keys so future deletions keep the mapping tables consistent.
--
-- Mirrors MySQL migration 070_clean_orphan_model_mappings.sql.
-- SQLite does not support ADD FOREIGN KEY on an existing table, so we recreate
-- the mapping tables with the extra FKs and copy the data over.

DELETE FROM model_channel_mapping WHERE channel_id NOT IN (SELECT id FROM channels);
DELETE FROM model_subscription_mapping WHERE subscription_account_id NOT IN (SELECT id FROM subscription_accounts);

PRAGMA foreign_keys=OFF;

-- Recreate model_channel_mapping with cascade FK on channel_id.
CREATE TABLE model_channel_mapping_new (
  id INTEGER PRIMARY KEY AUTOINCREMENT,
  channel_id INTEGER NOT NULL REFERENCES channels(id) ON DELETE CASCADE,
  model_id INTEGER NOT NULL REFERENCES models(id) ON DELETE CASCADE,
  upstream_model_id TEXT NOT NULL DEFAULT '',
  enabled INTEGER NOT NULL DEFAULT 1,
  priority INTEGER NOT NULL DEFAULT 0,
  config TEXT DEFAULT '',
  created_at INTEGER NOT NULL DEFAULT 0,
  updated_at INTEGER NOT NULL DEFAULT 0
);

INSERT INTO model_channel_mapping_new SELECT * FROM model_channel_mapping;
DROP TABLE model_channel_mapping;
ALTER TABLE model_channel_mapping_new RENAME TO model_channel_mapping;

CREATE UNIQUE INDEX idx_mcm_channel_model ON model_channel_mapping(channel_id, model_id);
CREATE INDEX idx_mcm_channel_id ON model_channel_mapping(channel_id);
CREATE INDEX idx_mcm_model_id ON model_channel_mapping(model_id);

-- Recreate model_subscription_mapping with cascade FK on subscription_account_id.
CREATE TABLE model_subscription_mapping_new (
  id INTEGER PRIMARY KEY AUTOINCREMENT,
  subscription_account_id INTEGER NOT NULL REFERENCES subscription_accounts(id) ON DELETE CASCADE,
  model_id INTEGER NOT NULL REFERENCES models(id) ON DELETE CASCADE,
  upstream_model_id TEXT NOT NULL DEFAULT '',
  group_name TEXT NOT NULL DEFAULT 'default',
  enabled INTEGER NOT NULL DEFAULT 1,
  priority INTEGER NOT NULL DEFAULT 0,
  created_at INTEGER NOT NULL DEFAULT 0,
  updated_at INTEGER NOT NULL DEFAULT 0
);

INSERT INTO model_subscription_mapping_new SELECT * FROM model_subscription_mapping;
DROP TABLE model_subscription_mapping;
ALTER TABLE model_subscription_mapping_new RENAME TO model_subscription_mapping;

CREATE UNIQUE INDEX idx_msm_account_model_group ON model_subscription_mapping(subscription_account_id, model_id, group_name);
CREATE INDEX idx_msm_account_id ON model_subscription_mapping(subscription_account_id);
CREATE INDEX idx_msm_model_id ON model_subscription_mapping(model_id);
CREATE INDEX idx_msm_group ON model_subscription_mapping(group_name);

PRAGMA foreign_keys=ON;

-- Model routing table (SQLite). Mirrors MySQL migration 065.
-- See docs/model-management-design.md §9.3 #3 / §9.4 P2.
CREATE TABLE IF NOT EXISTS model_routings (
  id INTEGER PRIMARY KEY AUTOINCREMENT,
  group_name TEXT NOT NULL DEFAULT 'default',
  model TEXT NOT NULL,
  platform TEXT NOT NULL DEFAULT '',
  subscription_account_id INTEGER NOT NULL,
  enabled INTEGER DEFAULT 1,
  priority INTEGER NOT NULL DEFAULT 0,
  created_at INTEGER NOT NULL DEFAULT 0,
  updated_at INTEGER NOT NULL DEFAULT 0
);

CREATE UNIQUE INDEX IF NOT EXISTS idx_model_routings_group_model_account_platform
  ON model_routings(group_name, model, platform, subscription_account_id);
CREATE INDEX IF NOT EXISTS idx_model_routings_group_model
  ON model_routings(group_name, model);
CREATE INDEX IF NOT EXISTS idx_model_routings_account
  ON model_routings(subscription_account_id);

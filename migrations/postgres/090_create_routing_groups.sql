CREATE TABLE routing_groups (
  id BIGSERIAL PRIMARY KEY,
  key TEXT COLLATE "C" NOT NULL UNIQUE,
  display_name TEXT NOT NULL,
  description TEXT NOT NULL,
  status TEXT NOT NULL DEFAULT 'disabled',
  access_mode TEXT NOT NULL DEFAULT 'restricted',
  model_access_mode TEXT NOT NULL DEFAULT 'all_authorized',
  sort_order INTEGER NOT NULL DEFAULT 0,
  revision BIGINT NOT NULL DEFAULT 1,
  created_at BIGINT NOT NULL,
  updated_at BIGINT NOT NULL
);
CREATE TABLE channel_routing_groups (
  channel_id BIGINT NOT NULL REFERENCES channels(id) ON DELETE CASCADE,
  routing_group_id BIGINT NOT NULL REFERENCES routing_groups(id),
  PRIMARY KEY (channel_id, routing_group_id)
);
CREATE INDEX idx_channel_routing_group ON channel_routing_groups (routing_group_id, channel_id);
CREATE TABLE account_routing_groups (
  subscription_account_id BIGINT NOT NULL REFERENCES subscription_accounts(id) ON DELETE CASCADE,
  routing_group_id BIGINT NOT NULL REFERENCES routing_groups(id),
  PRIMARY KEY (subscription_account_id, routing_group_id)
);
CREATE INDEX idx_account_routing_group ON account_routing_groups (routing_group_id, subscription_account_id);
ALTER TABLE model_subscription_mapping ADD COLUMN routing_group_id BIGINT NULL;
CREATE INDEX idx_model_subscription_routing_group ON model_subscription_mapping (routing_group_id, model_id);
ALTER TABLE model_routings ADD COLUMN routing_group_id BIGINT NULL;
CREATE INDEX idx_model_routing_group ON model_routings (routing_group_id, model);
CREATE TABLE routing_group_backfills (
  report_hash TEXT PRIMARY KEY,
  completed_at BIGINT NOT NULL,
  group_count INTEGER NOT NULL
);

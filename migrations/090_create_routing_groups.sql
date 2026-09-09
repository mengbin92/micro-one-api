-- Phase B is additive. Populate explicitly with group-backfill; never at startup.
-- VARBINARY keeps case and trailing bytes significant, including legacy keys.
CREATE TABLE routing_groups (
  id BIGINT NOT NULL AUTO_INCREMENT PRIMARY KEY,
  `key` VARBINARY(1024) NOT NULL,
  display_name TEXT NOT NULL,
  description TEXT NOT NULL,
  status VARCHAR(16) NOT NULL DEFAULT 'disabled',
  access_mode VARCHAR(16) NOT NULL DEFAULT 'restricted',
  model_access_mode VARCHAR(32) NOT NULL DEFAULT 'all_authorized',
  sort_order INT NOT NULL DEFAULT 0,
  revision BIGINT NOT NULL DEFAULT 1,
  created_at BIGINT NOT NULL,
  updated_at BIGINT NOT NULL,
  UNIQUE KEY uniq_routing_group_key (`key`)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4;
CREATE TABLE channel_routing_groups (
  channel_id BIGINT NOT NULL,
  routing_group_id BIGINT NOT NULL,
  PRIMARY KEY (channel_id, routing_group_id),
  KEY idx_channel_routing_group (routing_group_id, channel_id),
  FOREIGN KEY (channel_id) REFERENCES channels(id) ON DELETE CASCADE,
  FOREIGN KEY (routing_group_id) REFERENCES routing_groups(id)
) ENGINE=InnoDB;
CREATE TABLE account_routing_groups (
  subscription_account_id BIGINT NOT NULL,
  routing_group_id BIGINT NOT NULL,
  PRIMARY KEY (subscription_account_id, routing_group_id),
  KEY idx_account_routing_group (routing_group_id, subscription_account_id),
  FOREIGN KEY (subscription_account_id) REFERENCES subscription_accounts(id) ON DELETE CASCADE,
  FOREIGN KEY (routing_group_id) REFERENCES routing_groups(id)
) ENGINE=InnoDB;
ALTER TABLE model_subscription_mapping ADD COLUMN routing_group_id BIGINT NULL,
  ADD KEY idx_model_subscription_routing_group (routing_group_id, model_id);
ALTER TABLE model_routings ADD COLUMN routing_group_id BIGINT NULL,
  ADD KEY idx_model_routing_group (routing_group_id, model);
CREATE TABLE routing_group_backfills (
  report_hash VARCHAR(64) NOT NULL PRIMARY KEY,
  completed_at BIGINT NOT NULL,
  group_count INT NOT NULL
) ENGINE=InnoDB;

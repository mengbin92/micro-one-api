ALTER TABLE users ADD COLUMN default_routing_group_id BIGINT NULL,
  ADD COLUMN routing_access_revision BIGINT NOT NULL DEFAULT 0,
  ADD COLUMN public_group_access VARCHAR(16) NOT NULL DEFAULT 'explicit_only';
ALTER TABLE tokens ADD COLUMN routing_mode VARCHAR(16) NOT NULL DEFAULT 'inherit',
  ADD COLUMN routing_group_id BIGINT NULL,
  ADD COLUMN routing_revision BIGINT NOT NULL DEFAULT 1;
CREATE TABLE user_routing_group_grants (
  user_id BIGINT NOT NULL,
  routing_group_id BIGINT NOT NULL,
  source_type VARCHAR(16) NOT NULL,
  source_ref VARCHAR(128) NOT NULL,
  starts_at BIGINT NOT NULL DEFAULT 0,
  expires_at BIGINT NOT NULL DEFAULT 0,
  status VARCHAR(16) NOT NULL DEFAULT 'active',
  PRIMARY KEY (user_id, routing_group_id, source_type, source_ref),
  KEY idx_user_routing_group (routing_group_id, user_id),
  FOREIGN KEY (user_id) REFERENCES users(id) ON DELETE CASCADE
) ENGINE=InnoDB;

ALTER TABLE users ADD COLUMN default_routing_group_id BIGINT NULL,
  ADD COLUMN routing_access_revision BIGINT NOT NULL DEFAULT 0,
  ADD COLUMN public_group_access VARCHAR(16) NOT NULL DEFAULT 'explicit_only';
ALTER TABLE tokens ADD COLUMN routing_mode VARCHAR(16) NOT NULL DEFAULT 'inherit',
  ADD COLUMN routing_group_id BIGINT NULL,
  ADD COLUMN routing_revision BIGINT NOT NULL DEFAULT 1;
CREATE TABLE user_routing_group_grants (
  user_id BIGINT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
  routing_group_id BIGINT NOT NULL,
  source_type VARCHAR(16) NOT NULL,
  source_ref VARCHAR(64) NOT NULL,
  starts_at BIGINT NOT NULL DEFAULT 0,
  expires_at BIGINT NOT NULL DEFAULT 0,
  status VARCHAR(16) NOT NULL DEFAULT 'active',
  PRIMARY KEY (user_id, routing_group_id, source_type, source_ref)
);
CREATE INDEX idx_user_routing_group ON user_routing_group_grants(routing_group_id, user_id);

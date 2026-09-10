ALTER TABLE users ADD COLUMN default_routing_group_id INTEGER NULL;
ALTER TABLE users ADD COLUMN routing_access_revision INTEGER NOT NULL DEFAULT 0;
ALTER TABLE users ADD COLUMN public_group_access TEXT NOT NULL DEFAULT 'explicit_only';
ALTER TABLE tokens ADD COLUMN routing_mode TEXT NOT NULL DEFAULT 'inherit';
ALTER TABLE tokens ADD COLUMN routing_group_id INTEGER NULL;
ALTER TABLE tokens ADD COLUMN routing_revision INTEGER NOT NULL DEFAULT 1;
CREATE TABLE user_routing_group_grants (
  user_id INTEGER NOT NULL REFERENCES users(id) ON DELETE CASCADE,
  routing_group_id INTEGER NOT NULL,
  source_type TEXT NOT NULL,
  source_ref TEXT NOT NULL,
  starts_at INTEGER NOT NULL DEFAULT 0,
  expires_at INTEGER NOT NULL DEFAULT 0,
  status TEXT NOT NULL DEFAULT 'active',
  PRIMARY KEY (user_id, routing_group_id, source_type, source_ref)
);
CREATE INDEX idx_user_routing_group ON user_routing_group_grants(routing_group_id, user_id);

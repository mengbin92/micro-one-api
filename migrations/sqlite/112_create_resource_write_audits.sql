-- Shared resource owners record IAM writes atomically with their resource.
-- No resource contents or credentials are persisted in this audit stream.
CREATE TABLE resource_write_audits (
 event_id VARCHAR(64) PRIMARY KEY,
 actor_user_id BIGINT NOT NULL,
 operation VARCHAR(160) NOT NULL,
 resource_id VARCHAR(128) NOT NULL,
 decision_versions TEXT NOT NULL,
 reason TEXT NOT NULL,
 occurred_at BIGINT NOT NULL,
 result VARCHAR(16) NOT NULL CHECK (result IN ('success', 'failure'))
);
CREATE INDEX resource_write_audits_actor_time ON resource_write_audits (actor_user_id, occurred_at);

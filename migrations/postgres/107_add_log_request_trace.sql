-- Additive execution audit fields. Historical identities remain unknown.
ALTER TABLE logs ADD COLUMN root_request_id VARCHAR(128) NOT NULL DEFAULT '';
ALTER TABLE logs ADD COLUMN attempt_number INTEGER NOT NULL DEFAULT 0;
ALTER TABLE logs ADD COLUMN reservation_id VARCHAR(64) NOT NULL DEFAULT '';
ALTER TABLE logs ADD COLUMN source_kind VARCHAR(32) NOT NULL DEFAULT '';
ALTER TABLE logs ADD COLUMN upstream_model_id VARCHAR(255) NOT NULL DEFAULT '';
CREATE INDEX idx_log_user_root ON logs (user_id, root_request_id);

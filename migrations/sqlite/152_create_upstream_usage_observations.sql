-- Observation-only upstream usage evidence. This is NOT the consumption log:
-- it never feeds usage aggregates, call counts, quota or charges, and it
-- carries no settlement instruction, lease or writable amount.
CREATE TABLE IF NOT EXISTS upstream_usage_observations (
  event_id VARCHAR(255) PRIMARY KEY,
  send_id VARCHAR(160) NOT NULL,
  observation_id VARCHAR(64) NOT NULL,
  category VARCHAR(16) NOT NULL,
  sequence BIGINT NOT NULL,
  payload_sha256 VARCHAR(64) NOT NULL,
  payload TEXT NOT NULL,
  user_id BIGINT NOT NULL DEFAULT 0,
  usage_protocol VARCHAR(64) NOT NULL DEFAULT '',
  source_kind VARCHAR(32) NOT NULL DEFAULT '',
  source_id BIGINT NOT NULL DEFAULT 0,
  purpose VARCHAR(32) NOT NULL DEFAULT '',
  upstream_model_id VARCHAR(255) NOT NULL DEFAULT '',
  attempt_request_id VARCHAR(128) NOT NULL DEFAULT '',
  root_request_id VARCHAR(128) NOT NULL DEFAULT '',
  capture VARCHAR(32) NOT NULL DEFAULT '',
  capture_reason VARCHAR(64) NOT NULL DEFAULT '',
  usage_status VARCHAR(16) NOT NULL DEFAULT '',
  parser_revision VARCHAR(64) NOT NULL DEFAULT '',
  contract_revision VARCHAR(64) NOT NULL DEFAULT '',
  build_revision VARCHAR(128) NOT NULL DEFAULT '',
  http_status INT NOT NULL DEFAULT 0,
  is_stream INTEGER NOT NULL DEFAULT 0,
  observed_at_ms BIGINT NOT NULL,
  received_at_ms BIGINT NOT NULL
);
CREATE INDEX idx_usage_observation_send ON upstream_usage_observations (observation_id, category);
CREATE INDEX idx_usage_observation_send_id ON upstream_usage_observations (send_id);
CREATE INDEX idx_usage_observation_user_time ON upstream_usage_observations (user_id, observed_at_ms, event_id);
CREATE INDEX idx_usage_observation_observed ON upstream_usage_observations (observed_at_ms, event_id);
CREATE INDEX idx_usage_observation_received ON upstream_usage_observations (received_at_ms);
CREATE INDEX idx_usage_observation_capture ON upstream_usage_observations (capture);
CREATE INDEX idx_usage_observation_status ON upstream_usage_observations (usage_status);
CREATE INDEX idx_usage_observation_purpose ON upstream_usage_observations (purpose);

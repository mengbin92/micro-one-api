-- Nullable observation metadata; historical rows remain unobserved.
ALTER TABLE logs ADD COLUMN request_timing_json TEXT NULL;
ALTER TABLE logs ADD COLUMN observation_id VARCHAR(36) NULL;
ALTER TABLE logs ADD COLUMN otel_trace_id VARCHAR(32) NULL;
ALTER TABLE logs ADD COLUMN request_started_at_ms BIGINT NULL;
ALTER TABLE logs ADD COLUMN request_outcome VARCHAR(16) NULL;
CREATE UNIQUE INDEX idx_log_observation ON logs (observation_id);
CREATE INDEX idx_log_trace_id ON logs (otel_trace_id);
CREATE INDEX idx_log_trace_root_time ON logs (source, user_id, root_request_id, request_started_at_ms, id);
CREATE INDEX idx_log_trace_user_time ON logs (source, user_id, request_started_at_ms, id);
CREATE INDEX idx_log_trace_time ON logs (source, request_started_at_ms, id);

-- All attempts, including failures before reservation. No FK to partitionable
-- logs: the owner deletes references/claims atomically with the summary.
CREATE TABLE request_trace_attempt_refs (
  observation_id VARCHAR(36) NOT NULL,
  attempt_number INTEGER NOT NULL,
  user_id BIGINT NOT NULL,
  request_id VARCHAR(128) NOT NULL,
  reservation_id VARCHAR(64) NULL,
  PRIMARY KEY (observation_id, attempt_number)
);
CREATE INDEX idx_trace_ref_reservation ON request_trace_attempt_refs (user_id, reservation_id);

CREATE TABLE IF NOT EXISTS model_health_states (
  id bigserial PRIMARY KEY,
  source_kind varchar(32) NOT NULL,
  source_id bigint NOT NULL,
  model_id varchar(191) NOT NULL,
  upstream_model_id varchar(191) NOT NULL,
  status varchar(32) NOT NULL DEFAULT 'healthy',
  request_count bigint NOT NULL DEFAULT 0,
  success_count bigint NOT NULL DEFAULT 0,
  failure_count bigint NOT NULL DEFAULT 0,
  consecutive_failures integer NOT NULL DEFAULT 0,
  total_latency_ms bigint NOT NULL DEFAULT 0,
  avg_latency_ms bigint NOT NULL DEFAULT 0,
  last_error text,
  last_checked_at bigint NOT NULL DEFAULT 0,
  last_success_at bigint NOT NULL DEFAULT 0,
  last_failure_at bigint NOT NULL DEFAULT 0,
  created_at bigint NOT NULL DEFAULT 0,
  updated_at bigint NOT NULL DEFAULT 0
);

CREATE UNIQUE INDEX IF NOT EXISTS uk_model_health_route
  ON model_health_states (source_kind, source_id, model_id, upstream_model_id);
CREATE INDEX IF NOT EXISTS idx_model_health_status_checked
  ON model_health_states (status, last_checked_at);
CREATE INDEX IF NOT EXISTS idx_model_health_model_checked
  ON model_health_states (model_id, last_checked_at);

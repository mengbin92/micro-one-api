CREATE TABLE IF NOT EXISTS model_health_states (
  id INTEGER PRIMARY KEY AUTOINCREMENT,
  source_kind TEXT NOT NULL,
  source_id INTEGER NOT NULL,
  model_id TEXT NOT NULL,
  upstream_model_id TEXT NOT NULL,
  status TEXT NOT NULL DEFAULT 'healthy',
  request_count INTEGER NOT NULL DEFAULT 0,
  success_count INTEGER NOT NULL DEFAULT 0,
  failure_count INTEGER NOT NULL DEFAULT 0,
  consecutive_failures INTEGER NOT NULL DEFAULT 0,
  total_latency_ms INTEGER NOT NULL DEFAULT 0,
  avg_latency_ms INTEGER NOT NULL DEFAULT 0,
  last_error TEXT,
  last_checked_at INTEGER NOT NULL DEFAULT 0,
  last_success_at INTEGER NOT NULL DEFAULT 0,
  last_failure_at INTEGER NOT NULL DEFAULT 0,
  created_at INTEGER NOT NULL DEFAULT 0,
  updated_at INTEGER NOT NULL DEFAULT 0
);

CREATE UNIQUE INDEX IF NOT EXISTS uk_model_health_route
  ON model_health_states (source_kind, source_id, model_id, upstream_model_id);
CREATE INDEX IF NOT EXISTS idx_model_health_status_checked
  ON model_health_states (status, last_checked_at);
CREATE INDEX IF NOT EXISTS idx_model_health_model_checked
  ON model_health_states (model_id, last_checked_at);

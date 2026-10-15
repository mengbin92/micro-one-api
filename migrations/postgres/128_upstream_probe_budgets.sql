-- P1 monitor-owned durable evidence; no user authorization or pricing is changed.
CREATE TABLE IF NOT EXISTS monitor_probe_budgets (
  id VARCHAR(128) PRIMARY KEY,
  calls BIGINT NOT NULL DEFAULT 0,
  input_tokens BIGINT NOT NULL DEFAULT 0,
  output_tokens BIGINT NOT NULL DEFAULT 0,
  cost BIGINT NOT NULL DEFAULT 0,
  unknown_cost_calls BIGINT NOT NULL DEFAULT 0,
  lease_until BIGINT NOT NULL DEFAULT 0,
  lease_owner VARCHAR(128) NOT NULL DEFAULT '',
  paused BOOLEAN NOT NULL DEFAULT FALSE
);
CREATE TABLE IF NOT EXISTS monitor_probe_attempts (
  id VARCHAR(128) PRIMARY KEY,
  source_day VARCHAR(128) NOT NULL,
  target_id VARCHAR(64) NOT NULL,
  target_digest VARCHAR(64) NOT NULL,
  permit_claimed BOOLEAN NOT NULL DEFAULT FALSE,
  result TEXT NOT NULL,
  created_at BIGINT NOT NULL
);
CREATE TABLE IF NOT EXISTS upstream_drift_reports (
  id VARCHAR(64) PRIMARY KEY,
  first_seen BIGINT NOT NULL,
  last_seen BIGINT NOT NULL,
  occurrences BIGINT NOT NULL,
  report TEXT NOT NULL
);

CREATE TABLE IF NOT EXISTS monitor_probe_coverage (id VARCHAR(64) PRIMARY KEY, status VARCHAR(64) NOT NULL, scheduled_at BIGINT NOT NULL, next_due BIGINT NOT NULL);

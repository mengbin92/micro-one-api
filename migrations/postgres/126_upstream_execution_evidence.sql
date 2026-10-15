-- P1 billing-owned durable evidence; no user authorization or pricing is changed.
CREATE TABLE IF NOT EXISTS billing_execution_intents (
  id VARCHAR(200) PRIMARY KEY,
  reservation_id VARCHAR(128) NOT NULL,
  status VARCHAR(32) NOT NULL DEFAULT 'processing',
  payload TEXT NOT NULL,
  lease_until BIGINT NOT NULL,
  evidence_deadline BIGINT NOT NULL
);
CREATE TABLE IF NOT EXISTS upstream_execution_evidence (
  id VARCHAR(255) PRIMARY KEY,
  intent_id VARCHAR(200) NOT NULL,
  category VARCHAR(16) NOT NULL,
  source_kind VARCHAR(32) NOT NULL,
  source_id BIGINT NOT NULL,
  upstream_model_id VARCHAR(255) NOT NULL,
  protocol VARCHAR(64) NOT NULL DEFAULT '',
  purpose VARCHAR(32) NOT NULL,
  estimated_cost BIGINT NULL,
  cost_basis VARCHAR(32) NOT NULL,
  digest VARCHAR(64) NOT NULL,
  payload TEXT NOT NULL,
  received_at BIGINT NOT NULL
);

CREATE INDEX idx_execution_deadline ON billing_execution_intents (evidence_deadline, reservation_id);
CREATE INDEX idx_execution_source ON upstream_execution_evidence (source_kind, source_id, upstream_model_id, purpose, received_at);

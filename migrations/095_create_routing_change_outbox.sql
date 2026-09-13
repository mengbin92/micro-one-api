CREATE TABLE IF NOT EXISTS routing_change_outbox (
  id VARCHAR(96) PRIMARY KEY,
  owner VARCHAR(16) NOT NULL,
  kind VARCHAR(16) NOT NULL,
  aggregate_id BIGINT NOT NULL,
  revision BIGINT NOT NULL,
  created_at BIGINT NOT NULL,
  delivered_at BIGINT NOT NULL DEFAULT 0
);
CREATE INDEX idx_routing_outbox_pending ON routing_change_outbox(owner, delivered_at, created_at);

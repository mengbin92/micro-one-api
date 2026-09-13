CREATE TABLE IF NOT EXISTS routing_change_outbox (
  id TEXT PRIMARY KEY,
  owner VARCHAR(16) NOT NULL,
  kind VARCHAR(16) NOT NULL,
  aggregate_id INTEGER NOT NULL,
  revision INTEGER NOT NULL,
  created_at INTEGER NOT NULL,
  delivered_at INTEGER NOT NULL DEFAULT 0
);
CREATE INDEX idx_routing_outbox_pending ON routing_change_outbox(owner, delivered_at, created_at);

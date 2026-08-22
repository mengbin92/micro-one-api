-- See migrations/081_create_channel_usage_events.sql.

CREATE TABLE IF NOT EXISTS channel_usage_events (
  reservation_id TEXT PRIMARY KEY,
  channel_id INTEGER NOT NULL,
  quota INTEGER NOT NULL,
  created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP
);

CREATE INDEX IF NOT EXISTS idx_channel_usage_events_channel_created
  ON channel_usage_events (channel_id, created_at);

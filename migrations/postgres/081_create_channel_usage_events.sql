-- See migrations/081_create_channel_usage_events.sql.

CREATE TABLE IF NOT EXISTS channel_usage_events (
  reservation_id varchar(64) PRIMARY KEY,
  channel_id bigint NOT NULL,
  quota bigint NOT NULL,
  created_at timestamp(3) NOT NULL DEFAULT CURRENT_TIMESTAMP
);

CREATE INDEX IF NOT EXISTS idx_channel_usage_events_channel_created
  ON channel_usage_events (channel_id, created_at);

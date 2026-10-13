-- P1 channel-owned durable evidence; no user authorization or pricing is changed.
CREATE TABLE IF NOT EXISTS channel_upstream_inventory (
  id VARCHAR(128) PRIMARY KEY,
  models TEXT NOT NULL,
  diff TEXT NOT NULL,
  attempt_id VARCHAR(128) NOT NULL,
  observed_at BIGINT NOT NULL,
  manual BOOLEAN NOT NULL DEFAULT FALSE
);

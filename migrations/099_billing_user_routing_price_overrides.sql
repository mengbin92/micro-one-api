-- Phase F: user-specific routing-group price ratio overrides.
-- Heads + history, following the 095 routing_billing_policy precedent.
-- The user override REPLACES (never multiplies) the published group ratio.
CREATE TABLE user_routing_price_heads (
  user_id BIGINT NOT NULL,
  routing_group_id BIGINT NOT NULL,
  version BIGINT NOT NULL,
  PRIMARY KEY (user_id, routing_group_id)
);
CREATE TABLE user_routing_price_overrides (
  user_id BIGINT NOT NULL,
  routing_group_id BIGINT NOT NULL,
  version BIGINT NOT NULL,
  price_ratio DOUBLE NOT NULL,
  created_at BIGINT NOT NULL,
  PRIMARY KEY (user_id, routing_group_id, version)
);

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
  price_ratio DOUBLE PRECISION NOT NULL,
  created_at BIGINT NOT NULL,
  PRIMARY KEY (user_id, routing_group_id, version)
);

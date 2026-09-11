CREATE TABLE user_routing_price_heads (
  user_id INTEGER NOT NULL,
  routing_group_id INTEGER NOT NULL,
  version INTEGER NOT NULL,
  PRIMARY KEY (user_id, routing_group_id)
);
CREATE TABLE user_routing_price_overrides (
  user_id INTEGER NOT NULL,
  routing_group_id INTEGER NOT NULL,
  version INTEGER NOT NULL,
  price_ratio REAL NOT NULL,
  created_at INTEGER NOT NULL,
  PRIMARY KEY (user_id, routing_group_id, version)
);

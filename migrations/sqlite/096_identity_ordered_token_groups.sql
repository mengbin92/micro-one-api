CREATE TABLE token_routing_group_orders (
  token_id INTEGER NOT NULL REFERENCES tokens(id) ON DELETE CASCADE,
  routing_group_id INTEGER NOT NULL,
  position INTEGER NOT NULL,
  PRIMARY KEY (token_id, routing_group_id)
);
CREATE INDEX idx_token_routing_order ON token_routing_group_orders (token_id, position);

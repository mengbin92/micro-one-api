-- Phase F: ordered (auto) token routing. Normalized relation table keeps the
-- user-configured group order; routing_group_id has no FK here because the
-- identity schema does not own routing_groups (same precedent as
-- user_routing_group_grants in 092).
CREATE TABLE token_routing_group_orders (
  token_id BIGINT NOT NULL,
  routing_group_id BIGINT NOT NULL,
  position INT NOT NULL,
  PRIMARY KEY (token_id, routing_group_id),
  KEY idx_token_routing_order (token_id, position),
  FOREIGN KEY (token_id) REFERENCES tokens(id) ON DELETE CASCADE
) ENGINE=InnoDB;

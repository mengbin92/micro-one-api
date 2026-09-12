CREATE TABLE routing_billing_policy_heads (
 routing_group_id BIGINT PRIMARY KEY,
 version BIGINT NOT NULL
);
CREATE TABLE routing_billing_policies (
 routing_group_id BIGINT NOT NULL,
 version BIGINT NOT NULL,
 billing_mode VARCHAR(32) NOT NULL,
 price_ratio DOUBLE NOT NULL,
 effective_at BIGINT NOT NULL,
 PRIMARY KEY (routing_group_id,version)
);

CREATE TABLE subscription_contract_guard (id INTEGER PRIMARY KEY, revision BIGINT NOT NULL);
INSERT INTO subscription_contract_guard (id, revision) VALUES (1, 1);

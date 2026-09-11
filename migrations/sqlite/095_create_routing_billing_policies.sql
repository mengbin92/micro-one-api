CREATE TABLE routing_billing_policy_heads (
 routing_group_id INTEGER PRIMARY KEY,
 version INTEGER NOT NULL
);
CREATE TABLE routing_billing_policies (
 routing_group_id INTEGER NOT NULL,
 version INTEGER NOT NULL,
 billing_mode VARCHAR(32) NOT NULL,
 price_ratio REAL NOT NULL,
 effective_at INTEGER NOT NULL,
 PRIMARY KEY (routing_group_id,version)
);

CREATE TABLE subscription_contract_guard (id INTEGER PRIMARY KEY, revision BIGINT NOT NULL);
INSERT INTO subscription_contract_guard (id, revision) VALUES (1, 1);

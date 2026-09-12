ALTER TABLE subscription_plans ADD COLUMN contract_snapshot LONGTEXT NULL;
ALTER TABLE subscription_plans ADD COLUMN revision BIGINT NOT NULL DEFAULT 1;
ALTER TABLE user_subscriptions ADD COLUMN contract_snapshot LONGTEXT NULL;
ALTER TABLE user_subscriptions ADD COLUMN entitlement_revision BIGINT NOT NULL DEFAULT 1;
ALTER TABLE user_subscriptions ADD COLUMN source_order VARCHAR(192) NOT NULL DEFAULT '';
CREATE TABLE subscription_plan_routing_groups (
 plan_id BIGINT NOT NULL,
 routing_group_id BIGINT NOT NULL,
 grants_access INTEGER NOT NULL DEFAULT 0,
 PRIMARY KEY (plan_id, routing_group_id)
);
CREATE INDEX idx_plan_coverage_group ON subscription_plan_routing_groups(routing_group_id, plan_id);
CREATE TABLE subscription_routing_entitlements (
 subscription_id BIGINT NOT NULL,
 routing_group_id BIGINT NOT NULL,
 grants_access INTEGER NOT NULL DEFAULT 0,
 PRIMARY KEY (subscription_id, routing_group_id)
);
CREATE INDEX idx_subscription_coverage_group ON subscription_routing_entitlements(routing_group_id, subscription_id);

ALTER TABLE user_subscriptions ADD COLUMN price_paid BIGINT NOT NULL DEFAULT 0;
CREATE TABLE subscription_commerce_receipts (
 user_id BIGINT NOT NULL,
 request_id VARCHAR(128) NOT NULL,
 request_hash VARCHAR(64) NOT NULL,
 result LONGTEXT NOT NULL,
 PRIMARY KEY (user_id, request_id)
);

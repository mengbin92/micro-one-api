-- Model routing table (P2 #3): model→specified subscription account routing.
--
-- Mirrors sub2api Group.ModelRouting (map[string][]int64). Each row pins a
-- model name (or wildcard pattern) within a group to one or more subscription
-- accounts, overriding the normal priority-tier selection. When a routing
-- row matches the requested model, SelectSubscriptionAccount restricts its
-- candidate tier to the routed account set (still respecting
-- status/quota/runtime-blocked), so the request goes to the operator-chosen
-- upstream provider instead of the default weighted/random pick.
--
-- Owned by channel-service (migration ownership.yaml).
-- See docs/model-management-design.md §9.3 #3 / §9.4 P2.
CREATE TABLE IF NOT EXISTS `model_routings` (
  `id` bigint NOT NULL AUTO_INCREMENT,
  `group_name` varchar(100) NOT NULL DEFAULT 'default' COMMENT 'tenancy group (matches subscription_account_abilities.group)',
  `model` varchar(255) NOT NULL COMMENT 'client model name or wildcard pattern, e.g. claude-* / *',
  `platform` varchar(32) NOT NULL DEFAULT '' COMMENT 'optional platform filter; empty = any platform',
  `subscription_account_id` bigint NOT NULL COMMENT 'target subscription account',
  `enabled` tinyint(1) NOT NULL DEFAULT 1,
  `priority` int NOT NULL DEFAULT 0 COMMENT 'within a routed tier, higher wins (mirrors abilities.priority)',
  `created_at` bigint NOT NULL DEFAULT 0,
  `updated_at` bigint NOT NULL DEFAULT 0,
  PRIMARY KEY (`id`),
  UNIQUE KEY `uk_model_routings_group_model_account` (`group_name`, `model`, `platform`, `subscription_account_id`),
  KEY `idx_model_routings_group_model` (`group_name`, `model`),
  KEY `idx_model_routings_account` (`subscription_account_id`)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COMMENT='model→account routing overrides';

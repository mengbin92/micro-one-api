-- Subscription group configuration for quota and routing policy.

CREATE TABLE IF NOT EXISTS `subscription_groups` (
  `id` bigint NOT NULL AUTO_INCREMENT,
  `name` varchar(64) NOT NULL,
  `display_name` varchar(128) NOT NULL DEFAULT '',
  `platform` varchar(32) NOT NULL,
  `subscription_type` varchar(32) NOT NULL DEFAULT 'standard',
  `daily_limit_usd` decimal(12,4) DEFAULT NULL,
  `weekly_limit_usd` decimal(12,4) DEFAULT NULL,
  `monthly_limit_usd` decimal(12,4) DEFAULT NULL,
  `rate_multiplier` decimal(4,2) NOT NULL DEFAULT 1.0,
  `status` int NOT NULL DEFAULT 1,
  `created_at` bigint NOT NULL DEFAULT 0,
  `updated_at` bigint NOT NULL DEFAULT 0,
  PRIMARY KEY (`id`),
  UNIQUE KEY `idx_sub_groups_name` (`name`),
  KEY `idx_sub_groups_platform` (`platform`)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4;

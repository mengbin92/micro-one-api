-- User subscription records for the subscription upgrade plan.

CREATE TABLE IF NOT EXISTS `user_subscriptions` (
  `id` bigint NOT NULL AUTO_INCREMENT,
  `user_id` bigint NOT NULL,
  `group_id` bigint NOT NULL,
  `subscription_name` varchar(128) NOT NULL DEFAULT '',
  `status` varchar(32) NOT NULL DEFAULT 'active',
  `starts_at` bigint NOT NULL,
  `expires_at` bigint NOT NULL,
  `daily_usage_usd` decimal(12,4) NOT NULL DEFAULT 0,
  `weekly_usage_usd` decimal(12,4) NOT NULL DEFAULT 0,
  `monthly_usage_usd` decimal(12,4) NOT NULL DEFAULT 0,
  `daily_window_start` bigint NOT NULL DEFAULT 0,
  `weekly_window_start` bigint NOT NULL DEFAULT 0,
  `monthly_window_start` bigint NOT NULL DEFAULT 0,
  `metadata` text,
  `created_at` bigint NOT NULL DEFAULT 0,
  `updated_at` bigint NOT NULL DEFAULT 0,
  PRIMARY KEY (`id`),
  KEY `idx_user_subs_user_id` (`user_id`),
  KEY `idx_user_subs_group_id` (`group_id`),
  KEY `idx_user_subs_status` (`status`),
  KEY `idx_user_subs_expires_at` (`expires_at`)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4;

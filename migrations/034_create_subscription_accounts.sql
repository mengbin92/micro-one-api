-- Store OAuth subscription accounts separately from API-key channels.

CREATE TABLE IF NOT EXISTS `subscription_accounts` (
  `id` bigint NOT NULL AUTO_INCREMENT,
  `name` varchar(128) NOT NULL DEFAULT '',
  `platform` varchar(32) NOT NULL,
  `account_type` varchar(32) NOT NULL DEFAULT 'oauth',
  `status` int DEFAULT 1,
  `group` varchar(32) DEFAULT 'default',
  `models` text DEFAULT NULL,
  `priority` bigint DEFAULT 0,
  `base_url` varchar(256) DEFAULT NULL,
  `access_token` text DEFAULT NULL,
  `refresh_token` text DEFAULT NULL,
  `expires_at` bigint DEFAULT 0,
  `account_id` varchar(128) DEFAULT '',
  `fingerprint` text DEFAULT NULL,
  `metadata` text DEFAULT NULL,
  `created_at` bigint DEFAULT 0,
  `updated_at` bigint DEFAULT 0,
  PRIMARY KEY (`id`),
  KEY `idx_subscription_platform_status` (`platform`, `status`),
  KEY `idx_subscription_group` (`group`)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4;

CREATE TABLE IF NOT EXISTS `subscription_account_abilities` (
  `id` bigint NOT NULL AUTO_INCREMENT,
  `group` varchar(32) NOT NULL DEFAULT 'default',
  `model` varchar(128) NOT NULL,
  `platform` varchar(32) NOT NULL,
  `account_id` bigint NOT NULL,
  `enabled` tinyint(1) DEFAULT 1,
  `priority` bigint DEFAULT 0,
  PRIMARY KEY (`id`),
  KEY `idx_subscription_account_id` (`account_id`),
  KEY `idx_subscription_model_group_platform` (`model`, `group`, `platform`)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4;

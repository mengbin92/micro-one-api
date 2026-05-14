CREATE TABLE IF NOT EXISTS `user_oauth_identities` (
  `id` bigint NOT NULL AUTO_INCREMENT,
  `user_id` bigint NOT NULL,
  `provider` varchar(32) NOT NULL,
  `provider_id` varchar(128) NOT NULL,
  `created_at` bigint NOT NULL DEFAULT 0,
  `updated_at` bigint NOT NULL DEFAULT 0,
  PRIMARY KEY (`id`),
  UNIQUE KEY `uk_provider_provider_id` (`provider`, `provider_id`),
  UNIQUE KEY `uk_user_provider` (`user_id`, `provider`),
  KEY `idx_user_id` (`user_id`)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4;

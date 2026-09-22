CREATE TABLE IF NOT EXISTS `identity_token_quota_dedupe` (
  `id` bigint NOT NULL AUTO_INCREMENT,
  `reservation_id` varchar(64) NOT NULL,
  `user_id` bigint NOT NULL,
  `token_id` bigint NOT NULL,
  `amount` bigint NOT NULL,
  `remaining` bigint NOT NULL,
  `created_at` bigint NOT NULL DEFAULT 0,
  PRIMARY KEY (`id`),
  UNIQUE KEY `uq_identity_token_quota_dedupe` (`reservation_id`, `user_id`, `token_id`)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4;

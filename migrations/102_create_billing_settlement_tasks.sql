CREATE TABLE IF NOT EXISTS `billing_settlement_tasks` (
  `id` bigint NOT NULL AUTO_INCREMENT,
  `reservation_id` varchar(64) NOT NULL,
  `payload` text NOT NULL,
  `status` varchar(16) NOT NULL DEFAULT 'pending',
  `attempts` int NOT NULL DEFAULT 0,
  `last_error` text NULL,
  `next_retry_at` bigint NOT NULL DEFAULT 0,
  `created_at` bigint NOT NULL DEFAULT 0,
  `updated_at` bigint NOT NULL DEFAULT 0,
  PRIMARY KEY (`id`),
  UNIQUE KEY `uq_billing_settlement_tasks_reservation` (`reservation_id`),
  KEY `idx_billing_settlement_tasks_pending` (`status`, `next_retry_at`)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4;

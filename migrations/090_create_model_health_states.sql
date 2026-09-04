-- Passive model health snapshots derived from real relay traffic.
-- The key includes both the client-facing model and the concrete upstream
-- model so per-source rewrites never merge unrelated upstream routes.
CREATE TABLE IF NOT EXISTS `model_health_states` (
  `id` bigint NOT NULL AUTO_INCREMENT,
  `source_kind` varchar(32) NOT NULL COMMENT 'channel | subscription',
  `source_id` bigint NOT NULL,
  `model_id` varchar(191) NOT NULL,
  `upstream_model_id` varchar(191) NOT NULL,
  `status` varchar(32) NOT NULL DEFAULT 'healthy',
  `request_count` bigint NOT NULL DEFAULT 0,
  `success_count` bigint NOT NULL DEFAULT 0,
  `failure_count` bigint NOT NULL DEFAULT 0,
  `consecutive_failures` int NOT NULL DEFAULT 0,
  `avg_latency_ms` bigint NOT NULL DEFAULT 0,
  `last_error` text NULL,
  `last_checked_at` bigint NOT NULL DEFAULT 0,
  `last_success_at` bigint NOT NULL DEFAULT 0,
  `last_failure_at` bigint NOT NULL DEFAULT 0,
  `created_at` bigint NOT NULL DEFAULT 0,
  `updated_at` bigint NOT NULL DEFAULT 0,
  PRIMARY KEY (`id`),
  UNIQUE KEY `uk_model_health_route` (`source_kind`, `source_id`, `model_id`, `upstream_model_id`),
  KEY `idx_model_health_status_checked` (`status`, `last_checked_at`),
  KEY `idx_model_health_model_checked` (`model_id`, `last_checked_at`)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COMMENT='Passive per-source model health snapshots';

-- Codex 5h / 7d upstream subscription quota snapshots.

CREATE TABLE IF NOT EXISTS `account_quota_snapshots` (
  `account_id` bigint NOT NULL,
  `primary_used_percent` double DEFAULT NULL,
  `primary_reset_after_seconds` int DEFAULT NULL,
  `primary_window_minutes` int DEFAULT NULL,
  `secondary_used_percent` double DEFAULT NULL,
  `secondary_reset_after_seconds` int DEFAULT NULL,
  `secondary_window_minutes` int DEFAULT NULL,
  `primary_over_secondary_percent` double DEFAULT NULL,
  `updated_at` timestamp NULL DEFAULT NULL,
  `snapshot_paused` tinyint(1) NOT NULL DEFAULT 0,
  PRIMARY KEY (`account_id`),
  KEY `idx_account_quota_snapshot_updated` (`updated_at`)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4;

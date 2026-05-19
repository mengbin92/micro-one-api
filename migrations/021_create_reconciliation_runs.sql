-- Persist each reconciliation run so admins can review history.
-- Discrepancies are stored as a JSON array on the run row to keep the schema simple;
-- typical runs have zero or a handful of inconsistencies, so a separate table is unnecessary.

CREATE TABLE IF NOT EXISTS `reconciliation_runs` (
  `id` bigint NOT NULL AUTO_INCREMENT,
  `run_at` bigint NOT NULL DEFAULT 0,
  `expired_cleaned` int NOT NULL DEFAULT 0,
  `total_accounts` int NOT NULL DEFAULT 0,
  `total_reservations` int NOT NULL DEFAULT 0,
  `discrepancy_count` int NOT NULL DEFAULT 0,
  `discrepancies` text,
  `created_at` bigint NOT NULL DEFAULT 0,
  PRIMARY KEY (`id`),
  KEY `idx_run_at` (`run_at`)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4;

ALTER TABLE `channels`
  ADD COLUMN `health_status` varchar(32) NOT NULL DEFAULT 'healthy' AFTER `consecutive_balance_refresh_failures`,
  ADD COLUMN `health_last_error` text AFTER `health_status`,
  ADD COLUMN `health_last_success_time` bigint DEFAULT 0 AFTER `health_last_error`,
  ADD COLUMN `health_last_failure_time` bigint DEFAULT 0 AFTER `health_last_success_time`,
  ADD COLUMN `health_consecutive_failures` int DEFAULT 0 AFTER `health_last_failure_time`,
  ADD COLUMN `circuit_opened_until` bigint DEFAULT 0 AFTER `health_consecutive_failures`;

CREATE INDEX `idx_channels_health_status` ON `channels` (`health_status`);
CREATE INDEX `idx_channels_circuit_opened_until` ON `channels` (`circuit_opened_until`);

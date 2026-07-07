-- Idempotent log for automated fixed-strategy quota resets performed by the
-- account-ops sweeper. One row per (account, window-start, scope) so that a
-- repeated worker tick within the same natural-day/week boundary does not
-- reset or write a duplicate event.

CREATE TABLE IF NOT EXISTS `subscription_account_quota_reset_runs` (
  `id` bigint NOT NULL AUTO_INCREMENT,
  `subscription_account_id` bigint NOT NULL,
  `scope` varchar(16) NOT NULL COMMENT 'daily or weekly',
  `window_start` bigint NOT NULL COMMENT 'unix ts of the fixed window boundary that was reset to',
  `strategy` varchar(16) NOT NULL DEFAULT 'fixed',
  `timezone` varchar(64) NOT NULL DEFAULT 'UTC',
  `reset_at` bigint NOT NULL COMMENT 'unix ts when the reset was applied',
  PRIMARY KEY (`id`),
  UNIQUE KEY `idx_subscription_account_quota_reset_runs_dedupe` (`subscription_account_id`, `scope`, `window_start`),
  KEY `idx_subscription_account_quota_reset_runs_account_time` (`subscription_account_id`, `reset_at`)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COMMENT='automated fixed-strategy quota reset runs';

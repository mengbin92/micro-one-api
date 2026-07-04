-- Add local subscription-account quota budgets. These fields are maintained by
-- channel-service and used by routing to skip accounts whose local budget is
-- exhausted. Limits are USD-denominated; 0 means unlimited.

ALTER TABLE `subscription_accounts`
  ADD COLUMN `quota_limit_usd` decimal(18,6) NOT NULL DEFAULT 0 COMMENT 'local total budget limit in USD; 0 means unlimited',
  ADD COLUMN `quota_used_usd` decimal(18,6) NOT NULL DEFAULT 0 COMMENT 'local total budget used in USD',
  ADD COLUMN `quota_daily_limit_usd` decimal(18,6) NOT NULL DEFAULT 0 COMMENT 'local rolling 24h budget limit in USD; 0 means unlimited',
  ADD COLUMN `quota_daily_used_usd` decimal(18,6) NOT NULL DEFAULT 0 COMMENT 'local rolling 24h budget used in USD',
  ADD COLUMN `quota_daily_window_start` bigint NOT NULL DEFAULT 0 COMMENT 'unix ts when the local 24h window started',
  ADD COLUMN `quota_weekly_limit_usd` decimal(18,6) NOT NULL DEFAULT 0 COMMENT 'local rolling 7d budget limit in USD; 0 means unlimited',
  ADD COLUMN `quota_weekly_used_usd` decimal(18,6) NOT NULL DEFAULT 0 COMMENT 'local rolling 7d budget used in USD',
  ADD COLUMN `quota_weekly_window_start` bigint NOT NULL DEFAULT 0 COMMENT 'unix ts when the local 7d window started',
  ADD COLUMN `rate_multiplier` decimal(10,4) NOT NULL DEFAULT 1 COMMENT 'local account usage multiplier',
  ADD INDEX `idx_subscription_local_quota` (`status`, `quota_daily_window_start`, `quota_weekly_window_start`);

-- Add the subscription-account lifecycle columns described in the hybrid relay
-- plan (§4.6.1): concurrency budget, rate-limit cooldown and quota-window
-- tracking. These back Phase 5 (concurrency slots, 429 cooldown, quota-window
-- awareness) so the schema is ready before the gateway code starts writing to
-- them; all columns are nullable / default-zero so existing rows and code are
-- unaffected.

ALTER TABLE `subscription_accounts`
  ADD COLUMN `concurrency` int NOT NULL DEFAULT 1 COMMENT 'max in-flight requests for this account',
  ADD COLUMN `rate_limited_until` bigint NOT NULL DEFAULT 0 COMMENT 'unix ts until which the account is cooled down after an upstream 429/overload',
  ADD COLUMN `quota_used_percent` float NOT NULL DEFAULT 0 COMMENT 'subscription quota usage percentage reported by the upstream (0-100)',
  ADD COLUMN `quota_reset_at` bigint NOT NULL DEFAULT 0 COMMENT 'unix ts when the subscription quota window resets',
  ADD COLUMN `last_used_at` bigint NOT NULL DEFAULT 0 COMMENT 'unix ts of the last request served by this account',
  ADD INDEX `idx_subscription_expires` (`expires_at`),
  ADD INDEX `idx_subscription_rate_limited` (`rate_limited_until`);

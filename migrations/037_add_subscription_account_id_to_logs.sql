-- Mirror the subscription-account attribution on the centralized logs table
-- so the admin Logs page can filter/group by subscription account. The column
-- is nullable/default-zero so existing rows and API-key channel traffic are
-- unaffected.

ALTER TABLE `logs`
  ADD COLUMN `subscription_account_id` bigint NOT NULL DEFAULT 0 COMMENT '订阅账号 ID（0 = 非订阅账号请求）' AFTER `channel_id`,
  ADD KEY `idx_logs_subscription_account_created` (`subscription_account_id`, `created_at`);

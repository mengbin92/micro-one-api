-- Track which subscription account served each billing ledger entry so usage,
-- cost and profit can be attributed to a first-class subscription account
-- (Codex/Claude OAuth) rather than being silently folded into the generic
-- channel_id dimension. The column is nullable/default-zero so existing rows
-- and API-key channel traffic are unaffected.

ALTER TABLE `billing_ledgers`
  ADD COLUMN `subscription_account_id` bigint NOT NULL DEFAULT 0 COMMENT '订阅账号 ID（0 = 非订阅账号请求）' AFTER `channel_id`,
  ADD KEY `idx_billing_ledgers_subscription_account_created` (`subscription_account_id`, `created_at`);

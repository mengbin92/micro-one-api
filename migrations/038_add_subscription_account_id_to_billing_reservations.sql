-- Carry the resolved subscription account id through the reservation lifecycle
-- so CommitQuota can attribute the finalized ledger entry to the correct
-- subscription account even when the upstream account was selected at reserve
-- time. The column is nullable so existing reservations are unaffected.

ALTER TABLE `billing_reservations`
  ADD COLUMN `subscription_account_id` varchar(64) DEFAULT '0' COMMENT '订阅账号 ID（0 = 非订阅账号请求）' AFTER `channel_id`;

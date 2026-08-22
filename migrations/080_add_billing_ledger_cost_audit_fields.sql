-- Preserve the two independent dimensions needed for reliable accounting:
-- where the request ran (source_kind/upstream_model_id), and whether its
-- historical vendor cost is reconstructable (cost_audit_status).

ALTER TABLE `billing_ledgers`
  ADD COLUMN `source_kind` varchar(32) NOT NULL DEFAULT '' AFTER `subscription_account_id`,
  ADD COLUMN `upstream_model_id` varchar(191) NOT NULL DEFAULT '' AFTER `source_kind`,
  ADD COLUMN `cost_audit_status` varchar(16) NOT NULL DEFAULT 'legacy' AFTER `upstream_model_id`;

UPDATE `billing_ledgers`
SET `source_kind` = CASE
  WHEN `subscription_account_id` > 0 THEN 'subscription'
  WHEN `channel_id` > 0 THEN 'channel'
  ELSE ''
END
WHERE `source_kind` = '' AND `type` = 'consume';

-- Add One API compatible channel fields used by admin/channel APIs.

ALTER TABLE `channels`
  ADD COLUMN `weight` int unsigned DEFAULT 0 AFTER `name`,
  ADD COLUMN `created_time` bigint DEFAULT 0 AFTER `weight`,
  ADD COLUMN `test_time` bigint DEFAULT 0 AFTER `created_time`,
  ADD COLUMN `response_time` bigint DEFAULT 0 AFTER `test_time`,
  ADD COLUMN `balance` double DEFAULT 0 AFTER `base_url`,
  ADD COLUMN `balance_updated_time` bigint DEFAULT 0 AFTER `balance`,
  ADD COLUMN `used_quota` bigint DEFAULT 0 AFTER `group`,
  ADD COLUMN `model_mapping` varchar(1024) DEFAULT '' AFTER `used_quota`,
  ADD COLUMN `system_prompt` text DEFAULT NULL AFTER `config`;

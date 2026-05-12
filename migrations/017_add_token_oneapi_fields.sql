ALTER TABLE `tokens`
  ADD COLUMN `created_time` bigint DEFAULT 0 AFTER `name`,
  ADD COLUMN `accessed_time` bigint DEFAULT 0 AFTER `created_time`,
  ADD COLUMN `used_quota` bigint DEFAULT 0 AFTER `unlimited_quota`,
  ADD COLUMN `subnet` varchar(255) DEFAULT '' AFTER `models`;

UPDATE `tokens`
SET `created_time` = `created_at`
WHERE `created_time` = 0 AND `created_at` <> 0;

UPDATE `tokens`
SET `accessed_time` = `created_time`
WHERE `accessed_time` = 0 AND `created_time` <> 0;

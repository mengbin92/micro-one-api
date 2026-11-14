-- Retire the legacy one-api abilities projection. The model registry mappings
-- are now the single source of truth for channel and subscription candidates.
-- Keep exact legacy routes by backfilling them once before dropping old tables.
--
-- MySQL DDL commits implicitly, so the transaction opened by the runner gives
-- no atomicity. Guard both DDL statements with information_schema/PREPARE
-- (the 061/067 convention) and keep the destructive table drops last: a run
-- that failed before schema_migrations was recorded can then replay safely.
SET @moa_151_has_msm := (
  SELECT COUNT(*) FROM information_schema.TABLES
  WHERE TABLE_SCHEMA = DATABASE() AND TABLE_NAME = 'model_subscription_mapping'
);
SET @moa_151_has_config := (
  SELECT COUNT(*) FROM information_schema.COLUMNS
  WHERE TABLE_SCHEMA = DATABASE() AND TABLE_NAME = 'model_subscription_mapping'
    AND COLUMN_NAME = 'config'
);
SET @moa_151_add_config_sql := IF(
  @moa_151_has_msm > 0 AND @moa_151_has_config = 0,
  'ALTER TABLE `model_subscription_mapping` ADD COLUMN `config` varchar(255) NOT NULL DEFAULT '''' AFTER `priority`',
  'SELECT 1'
);
PREPARE moa_151_add_config_stmt FROM @moa_151_add_config_sql;
EXECUTE moa_151_add_config_stmt;
DEALLOCATE PREPARE moa_151_add_config_stmt;

INSERT INTO `models` (`model_id`, `display_name`, `provider`, `model_type`, `status`, `is_public`, `created_at`, `updated_at`)
SELECT LOWER(TRIM(a.model)), MIN(TRIM(a.model)), '', 'chat', 1, 1, UNIX_TIMESTAMP()*1000, UNIX_TIMESTAMP()*1000
FROM `abilities` a
LEFT JOIN `models` m ON LOWER(m.model_id) = LOWER(TRIM(a.model))
WHERE m.id IS NULL AND TRIM(a.model) <> '' AND a.model NOT LIKE '%*%' AND a.model NOT LIKE '%?%'
GROUP BY LOWER(TRIM(a.model))
ON DUPLICATE KEY UPDATE `updated_at` = `updated_at`;

INSERT INTO `models` (`model_id`, `display_name`, `provider`, `model_type`, `status`, `is_public`, `created_at`, `updated_at`)
SELECT LOWER(TRIM(saa.model)), MIN(TRIM(saa.model)), '', 'chat', 1, 1, UNIX_TIMESTAMP()*1000, UNIX_TIMESTAMP()*1000
FROM `subscription_account_abilities` saa
LEFT JOIN `models` m ON LOWER(m.model_id) = LOWER(TRIM(saa.model))
WHERE m.id IS NULL AND TRIM(saa.model) <> '' AND saa.model NOT LIKE '%*%' AND saa.model NOT LIKE '%?%'
GROUP BY LOWER(TRIM(saa.model))
ON DUPLICATE KEY UPDATE `updated_at` = `updated_at`;

INSERT INTO `model_channel_mapping`
  (`channel_id`, `model_id`, `upstream_model_id`, `enabled`, `priority`, `config`, `created_at`, `updated_at`)
SELECT a.channel_id, m.id, MAX(TRIM(a.model)), 1, COALESCE(MAX(a.priority), 0),
       '{"_managed_by":"channel_models"}', UNIX_TIMESTAMP()*1000, UNIX_TIMESTAMP()*1000
FROM `abilities` a
JOIN `channels` c ON c.id = a.channel_id
JOIN `models` m ON LOWER(m.model_id) = LOWER(TRIM(a.model))
LEFT JOIN `model_channel_mapping` mcm ON mcm.channel_id = a.channel_id AND mcm.model_id = m.id
WHERE mcm.id IS NULL AND TRIM(a.model) <> '' AND a.model NOT LIKE '%*%' AND a.model NOT LIKE '%?%'
GROUP BY a.channel_id, m.id
ON DUPLICATE KEY UPDATE `model_channel_mapping`.`enabled` = VALUES(`enabled`);

INSERT INTO `model_subscription_mapping`
  (`subscription_account_id`, `model_id`, `upstream_model_id`, `group_name`, `enabled`, `priority`, `config`, `created_at`, `updated_at`)
SELECT saa.account_id, m.id, MAX(TRIM(saa.model)), saa.`group`, 1, COALESCE(MAX(saa.priority), 0),
       '{"_managed_by":"subscription_models"}', UNIX_TIMESTAMP()*1000, UNIX_TIMESTAMP()*1000
FROM `subscription_account_abilities` saa
JOIN `subscription_accounts` sa ON sa.id = saa.account_id
JOIN `models` m ON LOWER(m.model_id) = LOWER(TRIM(saa.model))
LEFT JOIN `model_subscription_mapping` msm
  ON msm.subscription_account_id = saa.account_id
 AND msm.model_id = m.id
 AND msm.group_name = saa.`group`
WHERE msm.id IS NULL AND TRIM(saa.model) <> '' AND saa.model NOT LIKE '%*%' AND saa.model NOT LIKE '%?%'
GROUP BY saa.account_id, m.id, saa.`group`
ON DUPLICATE KEY UPDATE `model_subscription_mapping`.`enabled` = VALUES(`enabled`);

UPDATE `model_subscription_mapping` msm
JOIN `routing_groups` rg ON rg.`key` = msm.group_name
SET msm.routing_group_id = rg.id
WHERE msm.routing_group_id IS NULL;

SET @moa_151_has_channels := (
  SELECT COUNT(*) FROM information_schema.TABLES
  WHERE TABLE_SCHEMA = DATABASE() AND TABLE_NAME = 'channels'
);
SET @moa_151_has_restrict := (
  SELECT COUNT(*) FROM information_schema.COLUMNS
  WHERE TABLE_SCHEMA = DATABASE() AND TABLE_NAME = 'channels'
    AND COLUMN_NAME = 'restrict_models'
);
SET @moa_151_drop_restrict_sql := IF(
  @moa_151_has_channels > 0 AND @moa_151_has_restrict > 0,
  'ALTER TABLE `channels` DROP COLUMN `restrict_models`',
  'SELECT 1'
);
PREPARE moa_151_drop_restrict_stmt FROM @moa_151_drop_restrict_sql;
EXECUTE moa_151_drop_restrict_stmt;
DEALLOCATE PREPARE moa_151_drop_restrict_stmt;

-- Destructive drops go last so a replay only has the marker INSERT left to do.
DROP TABLE IF EXISTS `abilities`;
DROP TABLE IF EXISTS `subscription_account_abilities`;

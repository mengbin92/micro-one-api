-- Retire the legacy one-api abilities projection. The model registry mappings
-- are now the single source of truth for channel and subscription candidates.
-- Keep exact legacy routes by backfilling them once before dropping old tables.
--
-- The mapping backfill validates the old projection against what the parent
-- resource still advertises: a channel ability is only resurrected when its
-- model is still in that channel's models CSV, and a subscription ability only
-- when its model is still in the account's models CSV and its group is still
-- in the account's group CSV. Models or groups removed from the parent during
-- the deploy-then-migrate window therefore stay removed. Hand-maintained
-- registry mappings are unaffected — they live only in the mapping tables and
-- never in the old projection.
--
-- MySQL DDL commits implicitly, so the transaction opened by the runner gives
-- no atomicity. Guard both DDL statements and every statement that reads the
-- two old tables with information_schema/PREPARE (the 061/067 convention):
-- a run that failed after the DROPs but before schema_migrations was
-- recorded can then replay safely — the guarded segments become no-ops.
SET @moa_151_has_abilities := (
  SELECT COUNT(*) FROM information_schema.TABLES
  WHERE TABLE_SCHEMA = DATABASE() AND TABLE_NAME = 'abilities'
);
SET @moa_151_has_saa := (
  SELECT COUNT(*) FROM information_schema.TABLES
  WHERE TABLE_SCHEMA = DATABASE() AND TABLE_NAME = 'subscription_account_abilities'
);
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

-- Catalog backfill stays global: a catalog row alone does not enable routing,
-- and the model table is shared across parents.
SET @moa_151_models_from_abilities := IF(
  @moa_151_has_abilities > 0,
  'INSERT INTO `models` (`model_id`, `display_name`, `provider`, `model_type`, `status`, `is_public`, `created_at`, `updated_at`) SELECT LOWER(TRIM(a.model)), MIN(TRIM(a.model)), '''', ''chat'', 1, 1, UNIX_TIMESTAMP()*1000, UNIX_TIMESTAMP()*1000 FROM `abilities` a LEFT JOIN `models` m ON LOWER(m.model_id) = LOWER(TRIM(a.model)) WHERE m.id IS NULL AND TRIM(a.model) <> '''' AND a.model NOT LIKE ''%*%'' AND a.model NOT LIKE ''%?%'' GROUP BY LOWER(TRIM(a.model)) ON DUPLICATE KEY UPDATE `updated_at` = `updated_at`',
  'SELECT 1'
);
PREPARE moa_151_stmt FROM @moa_151_models_from_abilities;
EXECUTE moa_151_stmt;
DEALLOCATE PREPARE moa_151_stmt;

SET @moa_151_models_from_saa := IF(
  @moa_151_has_saa > 0,
  'INSERT INTO `models` (`model_id`, `display_name`, `provider`, `model_type`, `status`, `is_public`, `created_at`, `updated_at`) SELECT LOWER(TRIM(saa.model)), MIN(TRIM(saa.model)), '''', ''chat'', 1, 1, UNIX_TIMESTAMP()*1000, UNIX_TIMESTAMP()*1000 FROM `subscription_account_abilities` saa LEFT JOIN `models` m ON LOWER(m.model_id) = LOWER(TRIM(saa.model)) WHERE m.id IS NULL AND TRIM(saa.model) <> '''' AND saa.model NOT LIKE ''%*%'' AND saa.model NOT LIKE ''%?%'' GROUP BY LOWER(TRIM(saa.model)) ON DUPLICATE KEY UPDATE `updated_at` = `updated_at`',
  'SELECT 1'
);
PREPARE moa_151_stmt FROM @moa_151_models_from_saa;
EXECUTE moa_151_stmt;
DEALLOCATE PREPARE moa_151_stmt;

-- Resurrect a channel route only when the channel still lists the model.
SET @moa_151_channel_mapping := IF(
  @moa_151_has_abilities > 0,
  'INSERT INTO `model_channel_mapping` (`channel_id`, `model_id`, `upstream_model_id`, `enabled`, `priority`, `config`, `created_at`, `updated_at`) SELECT a.channel_id, m.id, MAX(TRIM(a.model)), 1, COALESCE(MAX(a.priority), 0), ''{"_managed_by":"channel_models"}'', UNIX_TIMESTAMP()*1000, UNIX_TIMESTAMP()*1000 FROM `abilities` a JOIN `channels` c ON c.id = a.channel_id JOIN `models` m ON LOWER(m.model_id) = LOWER(TRIM(a.model)) LEFT JOIN `model_channel_mapping` mcm ON mcm.channel_id = a.channel_id AND mcm.model_id = m.id WHERE mcm.id IS NULL AND TRIM(a.model) <> '''' AND a.model NOT LIKE ''%*%'' AND a.model NOT LIKE ''%?%'' AND FIND_IN_SET(LOWER(TRIM(a.model)), REGEXP_REPLACE(CONCAT('','', LOWER(c.models), '',''), ''[[:space:]]*,[[:space:]]*'', '','')) > 0 GROUP BY a.channel_id, m.id ON DUPLICATE KEY UPDATE `model_channel_mapping`.`enabled` = VALUES(`enabled`)',
  'SELECT 1'
);
PREPARE moa_151_stmt FROM @moa_151_channel_mapping;
EXECUTE moa_151_stmt;
DEALLOCATE PREPARE moa_151_stmt;

-- Resurrect a subscription route only when the account still lists the model
-- and still belongs to the ability's group.
SET @moa_151_subscription_mapping := IF(
  @moa_151_has_saa > 0,
  'INSERT INTO `model_subscription_mapping` (`subscription_account_id`, `model_id`, `upstream_model_id`, `group_name`, `enabled`, `priority`, `config`, `created_at`, `updated_at`) SELECT saa.account_id, m.id, MAX(TRIM(saa.model)), saa.`group`, 1, COALESCE(MAX(saa.priority), 0), ''{"_managed_by":"subscription_models"}'', UNIX_TIMESTAMP()*1000, UNIX_TIMESTAMP()*1000 FROM `subscription_account_abilities` saa JOIN `subscription_accounts` sa ON sa.id = saa.account_id JOIN `models` m ON LOWER(m.model_id) = LOWER(TRIM(saa.model)) LEFT JOIN `model_subscription_mapping` msm ON msm.subscription_account_id = saa.account_id AND msm.model_id = m.id AND msm.group_name = saa.`group` WHERE msm.id IS NULL AND TRIM(saa.model) <> '''' AND saa.model NOT LIKE ''%*%'' AND saa.model NOT LIKE ''%?%'' AND FIND_IN_SET(LOWER(TRIM(saa.model)), REGEXP_REPLACE(CONCAT('','', LOWER(sa.models), '',''), ''[[:space:]]*,[[:space:]]*'', '','')) > 0 AND FIND_IN_SET(BINARY TRIM(saa.`group`), BINARY REGEXP_REPLACE(CONCAT('','', sa.`group`, '',''), ''[[:space:]]*,[[:space:]]*'', '','')) > 0 GROUP BY saa.account_id, m.id, saa.`group` ON DUPLICATE KEY UPDATE `model_subscription_mapping`.`enabled` = VALUES(`enabled`)',
  'SELECT 1'
);
PREPARE moa_151_stmt FROM @moa_151_subscription_mapping;
EXECUTE moa_151_stmt;
DEALLOCATE PREPARE moa_151_stmt;

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

-- Destructive drops go last. With the source-table guards above, a replay
-- after these drops only re-runs idempotent segments before the marker
-- INSERT records the version.
DROP TABLE IF EXISTS `abilities`;
DROP TABLE IF EXISTS `subscription_account_abilities`;

-- v0.11.0 review L4: remove orphaned model mappings whose parent channel or
-- subscription account no longer exists, then add ON DELETE CASCADE foreign
-- keys so future deletions keep the mapping tables consistent.
--
-- This migration is idempotent: the DELETEs are harmless when there are no
-- orphans, and the FK ADDs are guarded against existing constraints below.

-- Clean up orphaned rows before adding FKs, otherwise the ALTER TABLE would
-- fail on existing deployments that already have dangling mappings.
DELETE FROM `model_channel_mapping`
WHERE `channel_id` NOT IN (SELECT `id` FROM `channels`);

DELETE FROM `model_subscription_mapping`
WHERE `subscription_account_id` NOT IN (SELECT `id` FROM `subscription_accounts`);

-- MySQL has no ADD CONSTRAINT IF NOT EXISTS, so each FK is created only when
-- it is not already present. Add cascade FKs so channel/account deletion
-- automatically removes mappings.
SET @fk_mcm_channel_exists := (
  SELECT COUNT(*) FROM information_schema.TABLE_CONSTRAINTS
  WHERE CONSTRAINT_SCHEMA = DATABASE()
    AND TABLE_NAME = 'model_channel_mapping'
    AND CONSTRAINT_NAME = 'fk_mcm_channel'
);
SET @ddl_add_fk_mcm_channel := IF(
  @fk_mcm_channel_exists = 0,
  'ALTER TABLE `model_channel_mapping` ADD CONSTRAINT `fk_mcm_channel` FOREIGN KEY (`channel_id`) REFERENCES `channels` (`id`) ON DELETE CASCADE',
  'SELECT 1'
);
PREPARE fk_mcm_channel_stmt FROM @ddl_add_fk_mcm_channel;
EXECUTE fk_mcm_channel_stmt;
DEALLOCATE PREPARE fk_mcm_channel_stmt;

SET @fk_msm_account_exists := (
  SELECT COUNT(*) FROM information_schema.TABLE_CONSTRAINTS
  WHERE CONSTRAINT_SCHEMA = DATABASE()
    AND TABLE_NAME = 'model_subscription_mapping'
    AND CONSTRAINT_NAME = 'fk_msm_account'
);
SET @ddl_add_fk_msm_account := IF(
  @fk_msm_account_exists = 0,
  'ALTER TABLE `model_subscription_mapping` ADD CONSTRAINT `fk_msm_account` FOREIGN KEY (`subscription_account_id`) REFERENCES `subscription_accounts` (`id`) ON DELETE CASCADE',
  'SELECT 1'
);
PREPARE fk_msm_account_stmt FROM @ddl_add_fk_msm_account;
EXECUTE fk_msm_account_stmt;
DEALLOCATE PREPARE fk_msm_account_stmt;

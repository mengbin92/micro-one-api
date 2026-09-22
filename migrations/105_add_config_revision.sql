ALTER TABLE `configs`
  ADD COLUMN `revision` bigint NOT NULL DEFAULT 0 AFTER `updated_at`;
CREATE INDEX `idx_configs_revision` ON `configs` (`revision`);

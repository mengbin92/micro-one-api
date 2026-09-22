-- Preserve whether a reconciliation run completed and expose collection errors.
ALTER TABLE `reconciliation_runs`
  ADD COLUMN `status` varchar(16) NOT NULL DEFAULT 'completed' AFTER `created_at`,
  ADD COLUMN `error_message` text NULL AFTER `status`;

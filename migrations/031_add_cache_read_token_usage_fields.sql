ALTER TABLE `logs`
  ADD COLUMN `cache_read_tokens` bigint DEFAULT 0 AFTER `completion_tokens`;

ALTER TABLE `billing_ledgers`
  ADD COLUMN `cache_read_tokens` bigint DEFAULT 0 AFTER `completion_tokens`;

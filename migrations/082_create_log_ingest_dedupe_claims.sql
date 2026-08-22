-- Durable idempotency gate for relay usage-log delivery. A claim is inserted
-- in the same transaction as the log row, so a failed transaction is retryable.

CREATE TABLE IF NOT EXISTS `log_ingest_dedupe_claims` (
  `dedupe_key` varchar(191) NOT NULL,
  `log_id` bigint NOT NULL DEFAULT 0,
  `created_at` bigint NOT NULL,
  PRIMARY KEY (`dedupe_key`),
  KEY `idx_log_ingest_dedupe_log_id` (`log_id`)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COMMENT='Idempotency claims for centralized log ingestion';

INSERT IGNORE INTO `log_ingest_dedupe_claims` (`dedupe_key`, `log_id`, `created_at`)
SELECT CONCAT('consume:', `user_id`, ':', `request_id`), MIN(`id`), MIN(`created_at`)
FROM `logs`
WHERE `level` = 'consume' AND `request_id` <> ''
GROUP BY `user_id`, `request_id`;

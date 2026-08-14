-- Partition-safe idempotency gate for billing ledger writes.
--
-- billing_ledgers may be RANGE-partitioned by created_at. MySQL requires every
-- unique key on a partitioned table to include the partition column, so the
-- ledger table itself cannot retain a global UNIQUE(ledger_dedupe_key).
-- Keep the global claim in this small, non-partitioned table instead. The
-- primary key atomically arbitrates concurrent writers inside the same
-- transaction as the ledger insert; rollback releases the claim.

CREATE TABLE IF NOT EXISTS `billing_ledger_dedupe_claims` (
  `ledger_dedupe_key` varchar(160) NOT NULL,
  `created_at` datetime(3) NOT NULL DEFAULT CURRENT_TIMESTAMP(3),
  PRIMARY KEY (`ledger_dedupe_key`)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COMMENT='Global idempotency claims for partitioned billing ledgers';

-- Seed claims for all existing ledgers before the original unique index is
-- removed by phase3_partitioning.sql. GROUP BY makes this safe even if a
-- manually altered environment already contains duplicate ledger keys.
INSERT IGNORE INTO `billing_ledger_dedupe_claims` (`ledger_dedupe_key`, `created_at`)
SELECT `ledger_dedupe_key`, MIN(COALESCE(`created_at`, CURRENT_TIMESTAMP(3)))
FROM `billing_ledgers`
WHERE `ledger_dedupe_key` <> ''
GROUP BY `ledger_dedupe_key`;

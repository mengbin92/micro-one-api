-- Optional MySQL 8.0 operation: monthly partitioning for
-- oneapi_billing.billing_ledgers.
-- Run only after migration 078_create_billing_ledger_dedupe_claims has been
-- applied and the maintenance window in the runbook is approved.

-- Hard preflight guards. The mysql client executes a piped SQL file
-- statement by statement; a failed CALL therefore aborts the file before any
-- destructive ALTER can run. The version guard catches a database that has
-- not run the normal migrate path, while the coverage guard catches an
-- incomplete claim backfill (including ledgers written during a rolling
-- deploy window).
DROP PROCEDURE IF EXISTS verify_phase3_billing_partition_preflight;
DELIMITER $$
CREATE PROCEDURE verify_phase3_billing_partition_preflight()
BEGIN
    IF NOT EXISTS (
        SELECT 1
        FROM information_schema.TABLES
        WHERE TABLE_SCHEMA = DATABASE()
          AND TABLE_NAME = 'schema_migrations'
    ) THEN
        SIGNAL SQLSTATE '45000' SET
            MYSQL_ERRNO = 45000,
            MESSAGE_TEXT = 'billing partitioning requires the schema_migrations table from the normal migrate path';
    ELSEIF NOT EXISTS (
        SELECT 1
        FROM schema_migrations
        WHERE version = '078_create_billing_ledger_dedupe_claims'
    ) THEN
        SIGNAL SQLSTATE '45000' SET
            MYSQL_ERRNO = 45001,
            MESSAGE_TEXT = 'billing partitioning requires migration 078_create_billing_ledger_dedupe_claims to be applied';
    ELSEIF (
        SELECT COUNT(*)
        FROM billing_ledgers bl
        LEFT JOIN billing_ledger_dedupe_claims c
          ON c.ledger_dedupe_key = bl.ledger_dedupe_key
        WHERE bl.ledger_dedupe_key <> ''
          AND c.ledger_dedupe_key IS NULL
    ) > 0 THEN
        SIGNAL SQLSTATE '45000' SET
            MYSQL_ERRNO = 45002,
            MESSAGE_TEXT = 'billing partitioning requires billing_ledger_dedupe_claims backfill to reach zero missing keys';
    END IF;
END$$
DELIMITER ;
CALL verify_phase3_billing_partition_preflight();
DROP PROCEDURE verify_phase3_billing_partition_preflight;

-- created_at participates in the primary key after partitioning and therefore
-- cannot remain nullable.
UPDATE billing_ledgers
SET created_at = '1970-01-01 00:00:00.000'
WHERE created_at IS NULL;
ALTER TABLE billing_ledgers
    MODIFY COLUMN created_at datetime(3) NOT NULL;

ALTER TABLE billing_ledgers DROP PRIMARY KEY,
    ADD PRIMARY KEY (id, created_at);

-- Non-unique indexes do not need to contain the partition column. Preserve a
-- single-column lookup index for FindByDedupeKey and operational diagnostics;
-- global uniqueness is owned by billing_ledger_dedupe_claims.
ALTER TABLE billing_ledgers DROP INDEX idx_ledger_dedupe_key,
    ADD INDEX idx_ledger_dedupe_key (ledger_dedupe_key);

ALTER TABLE billing_ledgers
PARTITION BY RANGE (TO_DAYS(created_at)) (
    PARTITION p202601 VALUES LESS THAN (TO_DAYS('2026-02-01')),
    PARTITION p202602 VALUES LESS THAN (TO_DAYS('2026-03-01')),
    PARTITION p202603 VALUES LESS THAN (TO_DAYS('2026-04-01')),
    PARTITION p202604 VALUES LESS THAN (TO_DAYS('2026-05-01')),
    PARTITION p202605 VALUES LESS THAN (TO_DAYS('2026-06-01')),
    PARTITION p202606 VALUES LESS THAN (TO_DAYS('2026-07-01')),
    PARTITION p202607 VALUES LESS THAN (TO_DAYS('2026-08-01')),
    PARTITION p202608 VALUES LESS THAN (TO_DAYS('2026-09-01')),
    PARTITION p202609 VALUES LESS THAN (TO_DAYS('2026-10-01')),
    PARTITION p202610 VALUES LESS THAN (TO_DAYS('2026-11-01')),
    PARTITION p202611 VALUES LESS THAN (TO_DAYS('2026-12-01')),
    PARTITION p202612 VALUES LESS THAN (TO_DAYS('2027-01-01')),
    PARTITION p202701 VALUES LESS THAN (TO_DAYS('2027-02-01')),
    PARTITION p202702 VALUES LESS THAN (TO_DAYS('2027-03-01')),
    PARTITION p202703 VALUES LESS THAN (TO_DAYS('2027-04-01')),
    PARTITION p202704 VALUES LESS THAN (TO_DAYS('2027-05-01')),
    PARTITION p202705 VALUES LESS THAN (TO_DAYS('2027-06-01')),
    PARTITION p202706 VALUES LESS THAN (TO_DAYS('2027-07-01')),
    PARTITION p202707 VALUES LESS THAN (TO_DAYS('2027-08-01')),
    PARTITION p202708 VALUES LESS THAN (TO_DAYS('2027-09-01')),
    PARTITION p202709 VALUES LESS THAN (TO_DAYS('2027-10-01')),
    PARTITION p202710 VALUES LESS THAN (TO_DAYS('2027-11-01')),
    PARTITION p202711 VALUES LESS THAN (TO_DAYS('2027-12-01')),
    PARTITION p202712 VALUES LESS THAN (TO_DAYS('2028-01-01')),
    PARTITION pmax VALUES LESS THAN MAXVALUE
);

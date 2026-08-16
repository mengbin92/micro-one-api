-- Optional MySQL 8.0 operation: monthly partitioning for oneapi_log.logs.
-- Run only after the trigger thresholds and maintenance window in
-- docs/runbooks/table-partitioning-runbook.md are approved.
--
-- logs.created_at stores Unix epoch seconds (BIGINT), not DATETIME. The table
-- and the runtime maintenance code therefore use RANGE(created_at) with
-- UNIX_TIMESTAMP month boundaries.

-- Hard preflight guard. Partitioning log data is operationally independent,
-- but it must still be executed as a deliberate manual operation against a
-- database already under migration governance.
DROP PROCEDURE IF EXISTS verify_phase3_logs_partition_preflight;
DELIMITER $$
CREATE PROCEDURE verify_phase3_logs_partition_preflight()
BEGIN
    IF NOT EXISTS (
        SELECT 1
        FROM information_schema.TABLES
        WHERE TABLE_SCHEMA = DATABASE()
          AND TABLE_NAME = 'schema_migrations'
    ) THEN
        SIGNAL SQLSTATE '45000' SET
            MYSQL_ERRNO = 45003,
            MESSAGE_TEXT = 'logs partitioning requires the schema_migrations table from the normal migrate path';
    END IF;
END$$
DELIMITER ;
CALL verify_phase3_logs_partition_preflight();
DROP PROCEDURE verify_phase3_logs_partition_preflight;

-- A partitioned table requires every unique key, including the primary key, to
-- include the partition column. Backfill NULL defensively, make the column
-- non-null, then rebuild the auto-increment primary key with id first.
UPDATE logs SET created_at = 0 WHERE created_at IS NULL;
ALTER TABLE logs MODIFY COLUMN created_at bigint NOT NULL DEFAULT 0;
ALTER TABLE logs DROP PRIMARY KEY,
    ADD PRIMARY KEY (id, created_at);

ALTER TABLE logs
PARTITION BY RANGE (created_at) (
    PARTITION p202601 VALUES LESS THAN (UNIX_TIMESTAMP('2026-02-01 00:00:00')),
    PARTITION p202602 VALUES LESS THAN (UNIX_TIMESTAMP('2026-03-01 00:00:00')),
    PARTITION p202603 VALUES LESS THAN (UNIX_TIMESTAMP('2026-04-01 00:00:00')),
    PARTITION p202604 VALUES LESS THAN (UNIX_TIMESTAMP('2026-05-01 00:00:00')),
    PARTITION p202605 VALUES LESS THAN (UNIX_TIMESTAMP('2026-06-01 00:00:00')),
    PARTITION p202606 VALUES LESS THAN (UNIX_TIMESTAMP('2026-07-01 00:00:00')),
    PARTITION p202607 VALUES LESS THAN (UNIX_TIMESTAMP('2026-08-01 00:00:00')),
    PARTITION p202608 VALUES LESS THAN (UNIX_TIMESTAMP('2026-09-01 00:00:00')),
    PARTITION p202609 VALUES LESS THAN (UNIX_TIMESTAMP('2026-10-01 00:00:00')),
    PARTITION p202610 VALUES LESS THAN (UNIX_TIMESTAMP('2026-11-01 00:00:00')),
    PARTITION p202611 VALUES LESS THAN (UNIX_TIMESTAMP('2026-12-01 00:00:00')),
    PARTITION p202612 VALUES LESS THAN (UNIX_TIMESTAMP('2027-01-01 00:00:00')),
    PARTITION p202701 VALUES LESS THAN (UNIX_TIMESTAMP('2027-02-01 00:00:00')),
    PARTITION p202702 VALUES LESS THAN (UNIX_TIMESTAMP('2027-03-01 00:00:00')),
    PARTITION p202703 VALUES LESS THAN (UNIX_TIMESTAMP('2027-04-01 00:00:00')),
    PARTITION p202704 VALUES LESS THAN (UNIX_TIMESTAMP('2027-05-01 00:00:00')),
    PARTITION p202705 VALUES LESS THAN (UNIX_TIMESTAMP('2027-06-01 00:00:00')),
    PARTITION p202706 VALUES LESS THAN (UNIX_TIMESTAMP('2027-07-01 00:00:00')),
    PARTITION p202707 VALUES LESS THAN (UNIX_TIMESTAMP('2027-08-01 00:00:00')),
    PARTITION p202708 VALUES LESS THAN (UNIX_TIMESTAMP('2027-09-01 00:00:00')),
    PARTITION p202709 VALUES LESS THAN (UNIX_TIMESTAMP('2027-10-01 00:00:00')),
    PARTITION p202710 VALUES LESS THAN (UNIX_TIMESTAMP('2027-11-01 00:00:00')),
    PARTITION p202711 VALUES LESS THAN (UNIX_TIMESTAMP('2027-12-01 00:00:00')),
    PARTITION p202712 VALUES LESS THAN (UNIX_TIMESTAMP('2028-01-01 00:00:00')),
    PARTITION pmax VALUES LESS THAN MAXVALUE
);

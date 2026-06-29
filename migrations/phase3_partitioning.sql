-- Phase 3: Database Table Partitioning
-- Based on ARCHITECTURE_REFACTOR.md §5.3
--
-- This migration adds table partitioning for high-traffic time-series tables.
-- Partitioning improves query performance and enables efficient data cleanup.
-- Version: 1.1 (Updated for 2026-06 deployment)

-- ============================================================================
-- Prerequisites
-- ============================================================================
-- Ensure MySQL 8.0+ is installed for native partitioning support.
-- Verify current MySQL version:
-- SELECT VERSION();

-- ============================================================================
-- logs table - Monthly Partitioning
-- ============================================================================

-- Partition by TO_DAYS(created_at) for efficient range-based queries
-- Historical partitions start from 2026-01, with future partitions pre-created.

ALTER TABLE logs
PARTITION BY RANGE (TO_DAYS(created_at)) (
    -- Historical partitions for 2026
    PARTITION p202601 VALUES LESS THAN (TO_DAYS('2026-02-01')),
    PARTITION p202602 VALUES LESS THAN (TO_DAYS('2026-03-01')),
    PARTITION p202603 VALUES LESS THAN (TO_DAYS('2026-04-01')),
    PARTITION p202604 VALUES LESS THAN (TO_DAYS('2026-05-01')),
    PARTITION p202605 VALUES LESS THAN (TO_DAYS('2026-06-01')),
    -- Current and near-future partitions
    PARTITION p202606 VALUES LESS THAN (TO_DAYS('2026-07-01')),
    PARTITION p202607 VALUES LESS THAN (TO_DAYS('2026-08-01')),
    PARTITION p202608 VALUES LESS THAN (TO_DAYS('2026-09-01')),
    PARTITION p202609 VALUES LESS THAN (TO_DAYS('2026-10-01')),
    PARTITION p202610 VALUES LESS THAN (TO_DAYS('2026-11-01')),
    PARTITION p202611 VALUES LESS THAN (TO_DAYS('2026-12-01')),
    PARTITION p202612 VALUES LESS THAN (TO_DAYS('2027-01-01')),
    -- Future partitions for 2027
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
    -- Catch-all for dates beyond 2027
    PARTITION pmax VALUES LESS THAN MAXVALUE
);

-- ============================================================================
-- billing_ledgers table - Monthly Partitioning
-- ============================================================================

ALTER TABLE billing_ledgers
PARTITION BY RANGE (TO_DAYS(created_at)) (
    -- Historical partitions for 2026
    PARTITION p202601 VALUES LESS THAN (TO_DAYS('2026-02-01')),
    PARTITION p202602 VALUES LESS THAN (TO_DAYS('2026-03-01')),
    PARTITION p202603 VALUES LESS THAN (TO_DAYS('2026-04-01')),
    PARTITION p202604 VALUES LESS THAN (TO_DAYS('2026-05-01')),
    PARTITION p202605 VALUES LESS THAN (TO_DAYS('2026-06-01')),
    -- Current and near-future partitions
    PARTITION p202606 VALUES LESS THAN (TO_DAYS('2026-07-01')),
    PARTITION p202607 VALUES LESS THAN (TO_DAYS('2026-08-01')),
    PARTITION p202608 VALUES LESS THAN (TO_DAYS('2026-09-01')),
    PARTITION p202609 VALUES LESS THAN (TO_DAYS('2026-10-01')),
    PARTITION p202610 VALUES LESS THAN (TO_DAYS('2026-11-01')),
    PARTITION p202611 VALUES LESS THAN (TO_DAYS('2026-12-01')),
    PARTITION p202612 VALUES LESS THAN (TO_DAYS('2027-01-01')),
    -- Future partitions for 2027
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
    -- Catch-all for dates beyond 2027
    PARTITION pmax VALUES LESS THAN MAXVALUE
);

-- ============================================================================
-- Partition Management Procedures
-- ============================================================================

-- Procedure to add a new monthly partition for logs
DELIMITER //

DROP PROCEDURE IF EXISTS AddLogsPartition//

CREATE PROCEDURE AddLogsPartition(IN partition_name VARCHAR(20), IN cutoff_date DATE)
BEGIN
    SET @sql = CONCAT('ALTER TABLE logs REORGANIZE PARTITION pmax INTO (
        PARTITION ', partition_name, ' VALUES LESS THAN (TO_DAYS(''', cutoff_date, ' 00:00:00'')),
        PARTITION pmax VALUES LESS THAN MAXVALUE
    )');
    PREPARE stmt FROM @sql;
    EXECUTE stmt;
    DEALLOCATE PREPARE stmt;
END //

DELIMITER ;

-- Procedure to add a new monthly partition for billing_ledgers
DELIMITER //

DROP PROCEDURE IF EXISTS AddBillingLedgersPartition//

CREATE PROCEDURE AddBillingLedgersPartition(IN partition_name VARCHAR(20), IN cutoff_date DATE)
BEGIN
    SET @sql = CONCAT('ALTER TABLE billing_ledgers REORGANIZE PARTITION pmax INTO (
        PARTITION ', partition_name, ' VALUES LESS THAN (TO_DAYS(''', cutoff_date, ' 00:00:00'')),
        PARTITION pmax VALUES LESS THAN MAXVALUE
    )');
    PREPARE stmt FROM @sql;
    EXECUTE stmt;
    DEALLOCATE PREPARE stmt;
END //

DELIMITER ;

-- Procedure to drop an old partition (for cleanup)
DELIMITER //

DROP PROCEDURE IF EXISTS DropOldPartition//

CREATE PROCEDURE DropOldPartition(IN table_name VARCHAR(50), IN partition_name VARCHAR(20))
BEGIN
    SET @sql = CONCAT('ALTER TABLE ', table_name, ' DROP PARTITION ', partition_name);
    PREPARE stmt FROM @sql;
    EXECUTE stmt;
    DEALLOCATE PREPARE stmt;
END //

DELIMITER ;

-- Procedure to check partition status
DELIMITER //

DROP PROCEDURE IF EXISTS CheckPartitionStatus//

CREATE PROCEDURE CheckPartitionStatus(IN table_name VARCHAR(50))
BEGIN
    SELECT
        PARTITION_NAME,
        PARTITION_ORDINAL_POSITION,
        PARTITION_METHOD,
        PARTITION_EXPRESSION,
        PARTITION_DESCRIPTION,
        TABLE_ROWS,
        AVG_ROW_LENGTH,
        DATA_LENGTH,
        INDEX_LENGTH
    FROM information_schema.PARTITIONS
    WHERE TABLE_SCHEMA = DATABASE()
      AND TABLE_NAME = table_name
    ORDER BY PARTITION_ORDINAL_POSITION;
END //

DELIMITER ;

-- ============================================================================
-- Partition Maintenance Events (Optional)
-- ============================================================================

-- Enable event scheduler
-- SET GLOBAL event_scheduler = ON;

-- Event to automatically add new partitions monthly for logs
DELIMITER //

DROP EVENT IF EXISTS add_logs_partition//

CREATE EVENT IF NOT EXISTS add_logs_partition
ON SCHEDULE EVERY 1 MONTH
STARTS '2026-07-25 00:00:00' -- Run 5 days before month end
DO
BEGIN
    DECLARE next_month DATE;
    DECLARE partition_name VARCHAR(20);
    DECLARE cutoff_date DATE;

    SET next_month = DATE_ADD(CURDATE(), INTERVAL 2 MONTH);
    SET partition_name = CONCAT('p', DATE_FORMAT(next_month, '%Y%m'));
    SET cutoff_date = DATE_FORMAT(DATE_ADD(next_month, INTERVAL 1 MONTH), '%Y-%m-01');

    CALL AddLogsPartition(partition_name, cutoff_date);
END //

DELIMITER ;

-- Event to automatically add new partitions monthly for billing_ledgers
DELIMITER //

DROP EVENT IF EXISTS add_billing_ledgers_partition//

CREATE EVENT IF NOT EXISTS add_billing_ledgers_partition
ON SCHEDULE EVERY 1 MONTH
STARTS '2026-07-25 00:00:00'
DO
BEGIN
    DECLARE next_month DATE;
    DECLARE partition_name VARCHAR(20);
    DECLARE cutoff_date DATE;

    SET next_month = DATE_ADD(CURDATE(), INTERVAL 2 MONTH);
    SET partition_name = CONCAT('p', DATE_FORMAT(next_month, '%Y%m'));
    SET cutoff_date = DATE_FORMAT(DATE_ADD(next_month, INTERVAL 1 MONTH), '%Y-%m-01');

    CALL AddBillingLedgersPartition(partition_name, cutoff_date);
END //

DELIMITER ;

-- ============================================================================
-- Verification Queries
-- ============================================================================

-- Check partition information for logs
-- CALL CheckPartitionStatus('logs');

-- Check partition information for billing_ledgers
-- CALL CheckPartitionStatus('billing_ledgers');

-- Check if event scheduler is running
-- SHOW PROCESSLIST WHERE User = 'event_scheduler';

-- ============================================================================
-- Cleanup Example (Manual Execution Required)
-- ============================================================================

-- To drop old partitions (e.g., drop January 2026 after 6-month retention):
-- CALL DropOldPartition('logs', 'p202601');
-- CALL DropOldPartition('billing_ledgers', 'p202601');

-- ============================================================================
-- Rollback Commands (if needed)
-- ============================================================================

-- WARNING: Rolling back partitioning requires recreating the table
-- without partitions and migrating data back.

-- Disable automatic partition management
-- DROP EVENT IF EXISTS add_logs_partition;
-- DROP EVENT IF EXISTS add_billing_ledgers_partition;

-- Drop stored procedures
-- DROP PROCEDURE IF EXISTS AddLogsPartition;
-- DROP PROCEDURE IF EXISTS AddBillingLedgersPartition;
-- DROP PROCEDURE IF EXISTS DropOldPartition;
-- DROP PROCEDURE IF EXISTS CheckPartitionStatus;

-- ============================================================================
-- Notes for Migration
-- ============================================================================

-- 1. IMPORTANT: Partitioning requires removing any existing partitions first
--    if the table was previously partitioned with a different expression.

-- 2. For tables with existing data, consider using pt-online-schema-change
--    or gh-ost to avoid locking the table during migration.

-- 3. Partitioning keys must be part of all unique keys (including primary key).
--    If created_at is not in the primary key, you may need to drop and recreate
--    the primary key to include created_at.

-- 4. Query optimization: Always include created_at in WHERE clauses for
--    partitioned tables to enable partition pruning.

-- 5. Monitoring: Set up alerts for partition operations to ensure partitions
--    are being added and dropped as expected.

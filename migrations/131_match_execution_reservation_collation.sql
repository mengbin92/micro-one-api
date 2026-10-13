-- Existing installations use different utf8mb4 reservation collations.
-- Match the reference column to the authoritative reservation key before the expiry IN query.
SET @execution_reservation_collation = (SELECT COLLATION_NAME FROM information_schema.COLUMNS WHERE TABLE_SCHEMA = DATABASE() AND TABLE_NAME = 'billing_reservations' AND COLUMN_NAME = 'reservation_id');
SET @execution_reservation_charset = (SELECT CHARACTER_SET_NAME FROM information_schema.COLUMNS WHERE TABLE_SCHEMA = DATABASE() AND TABLE_NAME = 'billing_reservations' AND COLUMN_NAME = 'reservation_id');
SET @execution_reservation_ddl = CONCAT('ALTER TABLE billing_execution_intents MODIFY COLUMN reservation_id VARCHAR(128) CHARACTER SET ', @execution_reservation_charset, ' COLLATE ', @execution_reservation_collation, ' NOT NULL');
PREPARE execution_reservation_stmt FROM @execution_reservation_ddl;
EXECUTE execution_reservation_stmt;
DEALLOCATE PREPARE execution_reservation_stmt;

-- Restore the wallet's canonical fixed-point amount terminology after the
-- subscription-priority flow introduced balance_amount_quota. Keep the legacy
-- column for rollback compatibility; the application dual-writes both fields.

ALTER TABLE `billing_reservations`
  ADD COLUMN `balance_amount` BIGINT NOT NULL DEFAULT 0 AFTER `balance_amount_quota`;

UPDATE `billing_reservations`
SET `balance_amount` = `balance_amount_quota`
WHERE `balance_amount` = 0 AND `balance_amount_quota` <> 0;

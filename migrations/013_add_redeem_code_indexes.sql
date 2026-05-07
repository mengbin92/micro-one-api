-- Add indexes for redeem codes to improve search performance
ALTER TABLE `billing_redeem_codes`
ADD INDEX `idx_name` (`name`),
ADD INDEX `idx_status_created_at` (`status`, `created_at`);
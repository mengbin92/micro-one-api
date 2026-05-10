-- Add One API compatible user invitation fields.

ALTER TABLE `users` ADD COLUMN `aff_code` varchar(32) DEFAULT '';
ALTER TABLE `users` ADD COLUMN `inviter_id` bigint DEFAULT 0;

CREATE INDEX `idx_users_aff_code` ON `users` (`aff_code`);
CREATE INDEX `idx_users_inviter_id` ON `users` (`inviter_id`);

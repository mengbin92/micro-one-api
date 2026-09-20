-- Track when a notify worker claimed a row so startup recovery only resets
-- abandoned work, not notifications actively being sent by another replica.
ALTER TABLE `notifications`
  ADD COLUMN `processing_at` bigint NOT NULL DEFAULT 0 AFTER `last_error`;
ALTER TABLE `notifications`
  ADD KEY `idx_notifications_processing` (`status`, `processing_at`);

ALTER TABLE notifications ADD COLUMN IF NOT EXISTS processing_at BIGINT NOT NULL DEFAULT 0;
CREATE INDEX IF NOT EXISTS idx_notifications_processing ON notifications(status, processing_at);

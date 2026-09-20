ALTER TABLE notifications ADD COLUMN processing_at INTEGER NOT NULL DEFAULT 0;
CREATE INDEX IF NOT EXISTS idx_notifications_processing ON notifications(status, processing_at);

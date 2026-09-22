ALTER TABLE reconciliation_runs ADD COLUMN IF NOT EXISTS status varchar(16) NOT NULL DEFAULT 'completed';
ALTER TABLE reconciliation_runs ADD COLUMN IF NOT EXISTS error_message text;

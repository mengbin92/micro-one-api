ALTER TABLE reconciliation_runs ADD COLUMN status TEXT NOT NULL DEFAULT 'completed';
ALTER TABLE reconciliation_runs ADD COLUMN error_message TEXT;

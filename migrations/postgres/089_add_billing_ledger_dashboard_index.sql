-- User dashboard filters consume ledgers by user and type, then groups by
-- created_at. Keep the selective equality columns ahead of the time range.
CREATE INDEX IF NOT EXISTS idx_billing_ledgers_user_type_created
  ON billing_ledgers (user_id, type, created_at);

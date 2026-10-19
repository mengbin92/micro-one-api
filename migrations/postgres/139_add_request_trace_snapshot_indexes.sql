-- Cover both authorized snapshot orders before PostgreSQL ANALYZE.
-- The user prefix must precede id for a selective owner lookup; the separate
-- source/id index also streams snapshots that span multiple owners.
CREATE INDEX idx_log_trace_snapshot ON logs (source, id);
CREATE INDEX idx_log_trace_user_snapshot ON logs (source, user_id, id);

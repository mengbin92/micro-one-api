-- Audit reads are newest-first and may filter by operation prefix.
CREATE INDEX resource_write_audits_time ON resource_write_audits (occurred_at, event_id);
CREATE INDEX resource_write_audits_operation_time ON resource_write_audits (operation, occurred_at, event_id);

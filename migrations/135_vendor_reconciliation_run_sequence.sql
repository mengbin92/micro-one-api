-- Preserve the previous latest-run view for historical second-resolution rows;
-- new replays allocate a monotonic sequence while locking their report row.
ALTER TABLE billing_vendor_reconciliation_runs ADD COLUMN sequence BIGINT NOT NULL DEFAULT 0;
UPDATE billing_vendor_reconciliation_runs AS target
JOIN (
  SELECT id, ROW_NUMBER() OVER (PARTITION BY report_id ORDER BY created_at, id) AS run_sequence
  FROM billing_vendor_reconciliation_runs
) AS ranked ON ranked.id = target.id
SET target.sequence = ranked.run_sequence;
CREATE UNIQUE INDEX uk_vendor_run_sequence ON billing_vendor_reconciliation_runs (report_id, sequence);

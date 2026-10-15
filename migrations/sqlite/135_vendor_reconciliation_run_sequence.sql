-- Keep the historical latest-run view; serialize future replays by report row.
ALTER TABLE billing_vendor_reconciliation_runs ADD COLUMN sequence BIGINT NOT NULL DEFAULT 0;
WITH ranked AS (
  SELECT id, ROW_NUMBER() OVER (PARTITION BY report_id ORDER BY created_at, id) AS run_sequence
  FROM billing_vendor_reconciliation_runs
)
UPDATE billing_vendor_reconciliation_runs
SET sequence = (SELECT run_sequence FROM ranked WHERE ranked.id = billing_vendor_reconciliation_runs.id);
CREATE UNIQUE INDEX uk_vendor_run_sequence ON billing_vendor_reconciliation_runs (report_id, sequence);

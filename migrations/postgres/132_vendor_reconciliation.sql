-- P2 immutable external supplier reports and replay results; no ledger mutation.
CREATE TABLE IF NOT EXISTS billing_vendor_reports (
  id VARCHAR(64) PRIMARY KEY,
  root_scope_hash VARCHAR(64) NULL,
  scope_hash VARCHAR(64) NOT NULL,
  version VARCHAR(128) NOT NULL,
  supersedes VARCHAR(64) NULL,
  payload TEXT NOT NULL,
  imported_at BIGINT NOT NULL,
  CONSTRAINT uk_vendor_scope_root UNIQUE (root_scope_hash),
  CONSTRAINT uk_vendor_version UNIQUE (scope_hash, version),
  CONSTRAINT uk_vendor_correction UNIQUE (supersedes)
);
CREATE TABLE IF NOT EXISTS billing_vendor_reconciliation_runs (
  id VARCHAR(64) PRIMARY KEY,
  report_id VARCHAR(64) NOT NULL,
  payload TEXT NOT NULL,
  created_at BIGINT NOT NULL,
  FOREIGN KEY (report_id) REFERENCES billing_vendor_reports(id)
);
CREATE INDEX idx_vendor_run_report ON billing_vendor_reconciliation_runs (report_id, created_at);

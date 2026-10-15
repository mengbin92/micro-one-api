-- P2 immutable external supplier reports and replay results; no ledger mutation.
CREATE TABLE IF NOT EXISTS billing_vendor_reports (
  id VARCHAR(64) COLLATE utf8mb4_bin PRIMARY KEY,
  root_scope_hash VARCHAR(64) COLLATE utf8mb4_bin NULL,
  scope_hash VARCHAR(64) COLLATE utf8mb4_bin NOT NULL,
  version VARCHAR(128) COLLATE utf8mb4_bin NOT NULL,
  supersedes VARCHAR(64) COLLATE utf8mb4_bin NULL,
  payload LONGTEXT NOT NULL,
  imported_at BIGINT NOT NULL,
  CONSTRAINT uk_vendor_scope_root UNIQUE (root_scope_hash),
  CONSTRAINT uk_vendor_version UNIQUE (scope_hash, version),
  CONSTRAINT uk_vendor_correction UNIQUE (supersedes)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4;
CREATE TABLE IF NOT EXISTS billing_vendor_reconciliation_runs (
  id VARCHAR(64) COLLATE utf8mb4_bin PRIMARY KEY,
  report_id VARCHAR(64) COLLATE utf8mb4_bin NOT NULL,
  payload LONGTEXT NOT NULL,
  created_at BIGINT NOT NULL,
  FOREIGN KEY (report_id) REFERENCES billing_vendor_reports(id)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4;
CREATE INDEX idx_vendor_run_report ON billing_vendor_reconciliation_runs (report_id, created_at);

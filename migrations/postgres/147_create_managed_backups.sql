-- Admin owns managed backup policy, task receipts and append-only audit.
-- The worker intentionally supports MySQL only; mirrors keep admin/test
-- migrations portable and do not claim PostgreSQL/SQLite backup support.
CREATE TABLE IF NOT EXISTS backup_policies (
 id INTEGER PRIMARY KEY,
 revision BIGINT NOT NULL,
 document TEXT NOT NULL
);
INSERT INTO backup_policies (id, revision, document)
SELECT 1, 1, '{"Revision":1,"Enabled":false,"IntervalHours":24,"Retain":7,"RPOHours":24,"RTOMinutes":60}'
WHERE NOT EXISTS (SELECT 1 FROM backup_policies WHERE id = 1);
CREATE TABLE IF NOT EXISTS backup_jobs (
 id VARCHAR(80) PRIMARY KEY,
 status VARCHAR(24) NOT NULL,
 trigger_kind VARCHAR(24) NOT NULL,
 actor VARCHAR(80) NOT NULL,
 reason VARCHAR(240) NOT NULL,
 created_at BIGINT NOT NULL,
 started_at BIGINT NOT NULL DEFAULT 0,
 finished_at BIGINT NOT NULL DEFAULT 0,
 storage_id VARCHAR(64) NOT NULL DEFAULT '',
 object_key VARCHAR(512) NOT NULL DEFAULT '',
 sha256 VARCHAR(64) NOT NULL DEFAULT '',
 bytes BIGINT NOT NULL DEFAULT 0,
 error_code VARCHAR(80) NOT NULL DEFAULT '',
 policy TEXT NOT NULL
);
CREATE INDEX idx_backup_jobs_status_created ON backup_jobs (status, created_at);
CREATE TABLE IF NOT EXISTS backup_events (
 id VARCHAR(36) PRIMARY KEY,
 job_id VARCHAR(80) NOT NULL,
 action VARCHAR(32) NOT NULL,
 actor VARCHAR(80) NOT NULL,
 reason VARCHAR(240) NOT NULL,
 at BIGINT NOT NULL
);
CREATE INDEX idx_backup_events_at ON backup_events (at);

CREATE TABLE IF NOT EXISTS backup_worker_state (
 id INTEGER PRIMARY KEY,
 last_seen BIGINT NOT NULL DEFAULT 0,
 phase VARCHAR(24) NOT NULL DEFAULT 'offline',
 error_code VARCHAR(80) NOT NULL DEFAULT '',
 timeout_minutes INTEGER NOT NULL DEFAULT 0
);
INSERT INTO backup_worker_state (id, last_seen, phase, error_code, timeout_minutes)
SELECT 1, 0, 'offline', '', 0
WHERE NOT EXISTS (SELECT 1 FROM backup_worker_state WHERE id = 1);

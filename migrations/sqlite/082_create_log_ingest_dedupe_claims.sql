-- See migrations/082_create_log_ingest_dedupe_claims.sql.

CREATE TABLE IF NOT EXISTS log_ingest_dedupe_claims (
  dedupe_key TEXT PRIMARY KEY,
  log_id INTEGER NOT NULL DEFAULT 0,
  created_at INTEGER NOT NULL
);

CREATE INDEX IF NOT EXISTS idx_log_ingest_dedupe_log_id
  ON log_ingest_dedupe_claims (log_id);

INSERT OR IGNORE INTO log_ingest_dedupe_claims (dedupe_key, log_id, created_at)
SELECT 'consume:' || user_id || ':' || request_id, MIN(id), MIN(created_at)
FROM logs
WHERE level = 'consume' AND request_id <> ''
GROUP BY user_id, request_id;

-- See migrations/082_create_log_ingest_dedupe_claims.sql.

CREATE TABLE IF NOT EXISTS log_ingest_dedupe_claims (
  dedupe_key varchar(191) PRIMARY KEY,
  log_id bigint NOT NULL DEFAULT 0,
  created_at bigint NOT NULL
);

CREATE INDEX IF NOT EXISTS idx_log_ingest_dedupe_log_id
  ON log_ingest_dedupe_claims (log_id);

INSERT INTO log_ingest_dedupe_claims (dedupe_key, log_id, created_at)
SELECT 'consume:' || user_id::text || ':' || request_id, MIN(id), MIN(created_at)
FROM logs
WHERE level = 'consume' AND request_id <> ''
GROUP BY user_id, request_id
ON CONFLICT (dedupe_key) DO NOTHING;

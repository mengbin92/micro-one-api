-- Partition-safe idempotency gate for billing ledger writes.
CREATE TABLE IF NOT EXISTS billing_ledger_dedupe_claims (
  ledger_dedupe_key TEXT PRIMARY KEY,
  created_at INTEGER NOT NULL DEFAULT (unixepoch())
);

INSERT OR IGNORE INTO billing_ledger_dedupe_claims (ledger_dedupe_key, created_at)
SELECT ledger_dedupe_key, MIN(COALESCE(created_at, unixepoch()))
FROM billing_ledgers
WHERE ledger_dedupe_key <> ''
GROUP BY ledger_dedupe_key;

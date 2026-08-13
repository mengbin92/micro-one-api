-- Partition-safe idempotency gate for billing ledger writes.
CREATE TABLE IF NOT EXISTS billing_ledger_dedupe_claims (
  ledger_dedupe_key TEXT PRIMARY KEY,
  created_at TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP
);

INSERT INTO billing_ledger_dedupe_claims (ledger_dedupe_key, created_at)
SELECT ledger_dedupe_key, MIN(COALESCE(created_at, CURRENT_TIMESTAMP))
FROM billing_ledgers
WHERE ledger_dedupe_key <> ''
GROUP BY ledger_dedupe_key
ON CONFLICT (ledger_dedupe_key) DO NOTHING;

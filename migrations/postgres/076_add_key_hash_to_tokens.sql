-- identity-L6: store an HMAC-SHA256 hash of the access-token key (key_hash).
-- See migrations/076_add_key_hash_to_tokens.sql for full rationale.
-- Backfill is performed in-process by the app (see data.go BackfillTokenHashes)
-- because the HMAC secret lives in app config, not the DB.
-- Additive (column + index); rollback = DROP COLUMN key_hash.

ALTER TABLE tokens
  ADD COLUMN IF NOT EXISTS key_hash TEXT NOT NULL DEFAULT '';

CREATE INDEX IF NOT EXISTS idx_tokens_key_hash ON tokens(key_hash);

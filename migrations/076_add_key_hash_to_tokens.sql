-- identity-L6: store an HMAC-SHA256 hash of the access-token key (key_hash)
-- instead of relying on plaintext equality lookup, so a DB leak cannot
-- authenticate as any user.
--
-- Previously `FindTokenByKey` did `WHERE key = ?` against the plaintext column
-- (indexed by idx_key). A single DB dump exposed every live API key. This adds
-- a `key_hash` column: the app computes HMAC-SHA256(plaintext, secret) on
-- create/validate and looks up by the hash. The plaintext `key` column is
-- downgraded to hold only a short display prefix (first 8 + last 4 chars),
-- which preserves the masked-key UI without revealing enough to authenticate.
--
-- Backfill: existing plaintext keys cannot be hashed inside SQL (the HMAC
-- secret lives in the app config/env, not the DB). The app performs the
-- migration in-process at startup: rows with empty key_hash are re-hashed
-- from their still-plaintext key, then the key column is truncated to the
-- display prefix. See app/identity/internal/data/data.go BackfillTokenHashes.
--
-- Additive (column + index); rollback = DROP COLUMN key_hash.

ALTER TABLE `tokens`
  ADD COLUMN `key_hash` varchar(64) NOT NULL DEFAULT '' AFTER `key`;

-- Index on key_hash makes the ValidateToken hot path an O(1) indexed equality
-- seek. Non-unique so the in-process backfill (rows with empty key_hash) does
-- not collide on the default empty-string value before rows are re-hashed; the
-- app BackfillTokenHashes guarantees hash uniqueness at write time.
CREATE INDEX `idx_tokens_key_hash` ON `tokens` (`key_hash`);

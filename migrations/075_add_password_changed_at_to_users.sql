-- v0.11.x identity-M6: password-change epoch for session revocation.
--
-- Previously a stolen/leaked session JWT stayed valid for its full TTL
-- (USER_JWT_TOKEN_DURATION, default 24h) even after the user changed their
-- password or was force-signed-out, because logout was a no-op and there
-- was no server-side session store or revocation list. This adds a
-- `password_changed_at` epoch column: it is stamped into the session JWT as
-- `pwd_epoch` at signing time, and ValidateSessionToken rejects any token
-- whose embedded epoch predates the stored value. A password change / reset /
-- forced logout bumps the stored epoch, revoking every prior session without a
-- revocation list. The column defaults to 0 (no constraint) so existing
-- sessions keep working after migration.
--
-- The epoch is stored in Unix MILLISECONDS (not seconds) so that a password
-- change and session signing occurring within the same second still produce
-- distinct epoch values, guaranteeing reliable revocation (review L1).
-- bigint comfortably holds millisecond timestamps. Additive only;
-- rollback = DROP COLUMN.

ALTER TABLE `users`
  ADD COLUMN `password_changed_at` bigint NOT NULL DEFAULT 0 AFTER `password_hash`;

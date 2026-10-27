-- Identity-owned MFA state. Secrets are AES-GCM ciphertext; recovery codes are hashes.
CREATE TABLE identity_mfa (
  user_id BIGINT PRIMARY KEY,
  secret TEXT NOT NULL,
  pending_secret TEXT NOT NULL,
  pending_session VARCHAR(128) NOT NULL,
  pending_epoch BIGINT NOT NULL DEFAULT 0,
  pending_until BIGINT NOT NULL DEFAULT 0,
  recovery_hashes TEXT NOT NULL,
  revision BIGINT NOT NULL DEFAULT 1,
  last_step BIGINT NOT NULL DEFAULT -1,
  failures INTEGER NOT NULL DEFAULT 0,
  locked_until BIGINT NOT NULL DEFAULT 0,
  FOREIGN KEY (user_id) REFERENCES users(id) ON DELETE CASCADE
);

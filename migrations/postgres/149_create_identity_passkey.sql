-- Identity-owned WebAuthn (passkey) credentials. Public keys are AES-GCM
-- ciphertext under the MFA encryption key; attestation/transports are metadata.
CREATE TABLE identity_passkey (
  credential_id BYTEA PRIMARY KEY,
  user_id BIGINT NOT NULL,
  public_key TEXT NOT NULL,
  attestation TEXT NOT NULL,
  transport TEXT NOT NULL,
  name VARCHAR(128) NOT NULL,
  sign_count BIGINT NOT NULL DEFAULT 0,
  created_at BIGINT NOT NULL DEFAULT 0,
  last_used_at BIGINT NOT NULL DEFAULT 0,
  FOREIGN KEY (user_id) REFERENCES users(id) ON DELETE CASCADE
);
CREATE INDEX idx_identity_passkey_user ON identity_passkey(user_id);

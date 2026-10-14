-- P2 channel-owned immutable finite contracts and fenced protocol incidents.
CREATE TABLE IF NOT EXISTS channel_upstream_contract_versions (id VARCHAR(64) COLLATE utf8mb4_bin PRIMARY KEY, scope_key VARCHAR(64) NOT NULL, document LONGTEXT NOT NULL, created_at BIGINT NOT NULL);
CREATE INDEX idx_contract_scope ON channel_upstream_contract_versions(scope_key,created_at);
CREATE TABLE IF NOT EXISTS channel_upstream_contract_bindings (
 id VARCHAR(64) COLLATE utf8mb4_bin PRIMARY KEY, source_kind VARCHAR(32) NOT NULL, source_id BIGINT NOT NULL, upstream_model_id VARCHAR(255) NOT NULL, protocol VARCHAR(64) NOT NULL,
 current_revision VARCHAR(64) COLLATE utf8mb4_bin NOT NULL DEFAULT '', previous_revision VARCHAR(64) COLLATE utf8mb4_bin NOT NULL DEFAULT '', revision BIGINT NOT NULL DEFAULT 0, incident_revision BIGINT NOT NULL DEFAULT 0,
 status VARCHAR(32) NOT NULL, gray_percent INTEGER NOT NULL DEFAULT 0, started_at BIGINT NOT NULL DEFAULT 0, complete_attempts BIGINT NOT NULL DEFAULT 0, failures BIGINT NOT NULL DEFAULT 0,
 approved_percent INTEGER NOT NULL DEFAULT 0, previous_percent INTEGER NOT NULL DEFAULT 0, cooldown_until BIGINT NOT NULL DEFAULT 0, switch_window BIGINT NOT NULL DEFAULT 0, switches BIGINT NOT NULL DEFAULT 0, auto_revision VARCHAR(64) COLLATE utf8mb4_bin NOT NULL DEFAULT '', auto_until BIGINT NOT NULL DEFAULT 0
);
CREATE INDEX idx_contract_source ON channel_upstream_contract_bindings(source_kind,source_id);
CREATE TABLE IF NOT EXISTS channel_upstream_contract_probes (id VARCHAR(64) COLLATE utf8mb4_bin PRIMARY KEY, attempt_id VARCHAR(128) NOT NULL UNIQUE, scope_key VARCHAR(64) NOT NULL, revision VARCHAR(64) COLLATE utf8mb4_bin NOT NULL, source_revision VARCHAR(64) COLLATE utf8mb4_bin NOT NULL, binding_revision BIGINT NOT NULL, incident_revision BIGINT NOT NULL, expires_at BIGINT NOT NULL, result_digest VARCHAR(64) NOT NULL DEFAULT '', passed BOOLEAN NOT NULL DEFAULT FALSE, completed_at BIGINT NOT NULL DEFAULT 0);
CREATE INDEX idx_contract_probe ON channel_upstream_contract_probes(scope_key,revision,incident_revision,binding_revision,completed_at);
CREATE TABLE IF NOT EXISTS channel_upstream_contract_results (id VARCHAR(64) COLLATE utf8mb4_bin PRIMARY KEY, digest VARCHAR(64) NOT NULL, created_at BIGINT NOT NULL);
CREATE TABLE IF NOT EXISTS channel_upstream_contract_audits (id VARCHAR(64) COLLATE utf8mb4_bin PRIMARY KEY, scope_key VARCHAR(64) NOT NULL, action VARCHAR(64) NOT NULL, before_revision TEXT NOT NULL, after_revision TEXT NOT NULL, reason TEXT NOT NULL, actor_id BIGINT NOT NULL, binding_revision BIGINT NOT NULL, incident_revision BIGINT NOT NULL, created_at BIGINT NOT NULL, evidence_id TEXT NOT NULL);
ALTER TABLE model_channel_mapping ADD COLUMN contract_refs JSON NOT NULL DEFAULT (JSON_OBJECT());
ALTER TABLE model_subscription_mapping ADD COLUMN contract_refs JSON NOT NULL DEFAULT (JSON_OBJECT());

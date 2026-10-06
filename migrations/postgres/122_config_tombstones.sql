ALTER TABLE configs ADD COLUMN deleted INTEGER NOT NULL DEFAULT 0;
CREATE INDEX idx_configs_deleted_namespace ON configs (deleted, namespace);
CREATE TABLE config_key_locks (
 namespace VARCHAR(64) NOT NULL,
 config_key VARCHAR(128) NOT NULL,
 PRIMARY KEY (namespace, config_key)
);

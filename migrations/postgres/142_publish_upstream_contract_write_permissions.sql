-- Maintainer-approved publication of the existing upstream-contract write owners.
-- Adds only channel.upstream_contract.create/publish/quarantine to the builtin
-- root release for the v0.36.4 channel-11 contract pilot.
-- Preserves user assignments, other operations, existing deny/narrow grants,
-- disabled/archived governance and receipt replay; does not change financial
-- data, user charging, or any binding/quarantine state.
-- The policy lock serializes this increment with ordinary IAM management writes.
UPDATE iam_policy_state SET policy_revision = policy_revision WHERE id = 1;

-- TEMPORARY DDL does not implicitly commit on MySQL. A failed CHECK leaves all
-- persistent data unchanged. IF NOT EXISTS + DELETE also recover a reused MySQL
-- connection after a prior failed preflight left its temporary table behind.
CREATE TEMPORARY TABLE IF NOT EXISTS iam_upstream_contract_release_guard (
  ready INTEGER NOT NULL,
  apply_release INTEGER NOT NULL,
  root_grants_added INTEGER NOT NULL,
  before_data TEXT NOT NULL,
  CONSTRAINT iam_upstream_contract_catalog_and_root_must_be_publishable CHECK (ready = 1)
);
DELETE FROM iam_upstream_contract_release_guard;
INSERT INTO iam_upstream_contract_release_guard (ready, apply_release, root_grants_added, before_data)
SELECT
  CASE WHEN EXISTS (SELECT 1 FROM iam_audit_events WHERE event_id = 'migration:142:upstream-contract-write' AND actor_service_id = 'schema-migrate' AND action = 'iam.catalog.release.upstream_contract_write' AND result = 'success') OR (
    (SELECT COUNT(*) FROM iam_policy_state WHERE id = 1) = 1
    AND (SELECT COUNT(*) FROM iam_permissions p JOIN iam_resources r ON r.id = p.resource_id
      WHERE p.status IN ('draft', 'enabled')
      AND p.code IN ('channel.upstream_contract.create', 'channel.upstream_contract.publish', 'channel.upstream_contract.quarantine')
      AND r.code = 'channel.upstream_contract' AND r.owner_service = 'channel' AND r.enabled = 1) = 3
    AND EXISTS (SELECT 1 FROM iam_roles WHERE context_type = 'platform' AND organization_id = 0 AND context_key = 'platform' AND code = 'root' AND builtin = 1 AND status = 'enabled')
  ) THEN 1 ELSE 0 END,
  CASE WHEN EXISTS (SELECT 1 FROM iam_audit_events WHERE event_id = 'migration:142:upstream-contract-write' AND actor_service_id = 'schema-migrate' AND action = 'iam.catalog.release.upstream_contract_write' AND result = 'success') THEN 0 ELSE 1 END,
  (SELECT COUNT(*) FROM iam_roles r CROSS JOIN iam_permissions p
    WHERE r.context_key = 'platform' AND r.code = 'root' AND r.builtin = 1 AND r.status = 'enabled'
    AND p.code IN ('channel.upstream_contract.create', 'channel.upstream_contract.publish', 'channel.upstream_contract.quarantine')
    AND NOT EXISTS (SELECT 1 FROM iam_role_permissions g WHERE g.role_id = r.id AND g.permission_id = p.id)),
  JSON_BUILD_OBJECT(
    'policy_revision', (SELECT policy_revision FROM iam_policy_state WHERE id = 1),
    'catalog_revision', (SELECT catalog_revision FROM iam_policy_state WHERE id = 1),
    'authorization_mode', (SELECT authorization_mode FROM iam_policy_state WHERE id = 1),
    'root_role_revision', (SELECT revision FROM iam_roles WHERE context_key = 'platform' AND code = 'root' AND builtin = 1),
    'upstream_contract_write_statuses', (SELECT JSON_OBJECT_AGG(code, status) FROM iam_permissions WHERE code IN ('channel.upstream_contract.create', 'channel.upstream_contract.publish', 'channel.upstream_contract.quarantine')),
    'root_upstream_contract_grants', (SELECT COUNT(*) FROM iam_role_permissions g JOIN iam_roles r ON r.id = g.role_id JOIN iam_permissions p ON p.id = g.permission_id WHERE r.context_key = 'platform' AND r.code = 'root' AND r.builtin = 1 AND p.code IN ('channel.upstream_contract.create', 'channel.upstream_contract.publish', 'channel.upstream_contract.quarantine'))
  );

UPDATE iam_permissions SET status = 'enabled', binding_state = 'bound', revision = revision + 1
WHERE code IN ('channel.upstream_contract.create', 'channel.upstream_contract.publish', 'channel.upstream_contract.quarantine') AND (status <> 'enabled' OR binding_state <> 'bound')
AND (SELECT apply_release FROM iam_upstream_contract_release_guard) = 1;

UPDATE iam_roles SET revision = revision + 1
WHERE context_key = 'platform' AND code = 'root' AND builtin = 1 AND status = 'enabled'
AND (SELECT apply_release * root_grants_added FROM iam_upstream_contract_release_guard) > 0;

-- Any existing relation wins, including an explicit deny or a narrower allow.
INSERT INTO iam_role_permissions (context_key, role_id, permission_id, effect, scope_descriptor, revision)
SELECT 'platform', r.id, p.id, 'allow', '{"clauses":[{"all":true}]}', r.revision
FROM iam_roles r CROSS JOIN iam_permissions p
WHERE r.context_key = 'platform' AND r.code = 'root' AND r.builtin = 1 AND r.status = 'enabled'
AND p.code IN ('channel.upstream_contract.create', 'channel.upstream_contract.publish', 'channel.upstream_contract.quarantine')
AND (SELECT apply_release FROM iam_upstream_contract_release_guard) = 1
AND NOT EXISTS (SELECT 1 FROM iam_role_permissions g WHERE g.role_id = r.id AND g.permission_id = p.id);

-- Policy/catalog revisions invalidate existing authorization snapshots without
-- changing role assignments, active sessions, or any user's own revision.
UPDATE iam_policy_state SET policy_revision = policy_revision + 1, catalog_revision = catalog_revision + 1
WHERE id = 1 AND (SELECT apply_release FROM iam_upstream_contract_release_guard) = 1;

INSERT INTO iam_audit_events (event_id, actor_service_id, context_key, target_context_key, action, target,
  before_data, after_data, diff, result, decision_versions, request_id, occurred_at, reason)
SELECT 'migration:142:upstream-contract-write', 'schema-migrate', 'platform', 'platform',
  'iam.catalog.release.upstream_contract_write', 'platform/root:channel.upstream_contract.create,channel.upstream_contract.publish,channel.upstream_contract.quarantine',
  before_data, JSON_BUILD_OBJECT(
    'policy_revision', (SELECT policy_revision FROM iam_policy_state WHERE id = 1),
    'catalog_revision', (SELECT catalog_revision FROM iam_policy_state WHERE id = 1),
    'authorization_mode', (SELECT authorization_mode FROM iam_policy_state WHERE id = 1),
    'root_role_revision', (SELECT revision FROM iam_roles WHERE context_key = 'platform' AND code = 'root' AND builtin = 1),
    'upstream_contract_write_statuses', (SELECT JSON_OBJECT_AGG(code, status) FROM iam_permissions WHERE code IN ('channel.upstream_contract.create', 'channel.upstream_contract.publish', 'channel.upstream_contract.quarantine')),
    'root_upstream_contract_grants', (SELECT COUNT(*) FROM iam_role_permissions g JOIN iam_roles r ON r.id = g.role_id JOIN iam_permissions p ON p.id = g.permission_id WHERE r.context_key = 'platform' AND r.code = 'root' AND r.builtin = 1 AND p.code IN ('channel.upstream_contract.create', 'channel.upstream_contract.publish', 'channel.upstream_contract.quarantine'))
  ),
  '{"operations":["channel.upstream_contract.create","channel.upstream_contract.publish","channel.upstream_contract.quarantine"],"role":"platform/root","existing_grants":"preserved","user_assignment_changes":0,"writes_enabled":true,"financial_ledger_changes":0,"contract_bindings_published":0}',
  'success', JSON_BUILD_OBJECT('Policy', (SELECT policy_revision FROM iam_policy_state WHERE id = 1), 'Catalog', (SELECT catalog_revision FROM iam_policy_state WHERE id = 1)),
  '142_publish_upstream_contract_write_permissions', CAST(EXTRACT(EPOCH FROM clock_timestamp()) * 1000 AS BIGINT),
  'Publish maintainer-authorized upstream contract create/publish/quarantine for the existing builtin root role only; enables the channel-11 contract pilot without touching financial data'
FROM iam_upstream_contract_release_guard WHERE apply_release = 1;

DROP TABLE iam_upstream_contract_release_guard;

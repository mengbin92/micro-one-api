-- Maintainer-approved publication of the existing supplier-report import owner.
-- Adds only billing.vendor_report.import to the existing builtin root release.
-- Preserves user assignments, other operations, existing deny/narrow grants,
-- disabled/archived governance and receipt replay; does not change financial data.
-- The policy lock serializes this increment with ordinary IAM management writes.
UPDATE iam_policy_state SET policy_revision = policy_revision WHERE id = 1;

-- TEMPORARY DDL does not implicitly commit on MySQL. A failed CHECK leaves all
-- persistent data unchanged. IF NOT EXISTS + DELETE also recover a reused MySQL
-- connection after a prior failed preflight left its temporary table behind.
CREATE TEMPORARY TABLE IF NOT EXISTS iam_vendor_import_release_guard (
  ready INTEGER NOT NULL,
  apply_release INTEGER NOT NULL,
  root_grants_added INTEGER NOT NULL,
  before_data TEXT NOT NULL,
  CONSTRAINT iam_vendor_import_catalog_and_root_must_be_publishable CHECK (ready = 1)
);
DELETE FROM iam_vendor_import_release_guard;
INSERT INTO iam_vendor_import_release_guard (ready, apply_release, root_grants_added, before_data)
SELECT
  CASE WHEN EXISTS (SELECT 1 FROM iam_audit_events WHERE event_id = 'migration:140:vendor-report-import' AND actor_service_id = 'schema-migrate' AND action = 'iam.catalog.release.vendor_report_import' AND result = 'success') OR (
    (SELECT COUNT(*) FROM iam_policy_state WHERE id = 1) = 1
    AND (SELECT COUNT(*) FROM iam_permissions p JOIN iam_resources r ON r.id = p.resource_id
      WHERE p.status IN ('draft', 'enabled') AND p.action = 'import'
      AND p.code = 'billing.vendor_report.import' AND r.code = 'billing.vendor_report' AND r.owner_service = 'billing' AND r.enabled = 1) = 1
    AND EXISTS (SELECT 1 FROM iam_roles WHERE context_type = 'platform' AND organization_id = 0 AND context_key = 'platform' AND code = 'root' AND builtin = 1 AND status = 'enabled')
  ) THEN 1 ELSE 0 END,
  CASE WHEN EXISTS (SELECT 1 FROM iam_audit_events WHERE event_id = 'migration:140:vendor-report-import' AND actor_service_id = 'schema-migrate' AND action = 'iam.catalog.release.vendor_report_import' AND result = 'success') THEN 0 ELSE 1 END,
  (SELECT COUNT(*) FROM iam_roles r CROSS JOIN iam_permissions p
    WHERE r.context_key = 'platform' AND r.code = 'root' AND r.builtin = 1 AND r.status = 'enabled'
    AND p.code = 'billing.vendor_report.import'
    AND NOT EXISTS (SELECT 1 FROM iam_role_permissions g WHERE g.role_id = r.id AND g.permission_id = p.id)),
  JSON_OBJECT(
    'policy_revision', (SELECT policy_revision FROM iam_policy_state WHERE id = 1),
    'catalog_revision', (SELECT catalog_revision FROM iam_policy_state WHERE id = 1),
    'authorization_mode', (SELECT authorization_mode FROM iam_policy_state WHERE id = 1),
    'root_role_revision', (SELECT revision FROM iam_roles WHERE context_key = 'platform' AND code = 'root' AND builtin = 1),
    'vendor_report_import_status', (SELECT status FROM iam_permissions WHERE code = 'billing.vendor_report.import'),
    'root_import_grants', (SELECT COUNT(*) FROM iam_role_permissions g JOIN iam_roles r ON r.id = g.role_id JOIN iam_permissions p ON p.id = g.permission_id WHERE r.context_key = 'platform' AND r.code = 'root' AND r.builtin = 1 AND p.code = 'billing.vendor_report.import')
  );

UPDATE iam_permissions SET status = 'enabled', binding_state = 'bound', revision = revision + 1
WHERE code = 'billing.vendor_report.import' AND (status <> 'enabled' OR binding_state <> 'bound')
AND (SELECT apply_release FROM iam_vendor_import_release_guard) = 1;

UPDATE iam_roles SET revision = revision + 1
WHERE context_key = 'platform' AND code = 'root' AND builtin = 1 AND status = 'enabled'
AND (SELECT apply_release * root_grants_added FROM iam_vendor_import_release_guard) > 0;

-- Any existing relation wins, including an explicit deny or a narrower allow.
INSERT INTO iam_role_permissions (context_key, role_id, permission_id, effect, scope_descriptor, revision)
SELECT 'platform', r.id, p.id, 'allow', '{"clauses":[{"all":true}]}', r.revision
FROM iam_roles r CROSS JOIN iam_permissions p
WHERE r.context_key = 'platform' AND r.code = 'root' AND r.builtin = 1 AND r.status = 'enabled'
AND p.code = 'billing.vendor_report.import'
AND (SELECT apply_release FROM iam_vendor_import_release_guard) = 1
AND NOT EXISTS (SELECT 1 FROM iam_role_permissions g WHERE g.role_id = r.id AND g.permission_id = p.id);

-- Policy/catalog revisions invalidate existing authorization snapshots without
-- changing role assignments, active sessions, or any user's own revision.
UPDATE iam_policy_state SET policy_revision = policy_revision + 1, catalog_revision = catalog_revision + 1
WHERE id = 1 AND (SELECT apply_release FROM iam_vendor_import_release_guard) = 1;

INSERT INTO iam_audit_events (event_id, actor_service_id, context_key, target_context_key, action, target,
  before_data, after_data, diff, result, decision_versions, request_id, occurred_at, reason)
SELECT 'migration:140:vendor-report-import', 'schema-migrate', 'platform', 'platform',
  'iam.catalog.release.vendor_report_import', 'platform/root:billing.vendor_report.import',
  before_data, JSON_OBJECT(
    'policy_revision', (SELECT policy_revision FROM iam_policy_state WHERE id = 1),
    'catalog_revision', (SELECT catalog_revision FROM iam_policy_state WHERE id = 1),
    'authorization_mode', (SELECT authorization_mode FROM iam_policy_state WHERE id = 1),
    'root_role_revision', (SELECT revision FROM iam_roles WHERE context_key = 'platform' AND code = 'root' AND builtin = 1),
    'vendor_report_import_status', (SELECT status FROM iam_permissions WHERE code = 'billing.vendor_report.import'),
    'root_import_grants', (SELECT COUNT(*) FROM iam_role_permissions g JOIN iam_roles r ON r.id = g.role_id JOIN iam_permissions p ON p.id = g.permission_id WHERE r.context_key = 'platform' AND r.code = 'root' AND r.builtin = 1 AND p.code = 'billing.vendor_report.import')
  ),
  '{"operations":["billing.vendor_report.import"],"role":"platform/root","existing_grants":"preserved","user_assignment_changes":0,"writes_enabled":true,"financial_ledger_changes":0}',
  'success', JSON_OBJECT('Policy', (SELECT policy_revision FROM iam_policy_state WHERE id = 1), 'Catalog', (SELECT catalog_revision FROM iam_policy_state WHERE id = 1)),
  '140_publish_vendor_report_import', CAST(UNIX_TIMESTAMP(CURRENT_TIMESTAMP(3)) * 1000 AS UNSIGNED),
  'Publish maintainer-authorized supplier report import for the existing builtin root role only; preserve all other permissions and financial data'
FROM iam_vendor_import_release_guard WHERE apply_release = 1;

DROP TEMPORARY TABLE iam_vendor_import_release_guard;

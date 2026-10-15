-- Identity-owned P2 catalog additions only. No grants, bindings, activation or mode switch.

INSERT INTO iam_resources (code, name, owner_service) SELECT 'billing.vendor_report', 'billing.vendor_report', 'billing' WHERE NOT EXISTS (SELECT 1 FROM iam_resources WHERE code = 'billing.vendor_report');

INSERT INTO iam_permissions (resource_id, code, action, name, status, supported_scopes, supported_context_types, binding_state, protected) SELECT id, 'billing.vendor_report.read', 'read', 'billing.vendor_report.read', 'draft', '["all"]', '["platform"]', 'unbound', 0 FROM iam_resources WHERE code = 'billing.vendor_report' AND NOT EXISTS (SELECT 1 FROM iam_permissions WHERE code = 'billing.vendor_report.read');

INSERT INTO iam_permissions (resource_id, code, action, name, status, supported_scopes, supported_context_types, binding_state, protected) SELECT id, 'billing.vendor_report.import', 'import', 'billing.vendor_report.import', 'draft', '["all"]', '["platform"]', 'unbound', 0 FROM iam_resources WHERE code = 'billing.vendor_report' AND NOT EXISTS (SELECT 1 FROM iam_permissions WHERE code = 'billing.vendor_report.import');

INSERT INTO iam_permissions (resource_id, code, action, name, status, supported_scopes, supported_context_types, binding_state, protected) SELECT id, 'billing.vendor_report.attribute', 'attribute', 'billing.vendor_report.attribute', 'draft', '["all"]', '["platform"]', 'unbound', 0 FROM iam_resources WHERE code = 'billing.vendor_report' AND NOT EXISTS (SELECT 1 FROM iam_permissions WHERE code = 'billing.vendor_report.attribute');

INSERT INTO iam_resources (code, name, owner_service) SELECT 'channel.upstream_contract', 'channel.upstream_contract', 'channel' WHERE NOT EXISTS (SELECT 1 FROM iam_resources WHERE code = 'channel.upstream_contract');

INSERT INTO iam_permissions (resource_id, code, action, name, status, supported_scopes, supported_context_types, binding_state, protected) SELECT id, 'channel.upstream_contract.read', 'read', 'channel.upstream_contract.read', 'draft', '["all"]', '["platform"]', 'unbound', 0 FROM iam_resources WHERE code = 'channel.upstream_contract' AND NOT EXISTS (SELECT 1 FROM iam_permissions WHERE code = 'channel.upstream_contract.read');

INSERT INTO iam_permissions (resource_id, code, action, name, status, supported_scopes, supported_context_types, binding_state, protected) SELECT id, 'channel.upstream_contract.create', 'create', 'channel.upstream_contract.create', 'draft', '["all"]', '["platform"]', 'unbound', 0 FROM iam_resources WHERE code = 'channel.upstream_contract' AND NOT EXISTS (SELECT 1 FROM iam_permissions WHERE code = 'channel.upstream_contract.create');

INSERT INTO iam_permissions (resource_id, code, action, name, status, supported_scopes, supported_context_types, binding_state, protected) SELECT id, 'channel.upstream_contract.publish', 'publish', 'channel.upstream_contract.publish', 'draft', '["all"]', '["platform"]', 'unbound', 0 FROM iam_resources WHERE code = 'channel.upstream_contract' AND NOT EXISTS (SELECT 1 FROM iam_permissions WHERE code = 'channel.upstream_contract.publish');

INSERT INTO iam_permissions (resource_id, code, action, name, status, supported_scopes, supported_context_types, binding_state, protected) SELECT id, 'channel.upstream_contract.quarantine', 'quarantine', 'channel.upstream_contract.quarantine', 'draft', '["all"]', '["platform"]', 'unbound', 0 FROM iam_resources WHERE code = 'channel.upstream_contract' AND NOT EXISTS (SELECT 1 FROM iam_permissions WHERE code = 'channel.upstream_contract.quarantine');

UPDATE iam_policy_state SET catalog_revision = 2 WHERE id = 1 AND catalog_revision < 2;

-- A2 identity-owned storage. Epochs are UTC milliseconds; NULL expiry is infinite.

-- No mode switch, legacy backfill, live assignment or bound operation is introduced.

ALTER TABLE users ADD COLUMN authorization_revision BIGINT NOT NULL DEFAULT 1;

CREATE TABLE iam_policy_state (
  id INTEGER PRIMARY KEY CHECK (id = 1),
  policy_revision BIGINT NOT NULL CHECK (policy_revision > 0),
  catalog_revision BIGINT NOT NULL CHECK (catalog_revision > 0),
  authorization_mode VARCHAR(16) NOT NULL,
  cutover_state VARCHAR(16) NOT NULL,
  cutover_batch_id VARCHAR(128) NOT NULL DEFAULT '',
  cutover_verified_at BIGINT NULL,
  CHECK ((authorization_mode = 'legacy' AND cutover_state IN ('idle','blocked') AND cutover_verified_at IS NULL) OR (authorization_mode = 'iam' AND cutover_state IN ('verified','complete') AND cutover_verified_at > 0 AND cutover_verified_at IS NOT NULL)),
  CHECK ((cutover_state = 'idle' AND cutover_batch_id = '') OR (cutover_state <> 'idle' AND cutover_batch_id <> ''))
);

CREATE TABLE iam_resources (
  id BIGSERIAL PRIMARY KEY,
  code VARCHAR(160) NOT NULL UNIQUE,
  name VARCHAR(255) NOT NULL,
  owner_service VARCHAR(64) NOT NULL,
  enabled INTEGER NOT NULL DEFAULT 1 CHECK (enabled IN (0,1))
);

CREATE TABLE iam_permissions (
  id BIGSERIAL PRIMARY KEY,
  resource_id BIGINT NOT NULL,
  code VARCHAR(160) NOT NULL UNIQUE,
  action VARCHAR(80) NOT NULL,
  name VARCHAR(255) NOT NULL,
  category VARCHAR(64) NOT NULL DEFAULT '',
  status VARCHAR(16) NOT NULL CHECK (status IN ('draft','enabled','disabled','archived')),
  risk_level VARCHAR(16) NOT NULL DEFAULT 'normal',
  supported_scopes TEXT NOT NULL,
  supported_context_types TEXT NOT NULL,
  binding_state VARCHAR(16) NOT NULL CHECK (binding_state IN ('unbound','bound')),
  protected INTEGER NOT NULL DEFAULT 0 CHECK (protected IN (0,1)),
  revision BIGINT NOT NULL DEFAULT 1 CHECK (revision > 0),
  UNIQUE (resource_id, action),
  FOREIGN KEY (resource_id) REFERENCES iam_resources (id)
);

CREATE TABLE iam_roles (
  id BIGSERIAL PRIMARY KEY,
  context_type VARCHAR(16) NOT NULL,
  organization_id BIGINT NOT NULL DEFAULT 0,
  context_key VARCHAR(80) NOT NULL,
  code VARCHAR(80) NOT NULL,
  name VARCHAR(255) NOT NULL,
  description TEXT NOT NULL,
  status VARCHAR(16) NOT NULL CHECK (status IN ('draft','enabled','disabled','archived')),
  builtin INTEGER NOT NULL DEFAULT 0 CHECK (builtin IN (0,1)),
  max_members BIGINT NULL CHECK (max_members IS NULL OR max_members > 0),
  creation_delegation_id BIGINT NULL,
  revision BIGINT NOT NULL DEFAULT 1 CHECK (revision > 0),
  created_by BIGINT NOT NULL DEFAULT 0,
  updated_by BIGINT NOT NULL DEFAULT 0,
  UNIQUE (context_key, code),
  UNIQUE (context_key, id),
  CHECK ((context_type = 'platform' AND organization_id = 0 AND context_key = 'platform') OR (context_type = 'organization' AND organization_id > 0 AND context_key = 'organization:' || organization_id::text))
);

CREATE TABLE iam_role_permissions (
  id BIGSERIAL PRIMARY KEY,
  context_key VARCHAR(80) NOT NULL,
  role_id BIGINT NOT NULL,
  permission_id BIGINT NOT NULL,
  effect VARCHAR(8) NOT NULL CHECK (effect IN ('allow','deny')),
  scope_descriptor TEXT NOT NULL,
  revision BIGINT NOT NULL DEFAULT 1 CHECK (revision > 0),
  UNIQUE (role_id, permission_id, effect),
  FOREIGN KEY (context_key, role_id) REFERENCES iam_roles (context_key, id),
  FOREIGN KEY (permission_id) REFERENCES iam_permissions (id)
);

CREATE TABLE iam_role_inheritance (
  context_key VARCHAR(80) NOT NULL,
  senior_role_id BIGINT NOT NULL,
  junior_role_id BIGINT NOT NULL,
  PRIMARY KEY (context_key, senior_role_id, junior_role_id),
  CHECK (senior_role_id <> junior_role_id),
  FOREIGN KEY (context_key, senior_role_id) REFERENCES iam_roles (context_key, id),
  FOREIGN KEY (context_key, junior_role_id) REFERENCES iam_roles (context_key, id)
);

CREATE TABLE iam_user_roles (
  id BIGSERIAL PRIMARY KEY,
  context_key VARCHAR(80) NOT NULL,
  user_id BIGINT NOT NULL,
  membership_id BIGINT NOT NULL DEFAULT 0,
  role_id BIGINT NOT NULL,
  allow_boundary TEXT NOT NULL,
  starts_at BIGINT NOT NULL CHECK (starts_at > 0),
  expires_at BIGINT NULL,
  status VARCHAR(16) NOT NULL CHECK (status IN ('active','revoked')),
  assigned_by BIGINT NOT NULL DEFAULT 0,
  revision BIGINT NOT NULL DEFAULT 1 CHECK (revision > 0),
  origin VARCHAR(24) NOT NULL CHECK (origin IN ('legacy_candidate','default','bootstrap','explicit')),
  migration_batch_id VARCHAR(128) NOT NULL DEFAULT '',
  CHECK (expires_at IS NULL OR expires_at > starts_at),
  UNIQUE (context_key, user_id, role_id),
  CHECK ((context_key = 'platform' AND membership_id = 0) OR (context_key <> 'platform' AND membership_id > 0)),
  CHECK ((origin = 'legacy_candidate' AND migration_batch_id <> '') OR (origin <> 'legacy_candidate' AND migration_batch_id = '')),
  FOREIGN KEY (context_key, role_id) REFERENCES iam_roles (context_key, id),
  FOREIGN KEY (user_id) REFERENCES users (id)
);

CREATE TABLE iam_sessions (
  session_id VARCHAR(128) PRIMARY KEY CHECK (session_id <> ''),
  user_id BIGINT NOT NULL,
  expires_at BIGINT NOT NULL CHECK (expires_at > 0),
  revoked_at BIGINT NULL,
  revision BIGINT NOT NULL DEFAULT 1 CHECK (revision > 0),
  FOREIGN KEY (user_id) REFERENCES users (id)
);

CREATE TABLE iam_session_contexts (
  session_id VARCHAR(128) NOT NULL,
  context_key VARCHAR(80) NOT NULL,
  activation_state VARCHAR(24) NOT NULL CHECK (activation_state IN ('active','selection_required')),
  revoked_at BIGINT NULL,
  revision BIGINT NOT NULL DEFAULT 1 CHECK (revision > 0),
  PRIMARY KEY (session_id, context_key),
  FOREIGN KEY (session_id) REFERENCES iam_sessions (session_id)
);

CREATE TABLE iam_session_roles (
  session_id VARCHAR(128) NOT NULL,
  context_key VARCHAR(80) NOT NULL,
  role_id BIGINT NOT NULL,
  PRIMARY KEY (session_id, context_key, role_id),
  FOREIGN KEY (session_id, context_key) REFERENCES iam_session_contexts (session_id, context_key),
  FOREIGN KEY (context_key, role_id) REFERENCES iam_roles (context_key, id)
);

CREATE TABLE iam_delegations (
  id BIGSERIAL PRIMARY KEY,
  context_key VARCHAR(80) NOT NULL,
  manager_role_id BIGINT NOT NULL,
  target_kind VARCHAR(24) NOT NULL CHECK (target_kind IN ('role','role_creation','user_credentials')),
  target_role_id BIGINT NULL,
  actions TEXT NOT NULL,
  target_user_scope TEXT NOT NULL,
  grant_ceiling TEXT NOT NULL,
  can_redelegate INTEGER NOT NULL DEFAULT 0 CHECK (can_redelegate IN (0,1)),
  starts_at BIGINT NOT NULL CHECK (starts_at > 0),
  expires_at BIGINT NULL,
  revision BIGINT NOT NULL DEFAULT 1 CHECK (revision > 0),
  CHECK (expires_at IS NULL OR expires_at > starts_at),
  UNIQUE (context_key, id),
  CHECK ((target_kind = 'role' AND target_role_id IS NOT NULL) OR (target_kind <> 'role' AND target_role_id IS NULL)),
  CHECK (target_kind <> 'user_credentials' OR context_key = 'platform'),
  FOREIGN KEY (context_key, manager_role_id) REFERENCES iam_roles (context_key, id),
  FOREIGN KEY (context_key, target_role_id) REFERENCES iam_roles (context_key, id)
);

ALTER TABLE iam_roles ADD CONSTRAINT fk_iam_role_creation FOREIGN KEY (context_key, creation_delegation_id) REFERENCES iam_delegations (context_key, id);

CREATE TABLE iam_role_constraints (
  id BIGSERIAL PRIMARY KEY,
  context_key VARCHAR(80) NOT NULL,
  type VARCHAR(8) NOT NULL CHECK (type IN ('SSD','DSD')),
  name VARCHAR(255) NOT NULL,
  max_count BIGINT NOT NULL CHECK (max_count > 0),
  enabled INTEGER NOT NULL DEFAULT 1 CHECK (enabled IN (0,1)),
  revision BIGINT NOT NULL DEFAULT 1 CHECK (revision > 0),
  UNIQUE (context_key, id)
);

CREATE TABLE iam_role_constraint_members (
  context_key VARCHAR(80) NOT NULL,
  constraint_id BIGINT NOT NULL,
  role_id BIGINT NOT NULL,
  PRIMARY KEY (context_key, constraint_id, role_id),
  FOREIGN KEY (context_key, constraint_id) REFERENCES iam_role_constraints (context_key, id),
  FOREIGN KEY (context_key, role_id) REFERENCES iam_roles (context_key, id)
);

CREATE TABLE iam_menu_items (
  id BIGSERIAL PRIMARY KEY,
  parent_id BIGINT NULL,
  route_key VARCHAR(128) NOT NULL,
  name VARCHAR(255) NOT NULL,
  icon_key VARCHAR(64) NOT NULL,
  sort INTEGER NOT NULL DEFAULT 0,
  enabled INTEGER NOT NULL DEFAULT 1 CHECK (enabled IN (0,1)),
  required_all TEXT NOT NULL,
  required_any TEXT NOT NULL,
  revision BIGINT NOT NULL DEFAULT 1 CHECK (revision > 0),
  FOREIGN KEY (parent_id) REFERENCES iam_menu_items (id)
);

CREATE TABLE iam_audit_events (
  event_id VARCHAR(128) PRIMARY KEY,
  actor_user_id BIGINT NOT NULL DEFAULT 0,
  actor_service_id VARCHAR(128) NOT NULL DEFAULT '',
  actor_session_id VARCHAR(128) NOT NULL DEFAULT '',
  context_key VARCHAR(80) NOT NULL,
  target_context_key VARCHAR(80) NOT NULL,
  action VARCHAR(160) NOT NULL,
  target TEXT NOT NULL,
  before_data TEXT NOT NULL,
  after_data TEXT NOT NULL,
  diff TEXT NOT NULL,
  result VARCHAR(16) NOT NULL CHECK (result IN ('success','failure')),
  decision_versions TEXT NOT NULL,
  request_id VARCHAR(128) NOT NULL,
  occurred_at BIGINT NOT NULL CHECK (occurred_at > 0),
  reason TEXT NOT NULL
);

CREATE INDEX iam_user_roles_window ON iam_user_roles (context_key, role_id, status, starts_at, expires_at, user_id);

CREATE INDEX iam_user_roles_batch ON iam_user_roles (origin, migration_batch_id);

CREATE INDEX iam_sessions_user ON iam_sessions (user_id, expires_at);

CREATE INDEX iam_delegations_window ON iam_delegations (context_key, manager_role_id, starts_at, expires_at);

CREATE INDEX iam_audit_context_time ON iam_audit_events (target_context_key, occurred_at, event_id);

INSERT INTO iam_policy_state (id, policy_revision, catalog_revision, authorization_mode, cutover_state) VALUES (1, 1, 1, 'legacy', 'idle');

INSERT INTO iam_resources (id, code, name, owner_service) VALUES (1, 'admin.console', 'admin.console', 'admin');

INSERT INTO iam_resources (id, code, name, owner_service) VALUES (2, 'admin.overview', 'admin.overview', 'admin');

INSERT INTO iam_resources (id, code, name, owner_service) VALUES (3, 'billing.account', 'billing.account', 'billing');

INSERT INTO iam_resources (id, code, name, owner_service) VALUES (4, 'billing.payment', 'billing.payment', 'billing');

INSERT INTO iam_resources (id, code, name, owner_service) VALUES (5, 'billing.pricing', 'billing.pricing', 'billing');

INSERT INTO iam_resources (id, code, name, owner_service) VALUES (6, 'billing.reconciliation', 'billing.reconciliation', 'billing');

INSERT INTO iam_resources (id, code, name, owner_service) VALUES (7, 'billing.redemption', 'billing.redemption', 'billing');

INSERT INTO iam_resources (id, code, name, owner_service) VALUES (8, 'billing.report', 'billing.report', 'billing');

INSERT INTO iam_resources (id, code, name, owner_service) VALUES (9, 'billing.routing_policy', 'billing.routing_policy', 'billing');

INSERT INTO iam_resources (id, code, name, owner_service) VALUES (10, 'billing.upstream_cost', 'billing.upstream_cost', 'billing');

INSERT INTO iam_resources (id, code, name, owner_service) VALUES (11, 'channel.account', 'channel.account', 'channel');

INSERT INTO iam_resources (id, code, name, owner_service) VALUES (12, 'channel.channel', 'channel.channel', 'channel');

INSERT INTO iam_resources (id, code, name, owner_service) VALUES (13, 'channel.model', 'channel.model', 'channel');

INSERT INTO iam_resources (id, code, name, owner_service) VALUES (14, 'channel.model_alias', 'channel.model_alias', 'channel');

INSERT INTO iam_resources (id, code, name, owner_service) VALUES (15, 'channel.model_mapping', 'channel.model_mapping', 'channel');

INSERT INTO iam_resources (id, code, name, owner_service) VALUES (16, 'channel.model_routing', 'channel.model_routing', 'channel');

INSERT INTO iam_resources (id, code, name, owner_service) VALUES (17, 'channel.model_usage', 'channel.model_usage', 'channel');

INSERT INTO iam_resources (id, code, name, owner_service) VALUES (18, 'channel.routing_group', 'channel.routing_group', 'channel');

INSERT INTO iam_resources (id, code, name, owner_service) VALUES (19, 'channel.usage_semantic_block', 'channel.usage_semantic_block', 'channel');

INSERT INTO iam_resources (id, code, name, owner_service) VALUES (20, 'iam.audit', 'iam.audit', 'identity');

INSERT INTO iam_resources (id, code, name, owner_service) VALUES (21, 'iam.authorization', 'iam.authorization', 'identity');

INSERT INTO iam_resources (id, code, name, owner_service) VALUES (22, 'iam.constraint', 'iam.constraint', 'identity');

INSERT INTO iam_resources (id, code, name, owner_service) VALUES (23, 'iam.delegation', 'iam.delegation', 'identity');

INSERT INTO iam_resources (id, code, name, owner_service) VALUES (24, 'iam.menu', 'iam.menu', 'identity');

INSERT INTO iam_resources (id, code, name, owner_service) VALUES (25, 'iam.permission', 'iam.permission', 'identity');

INSERT INTO iam_resources (id, code, name, owner_service) VALUES (26, 'iam.role', 'iam.role', 'identity');

INSERT INTO iam_resources (id, code, name, owner_service) VALUES (27, 'identity.routing_access', 'identity.routing_access', 'identity');

INSERT INTO iam_resources (id, code, name, owner_service) VALUES (28, 'identity.session', 'identity.session', 'identity');

INSERT INTO iam_resources (id, code, name, owner_service) VALUES (29, 'identity.user', 'identity.user', 'identity');

INSERT INTO iam_resources (id, code, name, owner_service) VALUES (30, 'identity.user_role', 'identity.user_role', 'identity');

INSERT INTO iam_resources (id, code, name, owner_service) VALUES (31, 'log.request', 'log.request', 'log');

INSERT INTO iam_resources (id, code, name, owner_service) VALUES (32, 'log.selection_event', 'log.selection_event', 'log');

INSERT INTO iam_resources (id, code, name, owner_service) VALUES (33, 'monitor.alert_rule', 'monitor.alert_rule', 'monitor');

INSERT INTO iam_resources (id, code, name, owner_service) VALUES (34, 'monitor.health', 'monitor.health', 'monitor');

INSERT INTO iam_resources (id, code, name, owner_service) VALUES (35, 'monitor.health.service', 'monitor.health.service', 'monitor');

INSERT INTO iam_resources (id, code, name, owner_service) VALUES (36, 'notify.notification', 'notify.notification', 'notify');

INSERT INTO iam_resources (id, code, name, owner_service) VALUES (37, 'organization.member', 'organization.member', 'identity');

INSERT INTO iam_resources (id, code, name, owner_service) VALUES (38, 'organization.member_role', 'organization.member_role', 'identity');

INSERT INTO iam_resources (id, code, name, owner_service) VALUES (39, 'organization.organization', 'organization.organization', 'identity');

INSERT INTO iam_resources (id, code, name, owner_service) VALUES (40, 'organization.unit', 'organization.unit', 'identity');

INSERT INTO iam_resources (id, code, name, owner_service) VALUES (41, 'subscription.plan', 'subscription.plan', 'billing');

INSERT INTO iam_resources (id, code, name, owner_service) VALUES (42, 'subscription.quota_policy', 'subscription.quota_policy', 'billing');

INSERT INTO iam_resources (id, code, name, owner_service) VALUES (43, 'subscription.user_subscription', 'subscription.user_subscription', 'billing');

INSERT INTO iam_resources (id, code, name, owner_service) VALUES (44, 'system.content', 'system.content', 'config');

INSERT INTO iam_resources (id, code, name, owner_service) VALUES (45, 'system.option', 'system.option', 'config');

INSERT INTO iam_permissions (id, resource_id, code, action, name, status, supported_scopes, supported_context_types, binding_state, protected) VALUES (1, 1, 'admin.console.enter', 'enter', 'admin.console.enter', 'draft', '["all"]', '["platform"]', 'unbound', 0);

INSERT INTO iam_permissions (id, resource_id, code, action, name, status, supported_scopes, supported_context_types, binding_state, protected) VALUES (2, 2, 'admin.overview.read', 'read', 'admin.overview.read', 'draft', '["all"]', '["platform"]', 'unbound', 0);

INSERT INTO iam_permissions (id, resource_id, code, action, name, status, supported_scopes, supported_context_types, binding_state, protected) VALUES (3, 3, 'billing.account.balance.adjust', 'balance.adjust', 'billing.account.balance.adjust', 'draft', '["all","self","user_ids","resource_ids"]', '["platform"]', 'unbound', 0);

INSERT INTO iam_permissions (id, resource_id, code, action, name, status, supported_scopes, supported_context_types, binding_state, protected) VALUES (4, 3, 'billing.account.balance.reset', 'balance.reset', 'billing.account.balance.reset', 'draft', '["all","self","user_ids","resource_ids"]', '["platform"]', 'unbound', 0);

INSERT INTO iam_permissions (id, resource_id, code, action, name, status, supported_scopes, supported_context_types, binding_state, protected) VALUES (5, 3, 'billing.account.cost.read', 'cost.read', 'billing.account.cost.read', 'draft', '["all","self","user_ids","resource_ids"]', '["platform"]', 'unbound', 0);

INSERT INTO iam_permissions (id, resource_id, code, action, name, status, supported_scopes, supported_context_types, binding_state, protected) VALUES (6, 3, 'billing.account.ledger.read', 'ledger.read', 'billing.account.ledger.read', 'draft', '["all","self","user_ids","resource_ids"]', '["platform"]', 'unbound', 0);

INSERT INTO iam_permissions (id, resource_id, code, action, name, status, supported_scopes, supported_context_types, binding_state, protected) VALUES (7, 3, 'billing.account.read', 'read', 'billing.account.read', 'draft', '["all","self","user_ids","resource_ids"]', '["platform"]', 'unbound', 0);

INSERT INTO iam_permissions (id, resource_id, code, action, name, status, supported_scopes, supported_context_types, binding_state, protected) VALUES (8, 4, 'billing.payment.list', 'list', 'billing.payment.list', 'draft', '["all","self","user_ids","resource_ids"]', '["platform"]', 'unbound', 0);

INSERT INTO iam_permissions (id, resource_id, code, action, name, status, supported_scopes, supported_context_types, binding_state, protected) VALUES (9, 4, 'billing.payment.read', 'read', 'billing.payment.read', 'draft', '["all","self","user_ids","resource_ids"]', '["platform"]', 'unbound', 0);

INSERT INTO iam_permissions (id, resource_id, code, action, name, status, supported_scopes, supported_context_types, binding_state, protected) VALUES (10, 4, 'billing.payment.refund', 'refund', 'billing.payment.refund', 'draft', '["all","self","user_ids","resource_ids"]', '["platform"]', 'unbound', 0);

INSERT INTO iam_permissions (id, resource_id, code, action, name, status, supported_scopes, supported_context_types, binding_state, protected) VALUES (11, 5, 'billing.pricing.export', 'export', 'billing.pricing.export', 'draft', '["all","resource_ids"]', '["platform"]', 'unbound', 0);

INSERT INTO iam_permissions (id, resource_id, code, action, name, status, supported_scopes, supported_context_types, binding_state, protected) VALUES (12, 5, 'billing.pricing.import', 'import', 'billing.pricing.import', 'draft', '["all","resource_ids"]', '["platform"]', 'unbound', 0);

INSERT INTO iam_permissions (id, resource_id, code, action, name, status, supported_scopes, supported_context_types, binding_state, protected) VALUES (13, 5, 'billing.pricing.read', 'read', 'billing.pricing.read', 'draft', '["all","resource_ids"]', '["platform"]', 'unbound', 0);

INSERT INTO iam_permissions (id, resource_id, code, action, name, status, supported_scopes, supported_context_types, binding_state, protected) VALUES (14, 5, 'billing.pricing.update', 'update', 'billing.pricing.update', 'draft', '["all","resource_ids"]', '["platform"]', 'unbound', 0);

INSERT INTO iam_permissions (id, resource_id, code, action, name, status, supported_scopes, supported_context_types, binding_state, protected) VALUES (15, 6, 'billing.reconciliation.issues.read', 'issues.read', 'billing.reconciliation.issues.read', 'draft', '["all"]', '["platform"]', 'unbound', 0);

INSERT INTO iam_permissions (id, resource_id, code, action, name, status, supported_scopes, supported_context_types, binding_state, protected) VALUES (16, 6, 'billing.reconciliation.read', 'read', 'billing.reconciliation.read', 'draft', '["all"]', '["platform"]', 'unbound', 0);

INSERT INTO iam_permissions (id, resource_id, code, action, name, status, supported_scopes, supported_context_types, binding_state, protected) VALUES (17, 6, 'billing.reconciliation.run', 'run', 'billing.reconciliation.run', 'draft', '["all"]', '["platform"]', 'unbound', 0);

INSERT INTO iam_permissions (id, resource_id, code, action, name, status, supported_scopes, supported_context_types, binding_state, protected) VALUES (18, 7, 'billing.redemption.batch_create', 'batch_create', 'billing.redemption.batch_create', 'draft', '["all","resource_ids"]', '["platform"]', 'unbound', 0);

INSERT INTO iam_permissions (id, resource_id, code, action, name, status, supported_scopes, supported_context_types, binding_state, protected) VALUES (19, 7, 'billing.redemption.create', 'create', 'billing.redemption.create', 'draft', '["all","resource_ids"]', '["platform"]', 'unbound', 0);

INSERT INTO iam_permissions (id, resource_id, code, action, name, status, supported_scopes, supported_context_types, binding_state, protected) VALUES (20, 7, 'billing.redemption.delete', 'delete', 'billing.redemption.delete', 'draft', '["all","resource_ids"]', '["platform"]', 'unbound', 0);

INSERT INTO iam_permissions (id, resource_id, code, action, name, status, supported_scopes, supported_context_types, binding_state, protected) VALUES (21, 7, 'billing.redemption.export', 'export', 'billing.redemption.export', 'draft', '["all","resource_ids"]', '["platform"]', 'unbound', 0);

INSERT INTO iam_permissions (id, resource_id, code, action, name, status, supported_scopes, supported_context_types, binding_state, protected) VALUES (22, 7, 'billing.redemption.list', 'list', 'billing.redemption.list', 'draft', '["all","resource_ids"]', '["platform"]', 'unbound', 0);

INSERT INTO iam_permissions (id, resource_id, code, action, name, status, supported_scopes, supported_context_types, binding_state, protected) VALUES (23, 7, 'billing.redemption.read', 'read', 'billing.redemption.read', 'draft', '["all","resource_ids"]', '["platform"]', 'unbound', 0);

INSERT INTO iam_permissions (id, resource_id, code, action, name, status, supported_scopes, supported_context_types, binding_state, protected) VALUES (24, 7, 'billing.redemption.update', 'update', 'billing.redemption.update', 'draft', '["all","resource_ids"]', '["platform"]', 'unbound', 0);

INSERT INTO iam_permissions (id, resource_id, code, action, name, status, supported_scopes, supported_context_types, binding_state, protected) VALUES (25, 8, 'billing.report.export', 'export', 'billing.report.export', 'draft', '["all","self","user_ids","resource_ids"]', '["platform"]', 'unbound', 0);

INSERT INTO iam_permissions (id, resource_id, code, action, name, status, supported_scopes, supported_context_types, binding_state, protected) VALUES (26, 9, 'billing.routing_policy.publish', 'publish', 'billing.routing_policy.publish', 'draft', '["all","self","user_ids","resource_ids","routing_group_ids"]', '["platform"]', 'unbound', 0);

INSERT INTO iam_permissions (id, resource_id, code, action, name, status, supported_scopes, supported_context_types, binding_state, protected) VALUES (27, 9, 'billing.routing_policy.read', 'read', 'billing.routing_policy.read', 'draft', '["all","self","user_ids","resource_ids","routing_group_ids"]', '["platform"]', 'unbound', 0);

INSERT INTO iam_permissions (id, resource_id, code, action, name, status, supported_scopes, supported_context_types, binding_state, protected) VALUES (28, 9, 'billing.routing_policy.user_override.delete', 'user_override.delete', 'billing.routing_policy.user_override.delete', 'draft', '["all","self","user_ids","resource_ids","routing_group_ids"]', '["platform"]', 'unbound', 0);

INSERT INTO iam_permissions (id, resource_id, code, action, name, status, supported_scopes, supported_context_types, binding_state, protected) VALUES (29, 9, 'billing.routing_policy.user_override.read', 'user_override.read', 'billing.routing_policy.user_override.read', 'draft', '["all","self","user_ids","resource_ids","routing_group_ids"]', '["platform"]', 'unbound', 0);

INSERT INTO iam_permissions (id, resource_id, code, action, name, status, supported_scopes, supported_context_types, binding_state, protected) VALUES (30, 9, 'billing.routing_policy.user_override.update', 'user_override.update', 'billing.routing_policy.user_override.update', 'draft', '["all","self","user_ids","resource_ids","routing_group_ids"]', '["platform"]', 'unbound', 0);

INSERT INTO iam_permissions (id, resource_id, code, action, name, status, supported_scopes, supported_context_types, binding_state, protected) VALUES (31, 10, 'billing.upstream_cost.create', 'create', 'billing.upstream_cost.create', 'draft', '["all","resource_ids"]', '["platform"]', 'unbound', 0);

INSERT INTO iam_permissions (id, resource_id, code, action, name, status, supported_scopes, supported_context_types, binding_state, protected) VALUES (32, 10, 'billing.upstream_cost.delete', 'delete', 'billing.upstream_cost.delete', 'draft', '["all","resource_ids"]', '["platform"]', 'unbound', 0);

INSERT INTO iam_permissions (id, resource_id, code, action, name, status, supported_scopes, supported_context_types, binding_state, protected) VALUES (33, 10, 'billing.upstream_cost.migrate', 'migrate', 'billing.upstream_cost.migrate', 'draft', '["all","resource_ids"]', '["platform"]', 'unbound', 0);

INSERT INTO iam_permissions (id, resource_id, code, action, name, status, supported_scopes, supported_context_types, binding_state, protected) VALUES (34, 10, 'billing.upstream_cost.read', 'read', 'billing.upstream_cost.read', 'draft', '["all","resource_ids"]', '["platform"]', 'unbound', 0);

INSERT INTO iam_permissions (id, resource_id, code, action, name, status, supported_scopes, supported_context_types, binding_state, protected) VALUES (35, 10, 'billing.upstream_cost.update', 'update', 'billing.upstream_cost.update', 'draft', '["all","resource_ids"]', '["platform"]', 'unbound', 0);

INSERT INTO iam_permissions (id, resource_id, code, action, name, status, supported_scopes, supported_context_types, binding_state, protected) VALUES (36, 11, 'channel.account.create', 'create', 'channel.account.create', 'draft', '["all","resource_ids","routing_group_ids"]', '["platform"]', 'unbound', 0);

INSERT INTO iam_permissions (id, resource_id, code, action, name, status, supported_scopes, supported_context_types, binding_state, protected) VALUES (37, 11, 'channel.account.credential.update', 'credential.update', 'channel.account.credential.update', 'draft', '["all","resource_ids","routing_group_ids"]', '["platform"]', 'unbound', 0);

INSERT INTO iam_permissions (id, resource_id, code, action, name, status, supported_scopes, supported_context_types, binding_state, protected) VALUES (38, 11, 'channel.account.delete', 'delete', 'channel.account.delete', 'draft', '["all","resource_ids","routing_group_ids"]', '["platform"]', 'unbound', 0);

INSERT INTO iam_permissions (id, resource_id, code, action, name, status, supported_scopes, supported_context_types, binding_state, protected) VALUES (39, 11, 'channel.account.disable', 'disable', 'channel.account.disable', 'draft', '["all","resource_ids","routing_group_ids"]', '["platform"]', 'unbound', 0);

INSERT INTO iam_permissions (id, resource_id, code, action, name, status, supported_scopes, supported_context_types, binding_state, protected) VALUES (40, 11, 'channel.account.enable', 'enable', 'channel.account.enable', 'draft', '["all","resource_ids","routing_group_ids"]', '["platform"]', 'unbound', 0);

INSERT INTO iam_permissions (id, resource_id, code, action, name, status, supported_scopes, supported_context_types, binding_state, protected) VALUES (41, 11, 'channel.account.list', 'list', 'channel.account.list', 'draft', '["all","resource_ids","routing_group_ids"]', '["platform"]', 'unbound', 0);

INSERT INTO iam_permissions (id, resource_id, code, action, name, status, supported_scopes, supported_context_types, binding_state, protected) VALUES (42, 11, 'channel.account.oauth.bind', 'oauth.bind', 'channel.account.oauth.bind', 'draft', '["all","resource_ids","routing_group_ids"]', '["platform"]', 'unbound', 0);

INSERT INTO iam_permissions (id, resource_id, code, action, name, status, supported_scopes, supported_context_types, binding_state, protected) VALUES (43, 11, 'channel.account.quota.reset', 'quota.reset', 'channel.account.quota.reset', 'draft', '["all","resource_ids","routing_group_ids"]', '["platform"]', 'unbound', 0);

INSERT INTO iam_permissions (id, resource_id, code, action, name, status, supported_scopes, supported_context_types, binding_state, protected) VALUES (44, 11, 'channel.account.read', 'read', 'channel.account.read', 'draft', '["all","resource_ids","routing_group_ids"]', '["platform"]', 'unbound', 0);

INSERT INTO iam_permissions (id, resource_id, code, action, name, status, supported_scopes, supported_context_types, binding_state, protected) VALUES (45, 11, 'channel.account.recovery.clear', 'recovery.clear', 'channel.account.recovery.clear', 'draft', '["all","resource_ids","routing_group_ids"]', '["platform"]', 'unbound', 0);

INSERT INTO iam_permissions (id, resource_id, code, action, name, status, supported_scopes, supported_context_types, binding_state, protected) VALUES (46, 11, 'channel.account.update', 'update', 'channel.account.update', 'draft', '["all","resource_ids","routing_group_ids"]', '["platform"]', 'unbound', 0);

INSERT INTO iam_permissions (id, resource_id, code, action, name, status, supported_scopes, supported_context_types, binding_state, protected) VALUES (47, 12, 'channel.channel.balance.refresh', 'balance.refresh', 'channel.channel.balance.refresh', 'draft', '["all","resource_ids","routing_group_ids"]', '["platform"]', 'unbound', 0);

INSERT INTO iam_permissions (id, resource_id, code, action, name, status, supported_scopes, supported_context_types, binding_state, protected) VALUES (48, 12, 'channel.channel.batch_delete', 'batch_delete', 'channel.channel.batch_delete', 'draft', '["all","resource_ids","routing_group_ids"]', '["platform"]', 'unbound', 0);

INSERT INTO iam_permissions (id, resource_id, code, action, name, status, supported_scopes, supported_context_types, binding_state, protected) VALUES (49, 12, 'channel.channel.create', 'create', 'channel.channel.create', 'draft', '["all","resource_ids","routing_group_ids"]', '["platform"]', 'unbound', 0);

INSERT INTO iam_permissions (id, resource_id, code, action, name, status, supported_scopes, supported_context_types, binding_state, protected) VALUES (50, 12, 'channel.channel.delete', 'delete', 'channel.channel.delete', 'draft', '["all","resource_ids","routing_group_ids"]', '["platform"]', 'unbound', 0);

INSERT INTO iam_permissions (id, resource_id, code, action, name, status, supported_scopes, supported_context_types, binding_state, protected) VALUES (51, 12, 'channel.channel.disable', 'disable', 'channel.channel.disable', 'draft', '["all","resource_ids","routing_group_ids"]', '["platform"]', 'unbound', 0);

INSERT INTO iam_permissions (id, resource_id, code, action, name, status, supported_scopes, supported_context_types, binding_state, protected) VALUES (52, 12, 'channel.channel.enable', 'enable', 'channel.channel.enable', 'draft', '["all","resource_ids","routing_group_ids"]', '["platform"]', 'unbound', 0);

INSERT INTO iam_permissions (id, resource_id, code, action, name, status, supported_scopes, supported_context_types, binding_state, protected) VALUES (53, 12, 'channel.channel.export', 'export', 'channel.channel.export', 'draft', '["all","resource_ids","routing_group_ids"]', '["platform"]', 'unbound', 0);

INSERT INTO iam_permissions (id, resource_id, code, action, name, status, supported_scopes, supported_context_types, binding_state, protected) VALUES (54, 12, 'channel.channel.list', 'list', 'channel.channel.list', 'draft', '["all","resource_ids","routing_group_ids"]', '["platform"]', 'unbound', 0);

INSERT INTO iam_permissions (id, resource_id, code, action, name, status, supported_scopes, supported_context_types, binding_state, protected) VALUES (55, 12, 'channel.channel.read', 'read', 'channel.channel.read', 'draft', '["all","resource_ids","routing_group_ids"]', '["platform"]', 'unbound', 0);

INSERT INTO iam_permissions (id, resource_id, code, action, name, status, supported_scopes, supported_context_types, binding_state, protected) VALUES (56, 12, 'channel.channel.secret.read', 'secret.read', 'channel.channel.secret.read', 'draft', '["all","resource_ids","routing_group_ids"]', '["platform"]', 'unbound', 0);

INSERT INTO iam_permissions (id, resource_id, code, action, name, status, supported_scopes, supported_context_types, binding_state, protected) VALUES (57, 12, 'channel.channel.secret.rotate', 'secret.rotate', 'channel.channel.secret.rotate', 'draft', '["all","resource_ids","routing_group_ids"]', '["platform"]', 'unbound', 0);

INSERT INTO iam_permissions (id, resource_id, code, action, name, status, supported_scopes, supported_context_types, binding_state, protected) VALUES (58, 12, 'channel.channel.test', 'test', 'channel.channel.test', 'draft', '["all","resource_ids","routing_group_ids"]', '["platform"]', 'unbound', 0);

INSERT INTO iam_permissions (id, resource_id, code, action, name, status, supported_scopes, supported_context_types, binding_state, protected) VALUES (59, 12, 'channel.channel.update', 'update', 'channel.channel.update', 'draft', '["all","resource_ids","routing_group_ids"]', '["platform"]', 'unbound', 0);

INSERT INTO iam_permissions (id, resource_id, code, action, name, status, supported_scopes, supported_context_types, binding_state, protected) VALUES (60, 13, 'channel.model.batch_update', 'batch_update', 'channel.model.batch_update', 'draft', '["all","resource_ids"]', '["platform"]', 'unbound', 0);

INSERT INTO iam_permissions (id, resource_id, code, action, name, status, supported_scopes, supported_context_types, binding_state, protected) VALUES (61, 13, 'channel.model.canonical.merge', 'canonical.merge', 'channel.model.canonical.merge', 'draft', '["all","resource_ids"]', '["platform"]', 'unbound', 0);

INSERT INTO iam_permissions (id, resource_id, code, action, name, status, supported_scopes, supported_context_types, binding_state, protected) VALUES (62, 13, 'channel.model.canonical.preflight', 'canonical.preflight', 'channel.model.canonical.preflight', 'draft', '["all","resource_ids"]', '["platform"]', 'unbound', 0);

INSERT INTO iam_permissions (id, resource_id, code, action, name, status, supported_scopes, supported_context_types, binding_state, protected) VALUES (63, 13, 'channel.model.create', 'create', 'channel.model.create', 'draft', '["all","resource_ids"]', '["platform"]', 'unbound', 0);

INSERT INTO iam_permissions (id, resource_id, code, action, name, status, supported_scopes, supported_context_types, binding_state, protected) VALUES (64, 13, 'channel.model.delete', 'delete', 'channel.model.delete', 'draft', '["all","resource_ids"]', '["platform"]', 'unbound', 0);

INSERT INTO iam_permissions (id, resource_id, code, action, name, status, supported_scopes, supported_context_types, binding_state, protected) VALUES (65, 13, 'channel.model.disable', 'disable', 'channel.model.disable', 'draft', '["all","resource_ids"]', '["platform"]', 'unbound', 0);

INSERT INTO iam_permissions (id, resource_id, code, action, name, status, supported_scopes, supported_context_types, binding_state, protected) VALUES (66, 13, 'channel.model.enable', 'enable', 'channel.model.enable', 'draft', '["all","resource_ids"]', '["platform"]', 'unbound', 0);

INSERT INTO iam_permissions (id, resource_id, code, action, name, status, supported_scopes, supported_context_types, binding_state, protected) VALUES (67, 13, 'channel.model.export', 'export', 'channel.model.export', 'draft', '["all","resource_ids"]', '["platform"]', 'unbound', 0);

INSERT INTO iam_permissions (id, resource_id, code, action, name, status, supported_scopes, supported_context_types, binding_state, protected) VALUES (68, 13, 'channel.model.import', 'import', 'channel.model.import', 'draft', '["all","resource_ids"]', '["platform"]', 'unbound', 0);

INSERT INTO iam_permissions (id, resource_id, code, action, name, status, supported_scopes, supported_context_types, binding_state, protected) VALUES (69, 13, 'channel.model.list', 'list', 'channel.model.list', 'draft', '["all","resource_ids"]', '["platform"]', 'unbound', 0);

INSERT INTO iam_permissions (id, resource_id, code, action, name, status, supported_scopes, supported_context_types, binding_state, protected) VALUES (70, 13, 'channel.model.read', 'read', 'channel.model.read', 'draft', '["all","resource_ids"]', '["platform"]', 'unbound', 0);

INSERT INTO iam_permissions (id, resource_id, code, action, name, status, supported_scopes, supported_context_types, binding_state, protected) VALUES (71, 13, 'channel.model.update', 'update', 'channel.model.update', 'draft', '["all","resource_ids"]', '["platform"]', 'unbound', 0);

INSERT INTO iam_permissions (id, resource_id, code, action, name, status, supported_scopes, supported_context_types, binding_state, protected) VALUES (72, 14, 'channel.model_alias.create', 'create', 'channel.model_alias.create', 'draft', '["all","resource_ids"]', '["platform"]', 'unbound', 0);

INSERT INTO iam_permissions (id, resource_id, code, action, name, status, supported_scopes, supported_context_types, binding_state, protected) VALUES (73, 14, 'channel.model_alias.delete', 'delete', 'channel.model_alias.delete', 'draft', '["all","resource_ids"]', '["platform"]', 'unbound', 0);

INSERT INTO iam_permissions (id, resource_id, code, action, name, status, supported_scopes, supported_context_types, binding_state, protected) VALUES (74, 14, 'channel.model_alias.read', 'read', 'channel.model_alias.read', 'draft', '["all","resource_ids"]', '["platform"]', 'unbound', 0);

INSERT INTO iam_permissions (id, resource_id, code, action, name, status, supported_scopes, supported_context_types, binding_state, protected) VALUES (75, 15, 'channel.model_mapping.create', 'create', 'channel.model_mapping.create', 'draft', '["all","resource_ids","routing_group_ids"]', '["platform"]', 'unbound', 0);

INSERT INTO iam_permissions (id, resource_id, code, action, name, status, supported_scopes, supported_context_types, binding_state, protected) VALUES (76, 15, 'channel.model_mapping.delete', 'delete', 'channel.model_mapping.delete', 'draft', '["all","resource_ids","routing_group_ids"]', '["platform"]', 'unbound', 0);

INSERT INTO iam_permissions (id, resource_id, code, action, name, status, supported_scopes, supported_context_types, binding_state, protected) VALUES (77, 15, 'channel.model_mapping.read', 'read', 'channel.model_mapping.read', 'draft', '["all","resource_ids","routing_group_ids"]', '["platform"]', 'unbound', 0);

INSERT INTO iam_permissions (id, resource_id, code, action, name, status, supported_scopes, supported_context_types, binding_state, protected) VALUES (78, 15, 'channel.model_mapping.update', 'update', 'channel.model_mapping.update', 'draft', '["all","resource_ids","routing_group_ids"]', '["platform"]', 'unbound', 0);

INSERT INTO iam_permissions (id, resource_id, code, action, name, status, supported_scopes, supported_context_types, binding_state, protected) VALUES (79, 16, 'channel.model_routing.create', 'create', 'channel.model_routing.create', 'draft', '["all","resource_ids","routing_group_ids"]', '["platform"]', 'unbound', 0);

INSERT INTO iam_permissions (id, resource_id, code, action, name, status, supported_scopes, supported_context_types, binding_state, protected) VALUES (80, 16, 'channel.model_routing.delete', 'delete', 'channel.model_routing.delete', 'draft', '["all","resource_ids","routing_group_ids"]', '["platform"]', 'unbound', 0);

INSERT INTO iam_permissions (id, resource_id, code, action, name, status, supported_scopes, supported_context_types, binding_state, protected) VALUES (81, 16, 'channel.model_routing.read', 'read', 'channel.model_routing.read', 'draft', '["all","resource_ids","routing_group_ids"]', '["platform"]', 'unbound', 0);

INSERT INTO iam_permissions (id, resource_id, code, action, name, status, supported_scopes, supported_context_types, binding_state, protected) VALUES (82, 16, 'channel.model_routing.update', 'update', 'channel.model_routing.update', 'draft', '["all","resource_ids","routing_group_ids"]', '["platform"]', 'unbound', 0);

INSERT INTO iam_permissions (id, resource_id, code, action, name, status, supported_scopes, supported_context_types, binding_state, protected) VALUES (83, 17, 'channel.model_usage.read', 'read', 'channel.model_usage.read', 'draft', '["all","resource_ids"]', '["platform"]', 'unbound', 0);

INSERT INTO iam_permissions (id, resource_id, code, action, name, status, supported_scopes, supported_context_types, binding_state, protected) VALUES (84, 18, 'channel.routing_group.archive', 'archive', 'channel.routing_group.archive', 'draft', '["all","resource_ids"]', '["platform"]', 'unbound', 0);

INSERT INTO iam_permissions (id, resource_id, code, action, name, status, supported_scopes, supported_context_types, binding_state, protected) VALUES (85, 18, 'channel.routing_group.create', 'create', 'channel.routing_group.create', 'draft', '["all","resource_ids"]', '["platform"]', 'unbound', 0);

INSERT INTO iam_permissions (id, resource_id, code, action, name, status, supported_scopes, supported_context_types, binding_state, protected) VALUES (86, 18, 'channel.routing_group.disable', 'disable', 'channel.routing_group.disable', 'draft', '["all","resource_ids"]', '["platform"]', 'unbound', 0);

INSERT INTO iam_permissions (id, resource_id, code, action, name, status, supported_scopes, supported_context_types, binding_state, protected) VALUES (87, 18, 'channel.routing_group.enable', 'enable', 'channel.routing_group.enable', 'draft', '["all","resource_ids"]', '["platform"]', 'unbound', 0);

INSERT INTO iam_permissions (id, resource_id, code, action, name, status, supported_scopes, supported_context_types, binding_state, protected) VALUES (88, 18, 'channel.routing_group.list', 'list', 'channel.routing_group.list', 'draft', '["all","resource_ids"]', '["platform"]', 'unbound', 0);

INSERT INTO iam_permissions (id, resource_id, code, action, name, status, supported_scopes, supported_context_types, binding_state, protected) VALUES (89, 18, 'channel.routing_group.members.read', 'members.read', 'channel.routing_group.members.read', 'draft', '["all","resource_ids"]', '["platform"]', 'unbound', 0);

INSERT INTO iam_permissions (id, resource_id, code, action, name, status, supported_scopes, supported_context_types, binding_state, protected) VALUES (90, 18, 'channel.routing_group.members.update', 'members.update', 'channel.routing_group.members.update', 'draft', '["all","resource_ids"]', '["platform"]', 'unbound', 0);

INSERT INTO iam_permissions (id, resource_id, code, action, name, status, supported_scopes, supported_context_types, binding_state, protected) VALUES (91, 18, 'channel.routing_group.read', 'read', 'channel.routing_group.read', 'draft', '["all","resource_ids"]', '["platform"]', 'unbound', 0);

INSERT INTO iam_permissions (id, resource_id, code, action, name, status, supported_scopes, supported_context_types, binding_state, protected) VALUES (92, 18, 'channel.routing_group.resource_override.update', 'resource_override.update', 'channel.routing_group.resource_override.update', 'draft', '["all","resource_ids"]', '["platform"]', 'unbound', 0);

INSERT INTO iam_permissions (id, resource_id, code, action, name, status, supported_scopes, supported_context_types, binding_state, protected) VALUES (93, 18, 'channel.routing_group.update', 'update', 'channel.routing_group.update', 'draft', '["all","resource_ids"]', '["platform"]', 'unbound', 0);

INSERT INTO iam_permissions (id, resource_id, code, action, name, status, supported_scopes, supported_context_types, binding_state, protected) VALUES (94, 19, 'channel.usage_semantic_block.list', 'list', 'channel.usage_semantic_block.list', 'draft', '["all","resource_ids"]', '["platform"]', 'unbound', 0);

INSERT INTO iam_permissions (id, resource_id, code, action, name, status, supported_scopes, supported_context_types, binding_state, protected) VALUES (95, 19, 'channel.usage_semantic_block.resolve', 'resolve', 'channel.usage_semantic_block.resolve', 'draft', '["all","resource_ids"]', '["platform"]', 'unbound', 0);

INSERT INTO iam_permissions (id, resource_id, code, action, name, status, supported_scopes, supported_context_types, binding_state, protected) VALUES (96, 20, 'iam.audit.export', 'export', 'iam.audit.export', 'draft', '["all","resource_ids"]', '["platform"]', 'unbound', 0);

INSERT INTO iam_permissions (id, resource_id, code, action, name, status, supported_scopes, supported_context_types, binding_state, protected) VALUES (97, 20, 'iam.audit.read', 'read', 'iam.audit.read', 'draft', '["all","resource_ids"]', '["platform"]', 'unbound', 0);

INSERT INTO iam_permissions (id, resource_id, code, action, name, status, supported_scopes, supported_context_types, binding_state, protected) VALUES (98, 21, 'iam.authorization.explain', 'explain', 'iam.authorization.explain', 'draft', '["all","resource_ids"]', '["platform"]', 'unbound', 0);

INSERT INTO iam_permissions (id, resource_id, code, action, name, status, supported_scopes, supported_context_types, binding_state, protected) VALUES (99, 21, 'iam.authorization.self.read', 'self.read', 'iam.authorization.self.read', 'draft', '["all","resource_ids"]', '["platform"]', 'unbound', 1);

INSERT INTO iam_permissions (id, resource_id, code, action, name, status, supported_scopes, supported_context_types, binding_state, protected) VALUES (100, 21, 'iam.authorization.simulate', 'simulate', 'iam.authorization.simulate', 'draft', '["all","resource_ids"]', '["platform"]', 'unbound', 0);

INSERT INTO iam_permissions (id, resource_id, code, action, name, status, supported_scopes, supported_context_types, binding_state, protected) VALUES (101, 21, 'iam.authorization.user.read', 'user.read', 'iam.authorization.user.read', 'draft', '["all","resource_ids"]', '["platform"]', 'unbound', 0);

INSERT INTO iam_permissions (id, resource_id, code, action, name, status, supported_scopes, supported_context_types, binding_state, protected) VALUES (102, 22, 'iam.constraint.create', 'create', 'iam.constraint.create', 'draft', '["all","resource_ids"]', '["platform"]', 'unbound', 0);

INSERT INTO iam_permissions (id, resource_id, code, action, name, status, supported_scopes, supported_context_types, binding_state, protected) VALUES (103, 22, 'iam.constraint.delete', 'delete', 'iam.constraint.delete', 'draft', '["all","resource_ids"]', '["platform"]', 'unbound', 0);

INSERT INTO iam_permissions (id, resource_id, code, action, name, status, supported_scopes, supported_context_types, binding_state, protected) VALUES (104, 22, 'iam.constraint.read', 'read', 'iam.constraint.read', 'draft', '["all","resource_ids"]', '["platform"]', 'unbound', 0);

INSERT INTO iam_permissions (id, resource_id, code, action, name, status, supported_scopes, supported_context_types, binding_state, protected) VALUES (105, 22, 'iam.constraint.update', 'update', 'iam.constraint.update', 'draft', '["all","resource_ids"]', '["platform"]', 'unbound', 0);

INSERT INTO iam_permissions (id, resource_id, code, action, name, status, supported_scopes, supported_context_types, binding_state, protected) VALUES (106, 23, 'iam.delegation.create', 'create', 'iam.delegation.create', 'draft', '["all","resource_ids"]', '["platform"]', 'unbound', 0);

INSERT INTO iam_permissions (id, resource_id, code, action, name, status, supported_scopes, supported_context_types, binding_state, protected) VALUES (107, 23, 'iam.delegation.read', 'read', 'iam.delegation.read', 'draft', '["all","resource_ids"]', '["platform"]', 'unbound', 0);

INSERT INTO iam_permissions (id, resource_id, code, action, name, status, supported_scopes, supported_context_types, binding_state, protected) VALUES (108, 23, 'iam.delegation.revoke', 'revoke', 'iam.delegation.revoke', 'draft', '["all","resource_ids"]', '["platform"]', 'unbound', 0);

INSERT INTO iam_permissions (id, resource_id, code, action, name, status, supported_scopes, supported_context_types, binding_state, protected) VALUES (109, 23, 'iam.delegation.update', 'update', 'iam.delegation.update', 'draft', '["all","resource_ids"]', '["platform"]', 'unbound', 0);

INSERT INTO iam_permissions (id, resource_id, code, action, name, status, supported_scopes, supported_context_types, binding_state, protected) VALUES (110, 24, 'iam.menu.archive', 'archive', 'iam.menu.archive', 'draft', '["all","resource_ids"]', '["platform"]', 'unbound', 0);

INSERT INTO iam_permissions (id, resource_id, code, action, name, status, supported_scopes, supported_context_types, binding_state, protected) VALUES (111, 24, 'iam.menu.create', 'create', 'iam.menu.create', 'draft', '["all","resource_ids"]', '["platform"]', 'unbound', 0);

INSERT INTO iam_permissions (id, resource_id, code, action, name, status, supported_scopes, supported_context_types, binding_state, protected) VALUES (112, 24, 'iam.menu.list', 'list', 'iam.menu.list', 'draft', '["all","resource_ids"]', '["platform"]', 'unbound', 0);

INSERT INTO iam_permissions (id, resource_id, code, action, name, status, supported_scopes, supported_context_types, binding_state, protected) VALUES (113, 24, 'iam.menu.read', 'read', 'iam.menu.read', 'draft', '["all","resource_ids"]', '["platform"]', 'unbound', 0);

INSERT INTO iam_permissions (id, resource_id, code, action, name, status, supported_scopes, supported_context_types, binding_state, protected) VALUES (114, 24, 'iam.menu.update', 'update', 'iam.menu.update', 'draft', '["all","resource_ids"]', '["platform"]', 'unbound', 0);

INSERT INTO iam_permissions (id, resource_id, code, action, name, status, supported_scopes, supported_context_types, binding_state, protected) VALUES (115, 25, 'iam.permission.archive', 'archive', 'iam.permission.archive', 'draft', '["all","resource_ids"]', '["platform"]', 'unbound', 1);

INSERT INTO iam_permissions (id, resource_id, code, action, name, status, supported_scopes, supported_context_types, binding_state, protected) VALUES (116, 25, 'iam.permission.create', 'create', 'iam.permission.create', 'draft', '["all","resource_ids"]', '["platform"]', 'unbound', 1);

INSERT INTO iam_permissions (id, resource_id, code, action, name, status, supported_scopes, supported_context_types, binding_state, protected) VALUES (117, 25, 'iam.permission.disable', 'disable', 'iam.permission.disable', 'draft', '["all","resource_ids"]', '["platform"]', 'unbound', 1);

INSERT INTO iam_permissions (id, resource_id, code, action, name, status, supported_scopes, supported_context_types, binding_state, protected) VALUES (118, 25, 'iam.permission.enable', 'enable', 'iam.permission.enable', 'draft', '["all","resource_ids"]', '["platform"]', 'unbound', 1);

INSERT INTO iam_permissions (id, resource_id, code, action, name, status, supported_scopes, supported_context_types, binding_state, protected) VALUES (119, 25, 'iam.permission.list', 'list', 'iam.permission.list', 'draft', '["all","resource_ids"]', '["platform"]', 'unbound', 1);

INSERT INTO iam_permissions (id, resource_id, code, action, name, status, supported_scopes, supported_context_types, binding_state, protected) VALUES (120, 25, 'iam.permission.metadata.update', 'metadata.update', 'iam.permission.metadata.update', 'draft', '["all","resource_ids"]', '["platform"]', 'unbound', 1);

INSERT INTO iam_permissions (id, resource_id, code, action, name, status, supported_scopes, supported_context_types, binding_state, protected) VALUES (121, 25, 'iam.permission.read', 'read', 'iam.permission.read', 'draft', '["all","resource_ids"]', '["platform"]', 'unbound', 1);

INSERT INTO iam_permissions (id, resource_id, code, action, name, status, supported_scopes, supported_context_types, binding_state, protected) VALUES (122, 25, 'iam.permission.references.read', 'references.read', 'iam.permission.references.read', 'draft', '["all","resource_ids"]', '["platform"]', 'unbound', 1);

INSERT INTO iam_permissions (id, resource_id, code, action, name, status, supported_scopes, supported_context_types, binding_state, protected) VALUES (123, 26, 'iam.role.archive', 'archive', 'iam.role.archive', 'draft', '["all","resource_ids"]', '["platform"]', 'unbound', 0);

INSERT INTO iam_permissions (id, resource_id, code, action, name, status, supported_scopes, supported_context_types, binding_state, protected) VALUES (124, 26, 'iam.role.copy', 'copy', 'iam.role.copy', 'draft', '["all","resource_ids"]', '["platform"]', 'unbound', 0);

INSERT INTO iam_permissions (id, resource_id, code, action, name, status, supported_scopes, supported_context_types, binding_state, protected) VALUES (125, 26, 'iam.role.create', 'create', 'iam.role.create', 'draft', '["all","resource_ids"]', '["platform"]', 'unbound', 0);

INSERT INTO iam_permissions (id, resource_id, code, action, name, status, supported_scopes, supported_context_types, binding_state, protected) VALUES (126, 26, 'iam.role.disable', 'disable', 'iam.role.disable', 'draft', '["all","resource_ids"]', '["platform"]', 'unbound', 0);

INSERT INTO iam_permissions (id, resource_id, code, action, name, status, supported_scopes, supported_context_types, binding_state, protected) VALUES (127, 26, 'iam.role.enable', 'enable', 'iam.role.enable', 'draft', '["all","resource_ids"]', '["platform"]', 'unbound', 0);

INSERT INTO iam_permissions (id, resource_id, code, action, name, status, supported_scopes, supported_context_types, binding_state, protected) VALUES (128, 26, 'iam.role.hierarchy.update', 'hierarchy.update', 'iam.role.hierarchy.update', 'draft', '["all","resource_ids"]', '["platform"]', 'unbound', 0);

INSERT INTO iam_permissions (id, resource_id, code, action, name, status, supported_scopes, supported_context_types, binding_state, protected) VALUES (129, 26, 'iam.role.list', 'list', 'iam.role.list', 'draft', '["all","resource_ids"]', '["platform"]', 'unbound', 0);

INSERT INTO iam_permissions (id, resource_id, code, action, name, status, supported_scopes, supported_context_types, binding_state, protected) VALUES (130, 26, 'iam.role.members.read', 'members.read', 'iam.role.members.read', 'draft', '["all","resource_ids"]', '["platform"]', 'unbound', 0);

INSERT INTO iam_permissions (id, resource_id, code, action, name, status, supported_scopes, supported_context_types, binding_state, protected) VALUES (131, 26, 'iam.role.permissions.read', 'permissions.read', 'iam.role.permissions.read', 'draft', '["all","resource_ids"]', '["platform"]', 'unbound', 0);

INSERT INTO iam_permissions (id, resource_id, code, action, name, status, supported_scopes, supported_context_types, binding_state, protected) VALUES (132, 26, 'iam.role.permissions.update', 'permissions.update', 'iam.role.permissions.update', 'draft', '["all","resource_ids"]', '["platform"]', 'unbound', 0);

INSERT INTO iam_permissions (id, resource_id, code, action, name, status, supported_scopes, supported_context_types, binding_state, protected) VALUES (133, 26, 'iam.role.read', 'read', 'iam.role.read', 'draft', '["all","resource_ids"]', '["platform"]', 'unbound', 0);

INSERT INTO iam_permissions (id, resource_id, code, action, name, status, supported_scopes, supported_context_types, binding_state, protected) VALUES (134, 26, 'iam.role.update', 'update', 'iam.role.update', 'draft', '["all","resource_ids"]', '["platform"]', 'unbound', 0);

INSERT INTO iam_permissions (id, resource_id, code, action, name, status, supported_scopes, supported_context_types, binding_state, protected) VALUES (135, 27, 'identity.routing_access.default.update', 'default.update', 'identity.routing_access.default.update', 'draft', '["all","self","user_ids","resource_ids","routing_group_ids"]', '["platform"]', 'unbound', 0);

INSERT INTO iam_permissions (id, resource_id, code, action, name, status, supported_scopes, supported_context_types, binding_state, protected) VALUES (136, 27, 'identity.routing_access.grant', 'grant', 'identity.routing_access.grant', 'draft', '["all","self","user_ids","resource_ids","routing_group_ids"]', '["platform"]', 'unbound', 0);

INSERT INTO iam_permissions (id, resource_id, code, action, name, status, supported_scopes, supported_context_types, binding_state, protected) VALUES (137, 27, 'identity.routing_access.public_access.update', 'public_access.update', 'identity.routing_access.public_access.update', 'draft', '["all","self","user_ids","resource_ids","routing_group_ids"]', '["platform"]', 'unbound', 0);

INSERT INTO iam_permissions (id, resource_id, code, action, name, status, supported_scopes, supported_context_types, binding_state, protected) VALUES (138, 27, 'identity.routing_access.read', 'read', 'identity.routing_access.read', 'draft', '["all","self","user_ids","resource_ids","routing_group_ids"]', '["platform"]', 'unbound', 0);

INSERT INTO iam_permissions (id, resource_id, code, action, name, status, supported_scopes, supported_context_types, binding_state, protected) VALUES (139, 27, 'identity.routing_access.revoke', 'revoke', 'identity.routing_access.revoke', 'draft', '["all","self","user_ids","resource_ids","routing_group_ids"]', '["platform"]', 'unbound', 0);

INSERT INTO iam_permissions (id, resource_id, code, action, name, status, supported_scopes, supported_context_types, binding_state, protected) VALUES (140, 28, 'identity.session.roles.activate', 'roles.activate', 'identity.session.roles.activate', 'draft', '["all","self","user_ids","resource_ids"]', '["platform"]', 'unbound', 1);

INSERT INTO iam_permissions (id, resource_id, code, action, name, status, supported_scopes, supported_context_types, binding_state, protected) VALUES (141, 28, 'identity.session.roles.read', 'roles.read', 'identity.session.roles.read', 'draft', '["all","self","user_ids","resource_ids"]', '["platform"]', 'unbound', 1);

INSERT INTO iam_permissions (id, resource_id, code, action, name, status, supported_scopes, supported_context_types, binding_state, protected) VALUES (142, 28, 'identity.session.self.revoke', 'self.revoke', 'identity.session.self.revoke', 'draft', '["all","self","user_ids","resource_ids"]', '["platform"]', 'unbound', 1);

INSERT INTO iam_permissions (id, resource_id, code, action, name, status, supported_scopes, supported_context_types, binding_state, protected) VALUES (143, 29, 'identity.user.contact.read', 'contact.read', 'identity.user.contact.read', 'draft', '["all","self","user_ids","resource_ids","routing_group_ids"]', '["platform"]', 'unbound', 0);

INSERT INTO iam_permissions (id, resource_id, code, action, name, status, supported_scopes, supported_context_types, binding_state, protected) VALUES (144, 29, 'identity.user.create', 'create', 'identity.user.create', 'draft', '["all","self","user_ids","resource_ids","routing_group_ids"]', '["platform"]', 'unbound', 0);

INSERT INTO iam_permissions (id, resource_id, code, action, name, status, supported_scopes, supported_context_types, binding_state, protected) VALUES (145, 29, 'identity.user.credential.update', 'credential.update', 'identity.user.credential.update', 'draft', '["all","self","user_ids","resource_ids","routing_group_ids"]', '["platform"]', 'unbound', 0);

INSERT INTO iam_permissions (id, resource_id, code, action, name, status, supported_scopes, supported_context_types, binding_state, protected) VALUES (146, 29, 'identity.user.delete', 'delete', 'identity.user.delete', 'draft', '["all","self","user_ids","resource_ids","routing_group_ids"]', '["platform"]', 'unbound', 0);

INSERT INTO iam_permissions (id, resource_id, code, action, name, status, supported_scopes, supported_context_types, binding_state, protected) VALUES (147, 29, 'identity.user.disable', 'disable', 'identity.user.disable', 'draft', '["all","self","user_ids","resource_ids","routing_group_ids"]', '["platform"]', 'unbound', 0);

INSERT INTO iam_permissions (id, resource_id, code, action, name, status, supported_scopes, supported_context_types, binding_state, protected) VALUES (148, 29, 'identity.user.email_binding.update', 'email_binding.update', 'identity.user.email_binding.update', 'draft', '["all","self","user_ids","resource_ids","routing_group_ids"]', '["platform"]', 'unbound', 0);

INSERT INTO iam_permissions (id, resource_id, code, action, name, status, supported_scopes, supported_context_types, binding_state, protected) VALUES (149, 29, 'identity.user.enable', 'enable', 'identity.user.enable', 'draft', '["all","self","user_ids","resource_ids","routing_group_ids"]', '["platform"]', 'unbound', 0);

INSERT INTO iam_permissions (id, resource_id, code, action, name, status, supported_scopes, supported_context_types, binding_state, protected) VALUES (150, 29, 'identity.user.export', 'export', 'identity.user.export', 'draft', '["all","self","user_ids","resource_ids","routing_group_ids"]', '["platform"]', 'unbound', 0);

INSERT INTO iam_permissions (id, resource_id, code, action, name, status, supported_scopes, supported_context_types, binding_state, protected) VALUES (151, 29, 'identity.user.list', 'list', 'identity.user.list', 'draft', '["all","self","user_ids","resource_ids","routing_group_ids"]', '["platform"]', 'unbound', 0);

INSERT INTO iam_permissions (id, resource_id, code, action, name, status, supported_scopes, supported_context_types, binding_state, protected) VALUES (152, 29, 'identity.user.read', 'read', 'identity.user.read', 'draft', '["all","self","user_ids","resource_ids","routing_group_ids"]', '["platform"]', 'unbound', 0);

INSERT INTO iam_permissions (id, resource_id, code, action, name, status, supported_scopes, supported_context_types, binding_state, protected) VALUES (153, 29, 'identity.user.sessions.revoke', 'sessions.revoke', 'identity.user.sessions.revoke', 'draft', '["all","self","user_ids","resource_ids","routing_group_ids"]', '["platform"]', 'unbound', 0);

INSERT INTO iam_permissions (id, resource_id, code, action, name, status, supported_scopes, supported_context_types, binding_state, protected) VALUES (154, 29, 'identity.user.update', 'update', 'identity.user.update', 'draft', '["all","self","user_ids","resource_ids","routing_group_ids"]', '["platform"]', 'unbound', 0);

INSERT INTO iam_permissions (id, resource_id, code, action, name, status, supported_scopes, supported_context_types, binding_state, protected) VALUES (155, 30, 'identity.user_role.assign', 'assign', 'identity.user_role.assign', 'draft', '["all","resource_ids"]', '["platform"]', 'unbound', 0);

INSERT INTO iam_permissions (id, resource_id, code, action, name, status, supported_scopes, supported_context_types, binding_state, protected) VALUES (156, 30, 'identity.user_role.batch_assign', 'batch_assign', 'identity.user_role.batch_assign', 'draft', '["all","resource_ids"]', '["platform"]', 'unbound', 0);

INSERT INTO iam_permissions (id, resource_id, code, action, name, status, supported_scopes, supported_context_types, binding_state, protected) VALUES (157, 30, 'identity.user_role.read', 'read', 'identity.user_role.read', 'draft', '["all","resource_ids"]', '["platform"]', 'unbound', 0);

INSERT INTO iam_permissions (id, resource_id, code, action, name, status, supported_scopes, supported_context_types, binding_state, protected) VALUES (158, 30, 'identity.user_role.revoke', 'revoke', 'identity.user_role.revoke', 'draft', '["all","resource_ids"]', '["platform"]', 'unbound', 0);

INSERT INTO iam_permissions (id, resource_id, code, action, name, status, supported_scopes, supported_context_types, binding_state, protected) VALUES (159, 31, 'log.request.content.read', 'content.read', 'log.request.content.read', 'draft', '["all","self","user_ids","resource_ids"]', '["platform"]', 'unbound', 0);

INSERT INTO iam_permissions (id, resource_id, code, action, name, status, supported_scopes, supported_context_types, binding_state, protected) VALUES (160, 31, 'log.request.delete', 'delete', 'log.request.delete', 'draft', '["all","self","user_ids","resource_ids"]', '["platform"]', 'unbound', 0);

INSERT INTO iam_permissions (id, resource_id, code, action, name, status, supported_scopes, supported_context_types, binding_state, protected) VALUES (161, 31, 'log.request.export', 'export', 'log.request.export', 'draft', '["all","self","user_ids","resource_ids"]', '["platform"]', 'unbound', 0);

INSERT INTO iam_permissions (id, resource_id, code, action, name, status, supported_scopes, supported_context_types, binding_state, protected) VALUES (162, 31, 'log.request.list', 'list', 'log.request.list', 'draft', '["all","self","user_ids","resource_ids"]', '["platform"]', 'unbound', 0);

INSERT INTO iam_permissions (id, resource_id, code, action, name, status, supported_scopes, supported_context_types, binding_state, protected) VALUES (163, 31, 'log.request.purge', 'purge', 'log.request.purge', 'draft', '["all","self","user_ids","resource_ids"]', '["platform"]', 'unbound', 0);

INSERT INTO iam_permissions (id, resource_id, code, action, name, status, supported_scopes, supported_context_types, binding_state, protected) VALUES (164, 31, 'log.request.read', 'read', 'log.request.read', 'draft', '["all","self","user_ids","resource_ids"]', '["platform"]', 'unbound', 0);

INSERT INTO iam_permissions (id, resource_id, code, action, name, status, supported_scopes, supported_context_types, binding_state, protected) VALUES (165, 31, 'log.request.stats.read', 'stats.read', 'log.request.stats.read', 'draft', '["all","self","user_ids","resource_ids"]', '["platform"]', 'unbound', 0);

INSERT INTO iam_permissions (id, resource_id, code, action, name, status, supported_scopes, supported_context_types, binding_state, protected) VALUES (166, 32, 'log.selection_event.list', 'list', 'log.selection_event.list', 'draft', '["all","self","user_ids","resource_ids"]', '["platform"]', 'unbound', 0);

INSERT INTO iam_permissions (id, resource_id, code, action, name, status, supported_scopes, supported_context_types, binding_state, protected) VALUES (167, 33, 'monitor.alert_rule.create', 'create', 'monitor.alert_rule.create', 'draft', '["all","resource_ids"]', '["platform"]', 'unbound', 0);

INSERT INTO iam_permissions (id, resource_id, code, action, name, status, supported_scopes, supported_context_types, binding_state, protected) VALUES (168, 33, 'monitor.alert_rule.delete', 'delete', 'monitor.alert_rule.delete', 'draft', '["all","resource_ids"]', '["platform"]', 'unbound', 0);

INSERT INTO iam_permissions (id, resource_id, code, action, name, status, supported_scopes, supported_context_types, binding_state, protected) VALUES (169, 33, 'monitor.alert_rule.list', 'list', 'monitor.alert_rule.list', 'draft', '["all","resource_ids"]', '["platform"]', 'unbound', 0);

INSERT INTO iam_permissions (id, resource_id, code, action, name, status, supported_scopes, supported_context_types, binding_state, protected) VALUES (170, 33, 'monitor.alert_rule.read', 'read', 'monitor.alert_rule.read', 'draft', '["all","resource_ids"]', '["platform"]', 'unbound', 0);

INSERT INTO iam_permissions (id, resource_id, code, action, name, status, supported_scopes, supported_context_types, binding_state, protected) VALUES (171, 33, 'monitor.alert_rule.update', 'update', 'monitor.alert_rule.update', 'draft', '["all","resource_ids"]', '["platform"]', 'unbound', 0);

INSERT INTO iam_permissions (id, resource_id, code, action, name, status, supported_scopes, supported_context_types, binding_state, protected) VALUES (172, 34, 'monitor.health.channel.read', 'channel.read', 'monitor.health.channel.read', 'draft', '["all","resource_ids","routing_group_ids"]', '["platform"]', 'unbound', 0);

INSERT INTO iam_permissions (id, resource_id, code, action, name, status, supported_scopes, supported_context_types, binding_state, protected) VALUES (173, 34, 'monitor.health.model.read', 'model.read', 'monitor.health.model.read', 'draft', '["all","resource_ids","routing_group_ids"]', '["platform"]', 'unbound', 0);

INSERT INTO iam_permissions (id, resource_id, code, action, name, status, supported_scopes, supported_context_types, binding_state, protected) VALUES (174, 34, 'monitor.health.selector.read', 'selector.read', 'monitor.health.selector.read', 'draft', '["all","resource_ids","routing_group_ids"]', '["platform"]', 'unbound', 0);

INSERT INTO iam_permissions (id, resource_id, code, action, name, status, supported_scopes, supported_context_types, binding_state, protected) VALUES (175, 35, 'monitor.health.service.read', 'read', 'monitor.health.service.read', 'draft', '["all","resource_ids"]', '["platform"]', 'unbound', 0);

INSERT INTO iam_permissions (id, resource_id, code, action, name, status, supported_scopes, supported_context_types, binding_state, protected) VALUES (176, 36, 'notify.notification.acknowledge', 'acknowledge', 'notify.notification.acknowledge', 'draft', '["all","resource_ids"]', '["platform"]', 'unbound', 0);

INSERT INTO iam_permissions (id, resource_id, code, action, name, status, supported_scopes, supported_context_types, binding_state, protected) VALUES (177, 36, 'notify.notification.list', 'list', 'notify.notification.list', 'draft', '["all","resource_ids"]', '["platform"]', 'unbound', 0);

INSERT INTO iam_permissions (id, resource_id, code, action, name, status, supported_scopes, supported_context_types, binding_state, protected) VALUES (178, 36, 'notify.notification.read', 'read', 'notify.notification.read', 'draft', '["all","resource_ids"]', '["platform"]', 'unbound', 0);

INSERT INTO iam_permissions (id, resource_id, code, action, name, status, supported_scopes, supported_context_types, binding_state, protected) VALUES (179, 36, 'notify.notification.rules.update', 'rules.update', 'notify.notification.rules.update', 'draft', '["all","resource_ids"]', '["platform"]', 'unbound', 0);

INSERT INTO iam_permissions (id, resource_id, code, action, name, status, supported_scopes, supported_context_types, binding_state, protected) VALUES (180, 36, 'notify.notification.test', 'test', 'notify.notification.test', 'draft', '["all","resource_ids"]', '["platform"]', 'unbound', 0);

INSERT INTO iam_permissions (id, resource_id, code, action, name, status, supported_scopes, supported_context_types, binding_state, protected) VALUES (181, 37, 'organization.member.contact.read', 'contact.read', 'organization.member.contact.read', 'draft', '["all","resource_ids"]', '["organization"]', 'unbound', 0);

INSERT INTO iam_permissions (id, resource_id, code, action, name, status, supported_scopes, supported_context_types, binding_state, protected) VALUES (182, 37, 'organization.member.export', 'export', 'organization.member.export', 'draft', '["all","resource_ids"]', '["organization"]', 'unbound', 0);

INSERT INTO iam_permissions (id, resource_id, code, action, name, status, supported_scopes, supported_context_types, binding_state, protected) VALUES (183, 37, 'organization.member.invite', 'invite', 'organization.member.invite', 'draft', '["all","resource_ids"]', '["organization"]', 'unbound', 0);

INSERT INTO iam_permissions (id, resource_id, code, action, name, status, supported_scopes, supported_context_types, binding_state, protected) VALUES (184, 37, 'organization.member.list', 'list', 'organization.member.list', 'draft', '["all","resource_ids"]', '["organization"]', 'unbound', 0);

INSERT INTO iam_permissions (id, resource_id, code, action, name, status, supported_scopes, supported_context_types, binding_state, protected) VALUES (185, 37, 'organization.member.read', 'read', 'organization.member.read', 'draft', '["all","resource_ids"]', '["organization"]', 'unbound', 0);

INSERT INTO iam_permissions (id, resource_id, code, action, name, status, supported_scopes, supported_context_types, binding_state, protected) VALUES (186, 37, 'organization.member.remove', 'remove', 'organization.member.remove', 'draft', '["all","resource_ids"]', '["organization"]', 'unbound', 0);

INSERT INTO iam_permissions (id, resource_id, code, action, name, status, supported_scopes, supported_context_types, binding_state, protected) VALUES (187, 37, 'organization.member.restore', 'restore', 'organization.member.restore', 'draft', '["all","resource_ids"]', '["organization"]', 'unbound', 0);

INSERT INTO iam_permissions (id, resource_id, code, action, name, status, supported_scopes, supported_context_types, binding_state, protected) VALUES (188, 37, 'organization.member.suspend', 'suspend', 'organization.member.suspend', 'draft', '["all","resource_ids"]', '["organization"]', 'unbound', 0);

INSERT INTO iam_permissions (id, resource_id, code, action, name, status, supported_scopes, supported_context_types, binding_state, protected) VALUES (189, 37, 'organization.member.update', 'update', 'organization.member.update', 'draft', '["all","resource_ids"]', '["organization"]', 'unbound', 0);

INSERT INTO iam_permissions (id, resource_id, code, action, name, status, supported_scopes, supported_context_types, binding_state, protected) VALUES (190, 38, 'organization.member_role.assign', 'assign', 'organization.member_role.assign', 'draft', '["all","resource_ids"]', '["organization"]', 'unbound', 0);

INSERT INTO iam_permissions (id, resource_id, code, action, name, status, supported_scopes, supported_context_types, binding_state, protected) VALUES (191, 38, 'organization.member_role.batch_assign', 'batch_assign', 'organization.member_role.batch_assign', 'draft', '["all","resource_ids"]', '["organization"]', 'unbound', 0);

INSERT INTO iam_permissions (id, resource_id, code, action, name, status, supported_scopes, supported_context_types, binding_state, protected) VALUES (192, 38, 'organization.member_role.read', 'read', 'organization.member_role.read', 'draft', '["all","resource_ids"]', '["organization"]', 'unbound', 0);

INSERT INTO iam_permissions (id, resource_id, code, action, name, status, supported_scopes, supported_context_types, binding_state, protected) VALUES (193, 38, 'organization.member_role.revoke', 'revoke', 'organization.member_role.revoke', 'draft', '["all","resource_ids"]', '["organization"]', 'unbound', 0);

INSERT INTO iam_permissions (id, resource_id, code, action, name, status, supported_scopes, supported_context_types, binding_state, protected) VALUES (194, 39, 'organization.organization.archive', 'archive', 'organization.organization.archive', 'draft', '["all","resource_ids"]', '["platform"]', 'unbound', 0);

INSERT INTO iam_permissions (id, resource_id, code, action, name, status, supported_scopes, supported_context_types, binding_state, protected) VALUES (195, 39, 'organization.organization.create', 'create', 'organization.organization.create', 'draft', '["all","resource_ids"]', '["platform"]', 'unbound', 0);

INSERT INTO iam_permissions (id, resource_id, code, action, name, status, supported_scopes, supported_context_types, binding_state, protected) VALUES (196, 39, 'organization.organization.disable', 'disable', 'organization.organization.disable', 'draft', '["all","resource_ids"]', '["platform"]', 'unbound', 0);

INSERT INTO iam_permissions (id, resource_id, code, action, name, status, supported_scopes, supported_context_types, binding_state, protected) VALUES (197, 39, 'organization.organization.enable', 'enable', 'organization.organization.enable', 'draft', '["all","resource_ids"]', '["platform"]', 'unbound', 0);

INSERT INTO iam_permissions (id, resource_id, code, action, name, status, supported_scopes, supported_context_types, binding_state, protected) VALUES (198, 39, 'organization.organization.list', 'list', 'organization.organization.list', 'draft', '["all","resource_ids"]', '["platform"]', 'unbound', 0);

INSERT INTO iam_permissions (id, resource_id, code, action, name, status, supported_scopes, supported_context_types, binding_state, protected) VALUES (199, 39, 'organization.organization.ownership.transfer', 'ownership.transfer', 'organization.organization.ownership.transfer', 'draft', '["all","resource_ids"]', '["platform"]', 'unbound', 0);

INSERT INTO iam_permissions (id, resource_id, code, action, name, status, supported_scopes, supported_context_types, binding_state, protected) VALUES (200, 39, 'organization.organization.profile.update', 'profile.update', 'organization.organization.profile.update', 'draft', '["all","resource_ids"]', '["platform"]', 'unbound', 0);

INSERT INTO iam_permissions (id, resource_id, code, action, name, status, supported_scopes, supported_context_types, binding_state, protected) VALUES (201, 39, 'organization.organization.read', 'read', 'organization.organization.read', 'draft', '["all","resource_ids"]', '["platform"]', 'unbound', 0);

INSERT INTO iam_permissions (id, resource_id, code, action, name, status, supported_scopes, supported_context_types, binding_state, protected) VALUES (202, 40, 'organization.unit.archive', 'archive', 'organization.unit.archive', 'draft', '["all","resource_ids"]', '["organization"]', 'unbound', 0);

INSERT INTO iam_permissions (id, resource_id, code, action, name, status, supported_scopes, supported_context_types, binding_state, protected) VALUES (203, 40, 'organization.unit.create', 'create', 'organization.unit.create', 'draft', '["all","resource_ids"]', '["organization"]', 'unbound', 0);

INSERT INTO iam_permissions (id, resource_id, code, action, name, status, supported_scopes, supported_context_types, binding_state, protected) VALUES (204, 40, 'organization.unit.list', 'list', 'organization.unit.list', 'draft', '["all","resource_ids"]', '["organization"]', 'unbound', 0);

INSERT INTO iam_permissions (id, resource_id, code, action, name, status, supported_scopes, supported_context_types, binding_state, protected) VALUES (205, 40, 'organization.unit.members.read', 'members.read', 'organization.unit.members.read', 'draft', '["all","resource_ids"]', '["organization"]', 'unbound', 0);

INSERT INTO iam_permissions (id, resource_id, code, action, name, status, supported_scopes, supported_context_types, binding_state, protected) VALUES (206, 40, 'organization.unit.members.update', 'members.update', 'organization.unit.members.update', 'draft', '["all","resource_ids"]', '["organization"]', 'unbound', 0);

INSERT INTO iam_permissions (id, resource_id, code, action, name, status, supported_scopes, supported_context_types, binding_state, protected) VALUES (207, 40, 'organization.unit.move', 'move', 'organization.unit.move', 'draft', '["all","resource_ids"]', '["organization"]', 'unbound', 0);

INSERT INTO iam_permissions (id, resource_id, code, action, name, status, supported_scopes, supported_context_types, binding_state, protected) VALUES (208, 40, 'organization.unit.read', 'read', 'organization.unit.read', 'draft', '["all","resource_ids"]', '["organization"]', 'unbound', 0);

INSERT INTO iam_permissions (id, resource_id, code, action, name, status, supported_scopes, supported_context_types, binding_state, protected) VALUES (209, 40, 'organization.unit.update', 'update', 'organization.unit.update', 'draft', '["all","resource_ids"]', '["organization"]', 'unbound', 0);

INSERT INTO iam_permissions (id, resource_id, code, action, name, status, supported_scopes, supported_context_types, binding_state, protected) VALUES (210, 41, 'subscription.plan.create', 'create', 'subscription.plan.create', 'draft', '["all","resource_ids"]', '["platform"]', 'unbound', 0);

INSERT INTO iam_permissions (id, resource_id, code, action, name, status, supported_scopes, supported_context_types, binding_state, protected) VALUES (211, 41, 'subscription.plan.delete', 'delete', 'subscription.plan.delete', 'draft', '["all","resource_ids"]', '["platform"]', 'unbound', 0);

INSERT INTO iam_permissions (id, resource_id, code, action, name, status, supported_scopes, supported_context_types, binding_state, protected) VALUES (212, 41, 'subscription.plan.list', 'list', 'subscription.plan.list', 'draft', '["all","resource_ids"]', '["platform"]', 'unbound', 0);

INSERT INTO iam_permissions (id, resource_id, code, action, name, status, supported_scopes, supported_context_types, binding_state, protected) VALUES (213, 41, 'subscription.plan.publish', 'publish', 'subscription.plan.publish', 'draft', '["all","resource_ids"]', '["platform"]', 'unbound', 0);

INSERT INTO iam_permissions (id, resource_id, code, action, name, status, supported_scopes, supported_context_types, binding_state, protected) VALUES (214, 41, 'subscription.plan.read', 'read', 'subscription.plan.read', 'draft', '["all","resource_ids"]', '["platform"]', 'unbound', 0);

INSERT INTO iam_permissions (id, resource_id, code, action, name, status, supported_scopes, supported_context_types, binding_state, protected) VALUES (215, 41, 'subscription.plan.unpublish', 'unpublish', 'subscription.plan.unpublish', 'draft', '["all","resource_ids"]', '["platform"]', 'unbound', 0);

INSERT INTO iam_permissions (id, resource_id, code, action, name, status, supported_scopes, supported_context_types, binding_state, protected) VALUES (216, 41, 'subscription.plan.update', 'update', 'subscription.plan.update', 'draft', '["all","resource_ids"]', '["platform"]', 'unbound', 0);

INSERT INTO iam_permissions (id, resource_id, code, action, name, status, supported_scopes, supported_context_types, binding_state, protected) VALUES (217, 42, 'subscription.quota_policy.create', 'create', 'subscription.quota_policy.create', 'draft', '["all","resource_ids"]', '["platform"]', 'unbound', 0);

INSERT INTO iam_permissions (id, resource_id, code, action, name, status, supported_scopes, supported_context_types, binding_state, protected) VALUES (218, 42, 'subscription.quota_policy.delete', 'delete', 'subscription.quota_policy.delete', 'draft', '["all","resource_ids"]', '["platform"]', 'unbound', 0);

INSERT INTO iam_permissions (id, resource_id, code, action, name, status, supported_scopes, supported_context_types, binding_state, protected) VALUES (219, 42, 'subscription.quota_policy.list', 'list', 'subscription.quota_policy.list', 'draft', '["all","resource_ids"]', '["platform"]', 'unbound', 0);

INSERT INTO iam_permissions (id, resource_id, code, action, name, status, supported_scopes, supported_context_types, binding_state, protected) VALUES (220, 42, 'subscription.quota_policy.read', 'read', 'subscription.quota_policy.read', 'draft', '["all","resource_ids"]', '["platform"]', 'unbound', 0);

INSERT INTO iam_permissions (id, resource_id, code, action, name, status, supported_scopes, supported_context_types, binding_state, protected) VALUES (221, 42, 'subscription.quota_policy.update', 'update', 'subscription.quota_policy.update', 'draft', '["all","resource_ids"]', '["platform"]', 'unbound', 0);

INSERT INTO iam_permissions (id, resource_id, code, action, name, status, supported_scopes, supported_context_types, binding_state, protected) VALUES (222, 43, 'subscription.user_subscription.assign', 'assign', 'subscription.user_subscription.assign', 'draft', '["all","self","user_ids","resource_ids"]', '["platform"]', 'unbound', 0);

INSERT INTO iam_permissions (id, resource_id, code, action, name, status, supported_scopes, supported_context_types, binding_state, protected) VALUES (223, 43, 'subscription.user_subscription.change', 'change', 'subscription.user_subscription.change', 'draft', '["all","self","user_ids","resource_ids"]', '["platform"]', 'unbound', 0);

INSERT INTO iam_permissions (id, resource_id, code, action, name, status, supported_scopes, supported_context_types, binding_state, protected) VALUES (224, 43, 'subscription.user_subscription.extend', 'extend', 'subscription.user_subscription.extend', 'draft', '["all","self","user_ids","resource_ids"]', '["platform"]', 'unbound', 0);

INSERT INTO iam_permissions (id, resource_id, code, action, name, status, supported_scopes, supported_context_types, binding_state, protected) VALUES (225, 43, 'subscription.user_subscription.list', 'list', 'subscription.user_subscription.list', 'draft', '["all","self","user_ids","resource_ids"]', '["platform"]', 'unbound', 0);

INSERT INTO iam_permissions (id, resource_id, code, action, name, status, supported_scopes, supported_context_types, binding_state, protected) VALUES (226, 43, 'subscription.user_subscription.quota.reset', 'quota.reset', 'subscription.user_subscription.quota.reset', 'draft', '["all","self","user_ids","resource_ids"]', '["platform"]', 'unbound', 0);

INSERT INTO iam_permissions (id, resource_id, code, action, name, status, supported_scopes, supported_context_types, binding_state, protected) VALUES (227, 43, 'subscription.user_subscription.read', 'read', 'subscription.user_subscription.read', 'draft', '["all","self","user_ids","resource_ids"]', '["platform"]', 'unbound', 0);

INSERT INTO iam_permissions (id, resource_id, code, action, name, status, supported_scopes, supported_context_types, binding_state, protected) VALUES (228, 43, 'subscription.user_subscription.report.read', 'report.read', 'subscription.user_subscription.report.read', 'draft', '["all","self","user_ids","resource_ids"]', '["platform"]', 'unbound', 0);

INSERT INTO iam_permissions (id, resource_id, code, action, name, status, supported_scopes, supported_context_types, binding_state, protected) VALUES (229, 43, 'subscription.user_subscription.revoke', 'revoke', 'subscription.user_subscription.revoke', 'draft', '["all","self","user_ids","resource_ids"]', '["platform"]', 'unbound', 0);

INSERT INTO iam_permissions (id, resource_id, code, action, name, status, supported_scopes, supported_context_types, binding_state, protected) VALUES (230, 44, 'system.content.about.update', 'about.update', 'system.content.about.update', 'draft', '["all"]', '["platform"]', 'unbound', 0);

INSERT INTO iam_permissions (id, resource_id, code, action, name, status, supported_scopes, supported_context_types, binding_state, protected) VALUES (231, 44, 'system.content.home.update', 'home.update', 'system.content.home.update', 'draft', '["all"]', '["platform"]', 'unbound', 0);

INSERT INTO iam_permissions (id, resource_id, code, action, name, status, supported_scopes, supported_context_types, binding_state, protected) VALUES (232, 44, 'system.content.notice.update', 'notice.update', 'system.content.notice.update', 'draft', '["all"]', '["platform"]', 'unbound', 0);

INSERT INTO iam_permissions (id, resource_id, code, action, name, status, supported_scopes, supported_context_types, binding_state, protected) VALUES (233, 45, 'system.option.payment.update', 'payment.update', 'system.option.payment.update', 'draft', '["all"]', '["platform"]', 'unbound', 0);

INSERT INTO iam_permissions (id, resource_id, code, action, name, status, supported_scopes, supported_context_types, binding_state, protected) VALUES (234, 45, 'system.option.pricing.update', 'pricing.update', 'system.option.pricing.update', 'draft', '["all"]', '["platform"]', 'unbound', 0);

INSERT INTO iam_permissions (id, resource_id, code, action, name, status, supported_scopes, supported_context_types, binding_state, protected) VALUES (235, 45, 'system.option.read', 'read', 'system.option.read', 'draft', '["all"]', '["platform"]', 'unbound', 0);

INSERT INTO iam_permissions (id, resource_id, code, action, name, status, supported_scopes, supported_context_types, binding_state, protected) VALUES (236, 45, 'system.option.security.update', 'security.update', 'system.option.security.update', 'draft', '["all"]', '["platform"]', 'unbound', 0);

INSERT INTO iam_permissions (id, resource_id, code, action, name, status, supported_scopes, supported_context_types, binding_state, protected) VALUES (237, 45, 'system.option.update', 'update', 'system.option.update', 'draft', '["all"]', '["platform"]', 'unbound', 0);

INSERT INTO iam_roles (id, context_type, organization_id, context_key, code, name, description, status, builtin) VALUES (1, 'platform', 0, 'platform', 'guest', 'guest', '', 'enabled', 1);

INSERT INTO iam_roles (id, context_type, organization_id, context_key, code, name, description, status, builtin) VALUES (2, 'platform', 0, 'platform', 'member', 'member', '', 'enabled', 1);

INSERT INTO iam_roles (id, context_type, organization_id, context_key, code, name, description, status, builtin) VALUES (3, 'platform', 0, 'platform', 'platform_admin', 'platform_admin', '', 'enabled', 1);

INSERT INTO iam_roles (id, context_type, organization_id, context_key, code, name, description, status, builtin) VALUES (4, 'platform', 0, 'platform', 'root', 'root', '', 'enabled', 1);

INSERT INTO iam_role_permissions (context_key, role_id, permission_id, effect, scope_descriptor) VALUES ('platform', 4, 1, 'allow', '{"clauses":[{"all":true}]}');

INSERT INTO iam_role_permissions (context_key, role_id, permission_id, effect, scope_descriptor) VALUES ('platform', 4, 2, 'allow', '{"clauses":[{"all":true}]}');

INSERT INTO iam_role_permissions (context_key, role_id, permission_id, effect, scope_descriptor) VALUES ('platform', 4, 3, 'allow', '{"clauses":[{"all":true}]}');

INSERT INTO iam_role_permissions (context_key, role_id, permission_id, effect, scope_descriptor) VALUES ('platform', 4, 4, 'allow', '{"clauses":[{"all":true}]}');

INSERT INTO iam_role_permissions (context_key, role_id, permission_id, effect, scope_descriptor) VALUES ('platform', 4, 5, 'allow', '{"clauses":[{"all":true}]}');

INSERT INTO iam_role_permissions (context_key, role_id, permission_id, effect, scope_descriptor) VALUES ('platform', 4, 6, 'allow', '{"clauses":[{"all":true}]}');

INSERT INTO iam_role_permissions (context_key, role_id, permission_id, effect, scope_descriptor) VALUES ('platform', 4, 7, 'allow', '{"clauses":[{"all":true}]}');

INSERT INTO iam_role_permissions (context_key, role_id, permission_id, effect, scope_descriptor) VALUES ('platform', 4, 8, 'allow', '{"clauses":[{"all":true}]}');

INSERT INTO iam_role_permissions (context_key, role_id, permission_id, effect, scope_descriptor) VALUES ('platform', 4, 9, 'allow', '{"clauses":[{"all":true}]}');

INSERT INTO iam_role_permissions (context_key, role_id, permission_id, effect, scope_descriptor) VALUES ('platform', 4, 10, 'allow', '{"clauses":[{"all":true}]}');

INSERT INTO iam_role_permissions (context_key, role_id, permission_id, effect, scope_descriptor) VALUES ('platform', 4, 11, 'allow', '{"clauses":[{"all":true}]}');

INSERT INTO iam_role_permissions (context_key, role_id, permission_id, effect, scope_descriptor) VALUES ('platform', 4, 12, 'allow', '{"clauses":[{"all":true}]}');

INSERT INTO iam_role_permissions (context_key, role_id, permission_id, effect, scope_descriptor) VALUES ('platform', 4, 13, 'allow', '{"clauses":[{"all":true}]}');

INSERT INTO iam_role_permissions (context_key, role_id, permission_id, effect, scope_descriptor) VALUES ('platform', 4, 14, 'allow', '{"clauses":[{"all":true}]}');

INSERT INTO iam_role_permissions (context_key, role_id, permission_id, effect, scope_descriptor) VALUES ('platform', 4, 15, 'allow', '{"clauses":[{"all":true}]}');

INSERT INTO iam_role_permissions (context_key, role_id, permission_id, effect, scope_descriptor) VALUES ('platform', 4, 16, 'allow', '{"clauses":[{"all":true}]}');

INSERT INTO iam_role_permissions (context_key, role_id, permission_id, effect, scope_descriptor) VALUES ('platform', 4, 17, 'allow', '{"clauses":[{"all":true}]}');

INSERT INTO iam_role_permissions (context_key, role_id, permission_id, effect, scope_descriptor) VALUES ('platform', 4, 18, 'allow', '{"clauses":[{"all":true}]}');

INSERT INTO iam_role_permissions (context_key, role_id, permission_id, effect, scope_descriptor) VALUES ('platform', 4, 19, 'allow', '{"clauses":[{"all":true}]}');

INSERT INTO iam_role_permissions (context_key, role_id, permission_id, effect, scope_descriptor) VALUES ('platform', 4, 20, 'allow', '{"clauses":[{"all":true}]}');

INSERT INTO iam_role_permissions (context_key, role_id, permission_id, effect, scope_descriptor) VALUES ('platform', 4, 21, 'allow', '{"clauses":[{"all":true}]}');

INSERT INTO iam_role_permissions (context_key, role_id, permission_id, effect, scope_descriptor) VALUES ('platform', 4, 22, 'allow', '{"clauses":[{"all":true}]}');

INSERT INTO iam_role_permissions (context_key, role_id, permission_id, effect, scope_descriptor) VALUES ('platform', 4, 23, 'allow', '{"clauses":[{"all":true}]}');

INSERT INTO iam_role_permissions (context_key, role_id, permission_id, effect, scope_descriptor) VALUES ('platform', 4, 24, 'allow', '{"clauses":[{"all":true}]}');

INSERT INTO iam_role_permissions (context_key, role_id, permission_id, effect, scope_descriptor) VALUES ('platform', 4, 25, 'allow', '{"clauses":[{"all":true}]}');

INSERT INTO iam_role_permissions (context_key, role_id, permission_id, effect, scope_descriptor) VALUES ('platform', 4, 26, 'allow', '{"clauses":[{"all":true}]}');

INSERT INTO iam_role_permissions (context_key, role_id, permission_id, effect, scope_descriptor) VALUES ('platform', 4, 27, 'allow', '{"clauses":[{"all":true}]}');

INSERT INTO iam_role_permissions (context_key, role_id, permission_id, effect, scope_descriptor) VALUES ('platform', 4, 28, 'allow', '{"clauses":[{"all":true}]}');

INSERT INTO iam_role_permissions (context_key, role_id, permission_id, effect, scope_descriptor) VALUES ('platform', 4, 29, 'allow', '{"clauses":[{"all":true}]}');

INSERT INTO iam_role_permissions (context_key, role_id, permission_id, effect, scope_descriptor) VALUES ('platform', 4, 30, 'allow', '{"clauses":[{"all":true}]}');

INSERT INTO iam_role_permissions (context_key, role_id, permission_id, effect, scope_descriptor) VALUES ('platform', 4, 31, 'allow', '{"clauses":[{"all":true}]}');

INSERT INTO iam_role_permissions (context_key, role_id, permission_id, effect, scope_descriptor) VALUES ('platform', 4, 32, 'allow', '{"clauses":[{"all":true}]}');

INSERT INTO iam_role_permissions (context_key, role_id, permission_id, effect, scope_descriptor) VALUES ('platform', 4, 33, 'allow', '{"clauses":[{"all":true}]}');

INSERT INTO iam_role_permissions (context_key, role_id, permission_id, effect, scope_descriptor) VALUES ('platform', 4, 34, 'allow', '{"clauses":[{"all":true}]}');

INSERT INTO iam_role_permissions (context_key, role_id, permission_id, effect, scope_descriptor) VALUES ('platform', 4, 35, 'allow', '{"clauses":[{"all":true}]}');

INSERT INTO iam_role_permissions (context_key, role_id, permission_id, effect, scope_descriptor) VALUES ('platform', 4, 36, 'allow', '{"clauses":[{"all":true}]}');

INSERT INTO iam_role_permissions (context_key, role_id, permission_id, effect, scope_descriptor) VALUES ('platform', 4, 37, 'allow', '{"clauses":[{"all":true}]}');

INSERT INTO iam_role_permissions (context_key, role_id, permission_id, effect, scope_descriptor) VALUES ('platform', 4, 38, 'allow', '{"clauses":[{"all":true}]}');

INSERT INTO iam_role_permissions (context_key, role_id, permission_id, effect, scope_descriptor) VALUES ('platform', 4, 39, 'allow', '{"clauses":[{"all":true}]}');

INSERT INTO iam_role_permissions (context_key, role_id, permission_id, effect, scope_descriptor) VALUES ('platform', 4, 40, 'allow', '{"clauses":[{"all":true}]}');

INSERT INTO iam_role_permissions (context_key, role_id, permission_id, effect, scope_descriptor) VALUES ('platform', 4, 41, 'allow', '{"clauses":[{"all":true}]}');

INSERT INTO iam_role_permissions (context_key, role_id, permission_id, effect, scope_descriptor) VALUES ('platform', 4, 42, 'allow', '{"clauses":[{"all":true}]}');

INSERT INTO iam_role_permissions (context_key, role_id, permission_id, effect, scope_descriptor) VALUES ('platform', 4, 43, 'allow', '{"clauses":[{"all":true}]}');

INSERT INTO iam_role_permissions (context_key, role_id, permission_id, effect, scope_descriptor) VALUES ('platform', 4, 44, 'allow', '{"clauses":[{"all":true}]}');

INSERT INTO iam_role_permissions (context_key, role_id, permission_id, effect, scope_descriptor) VALUES ('platform', 4, 45, 'allow', '{"clauses":[{"all":true}]}');

INSERT INTO iam_role_permissions (context_key, role_id, permission_id, effect, scope_descriptor) VALUES ('platform', 4, 46, 'allow', '{"clauses":[{"all":true}]}');

INSERT INTO iam_role_permissions (context_key, role_id, permission_id, effect, scope_descriptor) VALUES ('platform', 4, 47, 'allow', '{"clauses":[{"all":true}]}');

INSERT INTO iam_role_permissions (context_key, role_id, permission_id, effect, scope_descriptor) VALUES ('platform', 4, 48, 'allow', '{"clauses":[{"all":true}]}');

INSERT INTO iam_role_permissions (context_key, role_id, permission_id, effect, scope_descriptor) VALUES ('platform', 4, 49, 'allow', '{"clauses":[{"all":true}]}');

INSERT INTO iam_role_permissions (context_key, role_id, permission_id, effect, scope_descriptor) VALUES ('platform', 4, 50, 'allow', '{"clauses":[{"all":true}]}');

INSERT INTO iam_role_permissions (context_key, role_id, permission_id, effect, scope_descriptor) VALUES ('platform', 4, 51, 'allow', '{"clauses":[{"all":true}]}');

INSERT INTO iam_role_permissions (context_key, role_id, permission_id, effect, scope_descriptor) VALUES ('platform', 4, 52, 'allow', '{"clauses":[{"all":true}]}');

INSERT INTO iam_role_permissions (context_key, role_id, permission_id, effect, scope_descriptor) VALUES ('platform', 4, 53, 'allow', '{"clauses":[{"all":true}]}');

INSERT INTO iam_role_permissions (context_key, role_id, permission_id, effect, scope_descriptor) VALUES ('platform', 4, 54, 'allow', '{"clauses":[{"all":true}]}');

INSERT INTO iam_role_permissions (context_key, role_id, permission_id, effect, scope_descriptor) VALUES ('platform', 4, 55, 'allow', '{"clauses":[{"all":true}]}');

INSERT INTO iam_role_permissions (context_key, role_id, permission_id, effect, scope_descriptor) VALUES ('platform', 4, 56, 'allow', '{"clauses":[{"all":true}]}');

INSERT INTO iam_role_permissions (context_key, role_id, permission_id, effect, scope_descriptor) VALUES ('platform', 4, 57, 'allow', '{"clauses":[{"all":true}]}');

INSERT INTO iam_role_permissions (context_key, role_id, permission_id, effect, scope_descriptor) VALUES ('platform', 4, 58, 'allow', '{"clauses":[{"all":true}]}');

INSERT INTO iam_role_permissions (context_key, role_id, permission_id, effect, scope_descriptor) VALUES ('platform', 4, 59, 'allow', '{"clauses":[{"all":true}]}');

INSERT INTO iam_role_permissions (context_key, role_id, permission_id, effect, scope_descriptor) VALUES ('platform', 4, 60, 'allow', '{"clauses":[{"all":true}]}');

INSERT INTO iam_role_permissions (context_key, role_id, permission_id, effect, scope_descriptor) VALUES ('platform', 4, 61, 'allow', '{"clauses":[{"all":true}]}');

INSERT INTO iam_role_permissions (context_key, role_id, permission_id, effect, scope_descriptor) VALUES ('platform', 4, 62, 'allow', '{"clauses":[{"all":true}]}');

INSERT INTO iam_role_permissions (context_key, role_id, permission_id, effect, scope_descriptor) VALUES ('platform', 4, 63, 'allow', '{"clauses":[{"all":true}]}');

INSERT INTO iam_role_permissions (context_key, role_id, permission_id, effect, scope_descriptor) VALUES ('platform', 4, 64, 'allow', '{"clauses":[{"all":true}]}');

INSERT INTO iam_role_permissions (context_key, role_id, permission_id, effect, scope_descriptor) VALUES ('platform', 4, 65, 'allow', '{"clauses":[{"all":true}]}');

INSERT INTO iam_role_permissions (context_key, role_id, permission_id, effect, scope_descriptor) VALUES ('platform', 4, 66, 'allow', '{"clauses":[{"all":true}]}');

INSERT INTO iam_role_permissions (context_key, role_id, permission_id, effect, scope_descriptor) VALUES ('platform', 4, 67, 'allow', '{"clauses":[{"all":true}]}');

INSERT INTO iam_role_permissions (context_key, role_id, permission_id, effect, scope_descriptor) VALUES ('platform', 4, 68, 'allow', '{"clauses":[{"all":true}]}');

INSERT INTO iam_role_permissions (context_key, role_id, permission_id, effect, scope_descriptor) VALUES ('platform', 4, 69, 'allow', '{"clauses":[{"all":true}]}');

INSERT INTO iam_role_permissions (context_key, role_id, permission_id, effect, scope_descriptor) VALUES ('platform', 4, 70, 'allow', '{"clauses":[{"all":true}]}');

INSERT INTO iam_role_permissions (context_key, role_id, permission_id, effect, scope_descriptor) VALUES ('platform', 4, 71, 'allow', '{"clauses":[{"all":true}]}');

INSERT INTO iam_role_permissions (context_key, role_id, permission_id, effect, scope_descriptor) VALUES ('platform', 4, 72, 'allow', '{"clauses":[{"all":true}]}');

INSERT INTO iam_role_permissions (context_key, role_id, permission_id, effect, scope_descriptor) VALUES ('platform', 4, 73, 'allow', '{"clauses":[{"all":true}]}');

INSERT INTO iam_role_permissions (context_key, role_id, permission_id, effect, scope_descriptor) VALUES ('platform', 4, 74, 'allow', '{"clauses":[{"all":true}]}');

INSERT INTO iam_role_permissions (context_key, role_id, permission_id, effect, scope_descriptor) VALUES ('platform', 4, 75, 'allow', '{"clauses":[{"all":true}]}');

INSERT INTO iam_role_permissions (context_key, role_id, permission_id, effect, scope_descriptor) VALUES ('platform', 4, 76, 'allow', '{"clauses":[{"all":true}]}');

INSERT INTO iam_role_permissions (context_key, role_id, permission_id, effect, scope_descriptor) VALUES ('platform', 4, 77, 'allow', '{"clauses":[{"all":true}]}');

INSERT INTO iam_role_permissions (context_key, role_id, permission_id, effect, scope_descriptor) VALUES ('platform', 4, 78, 'allow', '{"clauses":[{"all":true}]}');

INSERT INTO iam_role_permissions (context_key, role_id, permission_id, effect, scope_descriptor) VALUES ('platform', 4, 79, 'allow', '{"clauses":[{"all":true}]}');

INSERT INTO iam_role_permissions (context_key, role_id, permission_id, effect, scope_descriptor) VALUES ('platform', 4, 80, 'allow', '{"clauses":[{"all":true}]}');

INSERT INTO iam_role_permissions (context_key, role_id, permission_id, effect, scope_descriptor) VALUES ('platform', 4, 81, 'allow', '{"clauses":[{"all":true}]}');

INSERT INTO iam_role_permissions (context_key, role_id, permission_id, effect, scope_descriptor) VALUES ('platform', 4, 82, 'allow', '{"clauses":[{"all":true}]}');

INSERT INTO iam_role_permissions (context_key, role_id, permission_id, effect, scope_descriptor) VALUES ('platform', 4, 83, 'allow', '{"clauses":[{"all":true}]}');

INSERT INTO iam_role_permissions (context_key, role_id, permission_id, effect, scope_descriptor) VALUES ('platform', 4, 84, 'allow', '{"clauses":[{"all":true}]}');

INSERT INTO iam_role_permissions (context_key, role_id, permission_id, effect, scope_descriptor) VALUES ('platform', 4, 85, 'allow', '{"clauses":[{"all":true}]}');

INSERT INTO iam_role_permissions (context_key, role_id, permission_id, effect, scope_descriptor) VALUES ('platform', 4, 86, 'allow', '{"clauses":[{"all":true}]}');

INSERT INTO iam_role_permissions (context_key, role_id, permission_id, effect, scope_descriptor) VALUES ('platform', 4, 87, 'allow', '{"clauses":[{"all":true}]}');

INSERT INTO iam_role_permissions (context_key, role_id, permission_id, effect, scope_descriptor) VALUES ('platform', 4, 88, 'allow', '{"clauses":[{"all":true}]}');

INSERT INTO iam_role_permissions (context_key, role_id, permission_id, effect, scope_descriptor) VALUES ('platform', 4, 89, 'allow', '{"clauses":[{"all":true}]}');

INSERT INTO iam_role_permissions (context_key, role_id, permission_id, effect, scope_descriptor) VALUES ('platform', 4, 90, 'allow', '{"clauses":[{"all":true}]}');

INSERT INTO iam_role_permissions (context_key, role_id, permission_id, effect, scope_descriptor) VALUES ('platform', 4, 91, 'allow', '{"clauses":[{"all":true}]}');

INSERT INTO iam_role_permissions (context_key, role_id, permission_id, effect, scope_descriptor) VALUES ('platform', 4, 92, 'allow', '{"clauses":[{"all":true}]}');

INSERT INTO iam_role_permissions (context_key, role_id, permission_id, effect, scope_descriptor) VALUES ('platform', 4, 93, 'allow', '{"clauses":[{"all":true}]}');

INSERT INTO iam_role_permissions (context_key, role_id, permission_id, effect, scope_descriptor) VALUES ('platform', 4, 94, 'allow', '{"clauses":[{"all":true}]}');

INSERT INTO iam_role_permissions (context_key, role_id, permission_id, effect, scope_descriptor) VALUES ('platform', 4, 95, 'allow', '{"clauses":[{"all":true}]}');

INSERT INTO iam_role_permissions (context_key, role_id, permission_id, effect, scope_descriptor) VALUES ('platform', 4, 96, 'allow', '{"clauses":[{"all":true}]}');

INSERT INTO iam_role_permissions (context_key, role_id, permission_id, effect, scope_descriptor) VALUES ('platform', 4, 97, 'allow', '{"clauses":[{"all":true}]}');

INSERT INTO iam_role_permissions (context_key, role_id, permission_id, effect, scope_descriptor) VALUES ('platform', 4, 98, 'allow', '{"clauses":[{"all":true}]}');

INSERT INTO iam_role_permissions (context_key, role_id, permission_id, effect, scope_descriptor) VALUES ('platform', 4, 99, 'allow', '{"clauses":[{"all":true}]}');

INSERT INTO iam_role_permissions (context_key, role_id, permission_id, effect, scope_descriptor) VALUES ('platform', 4, 100, 'allow', '{"clauses":[{"all":true}]}');

INSERT INTO iam_role_permissions (context_key, role_id, permission_id, effect, scope_descriptor) VALUES ('platform', 4, 101, 'allow', '{"clauses":[{"all":true}]}');

INSERT INTO iam_role_permissions (context_key, role_id, permission_id, effect, scope_descriptor) VALUES ('platform', 4, 102, 'allow', '{"clauses":[{"all":true}]}');

INSERT INTO iam_role_permissions (context_key, role_id, permission_id, effect, scope_descriptor) VALUES ('platform', 4, 103, 'allow', '{"clauses":[{"all":true}]}');

INSERT INTO iam_role_permissions (context_key, role_id, permission_id, effect, scope_descriptor) VALUES ('platform', 4, 104, 'allow', '{"clauses":[{"all":true}]}');

INSERT INTO iam_role_permissions (context_key, role_id, permission_id, effect, scope_descriptor) VALUES ('platform', 4, 105, 'allow', '{"clauses":[{"all":true}]}');

INSERT INTO iam_role_permissions (context_key, role_id, permission_id, effect, scope_descriptor) VALUES ('platform', 4, 106, 'allow', '{"clauses":[{"all":true}]}');

INSERT INTO iam_role_permissions (context_key, role_id, permission_id, effect, scope_descriptor) VALUES ('platform', 4, 107, 'allow', '{"clauses":[{"all":true}]}');

INSERT INTO iam_role_permissions (context_key, role_id, permission_id, effect, scope_descriptor) VALUES ('platform', 4, 108, 'allow', '{"clauses":[{"all":true}]}');

INSERT INTO iam_role_permissions (context_key, role_id, permission_id, effect, scope_descriptor) VALUES ('platform', 4, 109, 'allow', '{"clauses":[{"all":true}]}');

INSERT INTO iam_role_permissions (context_key, role_id, permission_id, effect, scope_descriptor) VALUES ('platform', 4, 110, 'allow', '{"clauses":[{"all":true}]}');

INSERT INTO iam_role_permissions (context_key, role_id, permission_id, effect, scope_descriptor) VALUES ('platform', 4, 111, 'allow', '{"clauses":[{"all":true}]}');

INSERT INTO iam_role_permissions (context_key, role_id, permission_id, effect, scope_descriptor) VALUES ('platform', 4, 112, 'allow', '{"clauses":[{"all":true}]}');

INSERT INTO iam_role_permissions (context_key, role_id, permission_id, effect, scope_descriptor) VALUES ('platform', 4, 113, 'allow', '{"clauses":[{"all":true}]}');

INSERT INTO iam_role_permissions (context_key, role_id, permission_id, effect, scope_descriptor) VALUES ('platform', 4, 114, 'allow', '{"clauses":[{"all":true}]}');

INSERT INTO iam_role_permissions (context_key, role_id, permission_id, effect, scope_descriptor) VALUES ('platform', 4, 115, 'allow', '{"clauses":[{"all":true}]}');

INSERT INTO iam_role_permissions (context_key, role_id, permission_id, effect, scope_descriptor) VALUES ('platform', 4, 116, 'allow', '{"clauses":[{"all":true}]}');

INSERT INTO iam_role_permissions (context_key, role_id, permission_id, effect, scope_descriptor) VALUES ('platform', 4, 117, 'allow', '{"clauses":[{"all":true}]}');

INSERT INTO iam_role_permissions (context_key, role_id, permission_id, effect, scope_descriptor) VALUES ('platform', 4, 118, 'allow', '{"clauses":[{"all":true}]}');

INSERT INTO iam_role_permissions (context_key, role_id, permission_id, effect, scope_descriptor) VALUES ('platform', 4, 119, 'allow', '{"clauses":[{"all":true}]}');

INSERT INTO iam_role_permissions (context_key, role_id, permission_id, effect, scope_descriptor) VALUES ('platform', 4, 120, 'allow', '{"clauses":[{"all":true}]}');

INSERT INTO iam_role_permissions (context_key, role_id, permission_id, effect, scope_descriptor) VALUES ('platform', 4, 121, 'allow', '{"clauses":[{"all":true}]}');

INSERT INTO iam_role_permissions (context_key, role_id, permission_id, effect, scope_descriptor) VALUES ('platform', 4, 122, 'allow', '{"clauses":[{"all":true}]}');

INSERT INTO iam_role_permissions (context_key, role_id, permission_id, effect, scope_descriptor) VALUES ('platform', 4, 123, 'allow', '{"clauses":[{"all":true}]}');

INSERT INTO iam_role_permissions (context_key, role_id, permission_id, effect, scope_descriptor) VALUES ('platform', 4, 124, 'allow', '{"clauses":[{"all":true}]}');

INSERT INTO iam_role_permissions (context_key, role_id, permission_id, effect, scope_descriptor) VALUES ('platform', 4, 125, 'allow', '{"clauses":[{"all":true}]}');

INSERT INTO iam_role_permissions (context_key, role_id, permission_id, effect, scope_descriptor) VALUES ('platform', 4, 126, 'allow', '{"clauses":[{"all":true}]}');

INSERT INTO iam_role_permissions (context_key, role_id, permission_id, effect, scope_descriptor) VALUES ('platform', 4, 127, 'allow', '{"clauses":[{"all":true}]}');

INSERT INTO iam_role_permissions (context_key, role_id, permission_id, effect, scope_descriptor) VALUES ('platform', 4, 128, 'allow', '{"clauses":[{"all":true}]}');

INSERT INTO iam_role_permissions (context_key, role_id, permission_id, effect, scope_descriptor) VALUES ('platform', 4, 129, 'allow', '{"clauses":[{"all":true}]}');

INSERT INTO iam_role_permissions (context_key, role_id, permission_id, effect, scope_descriptor) VALUES ('platform', 4, 130, 'allow', '{"clauses":[{"all":true}]}');

INSERT INTO iam_role_permissions (context_key, role_id, permission_id, effect, scope_descriptor) VALUES ('platform', 4, 131, 'allow', '{"clauses":[{"all":true}]}');

INSERT INTO iam_role_permissions (context_key, role_id, permission_id, effect, scope_descriptor) VALUES ('platform', 4, 132, 'allow', '{"clauses":[{"all":true}]}');

INSERT INTO iam_role_permissions (context_key, role_id, permission_id, effect, scope_descriptor) VALUES ('platform', 4, 133, 'allow', '{"clauses":[{"all":true}]}');

INSERT INTO iam_role_permissions (context_key, role_id, permission_id, effect, scope_descriptor) VALUES ('platform', 4, 134, 'allow', '{"clauses":[{"all":true}]}');

INSERT INTO iam_role_permissions (context_key, role_id, permission_id, effect, scope_descriptor) VALUES ('platform', 4, 135, 'allow', '{"clauses":[{"all":true}]}');

INSERT INTO iam_role_permissions (context_key, role_id, permission_id, effect, scope_descriptor) VALUES ('platform', 4, 136, 'allow', '{"clauses":[{"all":true}]}');

INSERT INTO iam_role_permissions (context_key, role_id, permission_id, effect, scope_descriptor) VALUES ('platform', 4, 137, 'allow', '{"clauses":[{"all":true}]}');

INSERT INTO iam_role_permissions (context_key, role_id, permission_id, effect, scope_descriptor) VALUES ('platform', 4, 138, 'allow', '{"clauses":[{"all":true}]}');

INSERT INTO iam_role_permissions (context_key, role_id, permission_id, effect, scope_descriptor) VALUES ('platform', 4, 139, 'allow', '{"clauses":[{"all":true}]}');

INSERT INTO iam_role_permissions (context_key, role_id, permission_id, effect, scope_descriptor) VALUES ('platform', 4, 140, 'allow', '{"clauses":[{"all":true}]}');

INSERT INTO iam_role_permissions (context_key, role_id, permission_id, effect, scope_descriptor) VALUES ('platform', 4, 141, 'allow', '{"clauses":[{"all":true}]}');

INSERT INTO iam_role_permissions (context_key, role_id, permission_id, effect, scope_descriptor) VALUES ('platform', 4, 142, 'allow', '{"clauses":[{"all":true}]}');

INSERT INTO iam_role_permissions (context_key, role_id, permission_id, effect, scope_descriptor) VALUES ('platform', 4, 143, 'allow', '{"clauses":[{"all":true}]}');

INSERT INTO iam_role_permissions (context_key, role_id, permission_id, effect, scope_descriptor) VALUES ('platform', 4, 144, 'allow', '{"clauses":[{"all":true}]}');

INSERT INTO iam_role_permissions (context_key, role_id, permission_id, effect, scope_descriptor) VALUES ('platform', 4, 145, 'allow', '{"clauses":[{"all":true}]}');

INSERT INTO iam_role_permissions (context_key, role_id, permission_id, effect, scope_descriptor) VALUES ('platform', 4, 146, 'allow', '{"clauses":[{"all":true}]}');

INSERT INTO iam_role_permissions (context_key, role_id, permission_id, effect, scope_descriptor) VALUES ('platform', 4, 147, 'allow', '{"clauses":[{"all":true}]}');

INSERT INTO iam_role_permissions (context_key, role_id, permission_id, effect, scope_descriptor) VALUES ('platform', 4, 148, 'allow', '{"clauses":[{"all":true}]}');

INSERT INTO iam_role_permissions (context_key, role_id, permission_id, effect, scope_descriptor) VALUES ('platform', 4, 149, 'allow', '{"clauses":[{"all":true}]}');

INSERT INTO iam_role_permissions (context_key, role_id, permission_id, effect, scope_descriptor) VALUES ('platform', 4, 150, 'allow', '{"clauses":[{"all":true}]}');

INSERT INTO iam_role_permissions (context_key, role_id, permission_id, effect, scope_descriptor) VALUES ('platform', 4, 151, 'allow', '{"clauses":[{"all":true}]}');

INSERT INTO iam_role_permissions (context_key, role_id, permission_id, effect, scope_descriptor) VALUES ('platform', 4, 152, 'allow', '{"clauses":[{"all":true}]}');

INSERT INTO iam_role_permissions (context_key, role_id, permission_id, effect, scope_descriptor) VALUES ('platform', 4, 153, 'allow', '{"clauses":[{"all":true}]}');

INSERT INTO iam_role_permissions (context_key, role_id, permission_id, effect, scope_descriptor) VALUES ('platform', 4, 154, 'allow', '{"clauses":[{"all":true}]}');

INSERT INTO iam_role_permissions (context_key, role_id, permission_id, effect, scope_descriptor) VALUES ('platform', 4, 155, 'allow', '{"clauses":[{"all":true}]}');

INSERT INTO iam_role_permissions (context_key, role_id, permission_id, effect, scope_descriptor) VALUES ('platform', 4, 156, 'allow', '{"clauses":[{"all":true}]}');

INSERT INTO iam_role_permissions (context_key, role_id, permission_id, effect, scope_descriptor) VALUES ('platform', 4, 157, 'allow', '{"clauses":[{"all":true}]}');

INSERT INTO iam_role_permissions (context_key, role_id, permission_id, effect, scope_descriptor) VALUES ('platform', 4, 158, 'allow', '{"clauses":[{"all":true}]}');

INSERT INTO iam_role_permissions (context_key, role_id, permission_id, effect, scope_descriptor) VALUES ('platform', 4, 159, 'allow', '{"clauses":[{"all":true}]}');

INSERT INTO iam_role_permissions (context_key, role_id, permission_id, effect, scope_descriptor) VALUES ('platform', 4, 160, 'allow', '{"clauses":[{"all":true}]}');

INSERT INTO iam_role_permissions (context_key, role_id, permission_id, effect, scope_descriptor) VALUES ('platform', 4, 161, 'allow', '{"clauses":[{"all":true}]}');

INSERT INTO iam_role_permissions (context_key, role_id, permission_id, effect, scope_descriptor) VALUES ('platform', 4, 162, 'allow', '{"clauses":[{"all":true}]}');

INSERT INTO iam_role_permissions (context_key, role_id, permission_id, effect, scope_descriptor) VALUES ('platform', 4, 163, 'allow', '{"clauses":[{"all":true}]}');

INSERT INTO iam_role_permissions (context_key, role_id, permission_id, effect, scope_descriptor) VALUES ('platform', 4, 164, 'allow', '{"clauses":[{"all":true}]}');

INSERT INTO iam_role_permissions (context_key, role_id, permission_id, effect, scope_descriptor) VALUES ('platform', 4, 165, 'allow', '{"clauses":[{"all":true}]}');

INSERT INTO iam_role_permissions (context_key, role_id, permission_id, effect, scope_descriptor) VALUES ('platform', 4, 166, 'allow', '{"clauses":[{"all":true}]}');

INSERT INTO iam_role_permissions (context_key, role_id, permission_id, effect, scope_descriptor) VALUES ('platform', 4, 167, 'allow', '{"clauses":[{"all":true}]}');

INSERT INTO iam_role_permissions (context_key, role_id, permission_id, effect, scope_descriptor) VALUES ('platform', 4, 168, 'allow', '{"clauses":[{"all":true}]}');

INSERT INTO iam_role_permissions (context_key, role_id, permission_id, effect, scope_descriptor) VALUES ('platform', 4, 169, 'allow', '{"clauses":[{"all":true}]}');

INSERT INTO iam_role_permissions (context_key, role_id, permission_id, effect, scope_descriptor) VALUES ('platform', 4, 170, 'allow', '{"clauses":[{"all":true}]}');

INSERT INTO iam_role_permissions (context_key, role_id, permission_id, effect, scope_descriptor) VALUES ('platform', 4, 171, 'allow', '{"clauses":[{"all":true}]}');

INSERT INTO iam_role_permissions (context_key, role_id, permission_id, effect, scope_descriptor) VALUES ('platform', 4, 172, 'allow', '{"clauses":[{"all":true}]}');

INSERT INTO iam_role_permissions (context_key, role_id, permission_id, effect, scope_descriptor) VALUES ('platform', 4, 173, 'allow', '{"clauses":[{"all":true}]}');

INSERT INTO iam_role_permissions (context_key, role_id, permission_id, effect, scope_descriptor) VALUES ('platform', 4, 174, 'allow', '{"clauses":[{"all":true}]}');

INSERT INTO iam_role_permissions (context_key, role_id, permission_id, effect, scope_descriptor) VALUES ('platform', 4, 175, 'allow', '{"clauses":[{"all":true}]}');

INSERT INTO iam_role_permissions (context_key, role_id, permission_id, effect, scope_descriptor) VALUES ('platform', 4, 176, 'allow', '{"clauses":[{"all":true}]}');

INSERT INTO iam_role_permissions (context_key, role_id, permission_id, effect, scope_descriptor) VALUES ('platform', 4, 177, 'allow', '{"clauses":[{"all":true}]}');

INSERT INTO iam_role_permissions (context_key, role_id, permission_id, effect, scope_descriptor) VALUES ('platform', 4, 178, 'allow', '{"clauses":[{"all":true}]}');

INSERT INTO iam_role_permissions (context_key, role_id, permission_id, effect, scope_descriptor) VALUES ('platform', 4, 179, 'allow', '{"clauses":[{"all":true}]}');

INSERT INTO iam_role_permissions (context_key, role_id, permission_id, effect, scope_descriptor) VALUES ('platform', 4, 180, 'allow', '{"clauses":[{"all":true}]}');

INSERT INTO iam_role_permissions (context_key, role_id, permission_id, effect, scope_descriptor) VALUES ('platform', 4, 194, 'allow', '{"clauses":[{"all":true}]}');

INSERT INTO iam_role_permissions (context_key, role_id, permission_id, effect, scope_descriptor) VALUES ('platform', 4, 195, 'allow', '{"clauses":[{"all":true}]}');

INSERT INTO iam_role_permissions (context_key, role_id, permission_id, effect, scope_descriptor) VALUES ('platform', 4, 196, 'allow', '{"clauses":[{"all":true}]}');

INSERT INTO iam_role_permissions (context_key, role_id, permission_id, effect, scope_descriptor) VALUES ('platform', 4, 197, 'allow', '{"clauses":[{"all":true}]}');

INSERT INTO iam_role_permissions (context_key, role_id, permission_id, effect, scope_descriptor) VALUES ('platform', 4, 198, 'allow', '{"clauses":[{"all":true}]}');

INSERT INTO iam_role_permissions (context_key, role_id, permission_id, effect, scope_descriptor) VALUES ('platform', 4, 199, 'allow', '{"clauses":[{"all":true}]}');

INSERT INTO iam_role_permissions (context_key, role_id, permission_id, effect, scope_descriptor) VALUES ('platform', 4, 200, 'allow', '{"clauses":[{"all":true}]}');

INSERT INTO iam_role_permissions (context_key, role_id, permission_id, effect, scope_descriptor) VALUES ('platform', 4, 201, 'allow', '{"clauses":[{"all":true}]}');

INSERT INTO iam_role_permissions (context_key, role_id, permission_id, effect, scope_descriptor) VALUES ('platform', 4, 210, 'allow', '{"clauses":[{"all":true}]}');

INSERT INTO iam_role_permissions (context_key, role_id, permission_id, effect, scope_descriptor) VALUES ('platform', 4, 211, 'allow', '{"clauses":[{"all":true}]}');

INSERT INTO iam_role_permissions (context_key, role_id, permission_id, effect, scope_descriptor) VALUES ('platform', 4, 212, 'allow', '{"clauses":[{"all":true}]}');

INSERT INTO iam_role_permissions (context_key, role_id, permission_id, effect, scope_descriptor) VALUES ('platform', 4, 213, 'allow', '{"clauses":[{"all":true}]}');

INSERT INTO iam_role_permissions (context_key, role_id, permission_id, effect, scope_descriptor) VALUES ('platform', 4, 214, 'allow', '{"clauses":[{"all":true}]}');

INSERT INTO iam_role_permissions (context_key, role_id, permission_id, effect, scope_descriptor) VALUES ('platform', 4, 215, 'allow', '{"clauses":[{"all":true}]}');

INSERT INTO iam_role_permissions (context_key, role_id, permission_id, effect, scope_descriptor) VALUES ('platform', 4, 216, 'allow', '{"clauses":[{"all":true}]}');

INSERT INTO iam_role_permissions (context_key, role_id, permission_id, effect, scope_descriptor) VALUES ('platform', 4, 217, 'allow', '{"clauses":[{"all":true}]}');

INSERT INTO iam_role_permissions (context_key, role_id, permission_id, effect, scope_descriptor) VALUES ('platform', 4, 218, 'allow', '{"clauses":[{"all":true}]}');

INSERT INTO iam_role_permissions (context_key, role_id, permission_id, effect, scope_descriptor) VALUES ('platform', 4, 219, 'allow', '{"clauses":[{"all":true}]}');

INSERT INTO iam_role_permissions (context_key, role_id, permission_id, effect, scope_descriptor) VALUES ('platform', 4, 220, 'allow', '{"clauses":[{"all":true}]}');

INSERT INTO iam_role_permissions (context_key, role_id, permission_id, effect, scope_descriptor) VALUES ('platform', 4, 221, 'allow', '{"clauses":[{"all":true}]}');

INSERT INTO iam_role_permissions (context_key, role_id, permission_id, effect, scope_descriptor) VALUES ('platform', 4, 222, 'allow', '{"clauses":[{"all":true}]}');

INSERT INTO iam_role_permissions (context_key, role_id, permission_id, effect, scope_descriptor) VALUES ('platform', 4, 223, 'allow', '{"clauses":[{"all":true}]}');

INSERT INTO iam_role_permissions (context_key, role_id, permission_id, effect, scope_descriptor) VALUES ('platform', 4, 224, 'allow', '{"clauses":[{"all":true}]}');

INSERT INTO iam_role_permissions (context_key, role_id, permission_id, effect, scope_descriptor) VALUES ('platform', 4, 225, 'allow', '{"clauses":[{"all":true}]}');

INSERT INTO iam_role_permissions (context_key, role_id, permission_id, effect, scope_descriptor) VALUES ('platform', 4, 226, 'allow', '{"clauses":[{"all":true}]}');

INSERT INTO iam_role_permissions (context_key, role_id, permission_id, effect, scope_descriptor) VALUES ('platform', 4, 227, 'allow', '{"clauses":[{"all":true}]}');

INSERT INTO iam_role_permissions (context_key, role_id, permission_id, effect, scope_descriptor) VALUES ('platform', 4, 228, 'allow', '{"clauses":[{"all":true}]}');

INSERT INTO iam_role_permissions (context_key, role_id, permission_id, effect, scope_descriptor) VALUES ('platform', 4, 229, 'allow', '{"clauses":[{"all":true}]}');

INSERT INTO iam_role_permissions (context_key, role_id, permission_id, effect, scope_descriptor) VALUES ('platform', 4, 230, 'allow', '{"clauses":[{"all":true}]}');

INSERT INTO iam_role_permissions (context_key, role_id, permission_id, effect, scope_descriptor) VALUES ('platform', 4, 231, 'allow', '{"clauses":[{"all":true}]}');

INSERT INTO iam_role_permissions (context_key, role_id, permission_id, effect, scope_descriptor) VALUES ('platform', 4, 232, 'allow', '{"clauses":[{"all":true}]}');

INSERT INTO iam_role_permissions (context_key, role_id, permission_id, effect, scope_descriptor) VALUES ('platform', 4, 233, 'allow', '{"clauses":[{"all":true}]}');

INSERT INTO iam_role_permissions (context_key, role_id, permission_id, effect, scope_descriptor) VALUES ('platform', 4, 234, 'allow', '{"clauses":[{"all":true}]}');

INSERT INTO iam_role_permissions (context_key, role_id, permission_id, effect, scope_descriptor) VALUES ('platform', 4, 235, 'allow', '{"clauses":[{"all":true}]}');

INSERT INTO iam_role_permissions (context_key, role_id, permission_id, effect, scope_descriptor) VALUES ('platform', 4, 236, 'allow', '{"clauses":[{"all":true}]}');

INSERT INTO iam_role_permissions (context_key, role_id, permission_id, effect, scope_descriptor) VALUES ('platform', 4, 237, 'allow', '{"clauses":[{"all":true}]}');

SELECT setval(pg_get_serial_sequence('iam_resources', 'id'), (SELECT MAX(id) FROM iam_resources));

SELECT setval(pg_get_serial_sequence('iam_permissions', 'id'), (SELECT MAX(id) FROM iam_permissions));

SELECT setval(pg_get_serial_sequence('iam_roles', 'id'), (SELECT MAX(id) FROM iam_roles));

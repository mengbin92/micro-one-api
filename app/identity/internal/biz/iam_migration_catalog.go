package biz

import (
	"micro-one-api/domain/authorization"
	"slices"
	"time"
)

// Frozen D0 release manifest. New catalog operations do not enter historical
// roles automatically; changes to this list require an explicit release review.
var iamMigrationRootCodes = []string{
	"admin.console.enter",
	"admin.overview.read",
	"billing.account.balance.adjust",
	"billing.account.balance.reset",
	"billing.account.cost.read",
	"billing.account.ledger.read",
	"billing.account.read",
	"billing.payment.list",
	"billing.payment.read",
	"billing.payment.refund",
	"billing.pricing.export",
	"billing.pricing.import",
	"billing.pricing.read",
	"billing.pricing.update",
	"billing.reconciliation.issues.read",
	"billing.reconciliation.read",
	"billing.reconciliation.run",
	"billing.redemption.batch_create",
	"billing.redemption.create",
	"billing.redemption.delete",
	"billing.redemption.export",
	"billing.redemption.list",
	"billing.redemption.read",
	"billing.redemption.update",
	"billing.report.export",
	"billing.routing_policy.publish",
	"billing.routing_policy.read",
	"billing.routing_policy.user_override.delete",
	"billing.routing_policy.user_override.read",
	"billing.routing_policy.user_override.update",
	"billing.upstream_cost.create",
	"billing.upstream_cost.delete",
	"billing.upstream_cost.migrate",
	"billing.upstream_cost.read",
	"billing.upstream_cost.update",
	"channel.account.create",
	"channel.account.credential.update",
	"channel.account.delete",
	"channel.account.disable",
	"channel.account.enable",
	"channel.account.list",
	"channel.account.oauth.bind",
	"channel.account.quota.reset",
	"channel.account.read",
	"channel.account.recovery.clear",
	"channel.account.update",
	"channel.channel.balance.refresh",
	"channel.channel.batch_delete",
	"channel.channel.create",
	"channel.channel.delete",
	"channel.channel.disable",
	"channel.channel.enable",
	"channel.channel.export",
	"channel.channel.list",
	"channel.channel.read",
	"channel.channel.secret.read",
	"channel.channel.secret.rotate",
	"channel.channel.test",
	"channel.channel.update",
	"channel.model.batch_update",
	"channel.model.canonical.merge",
	"channel.model.canonical.preflight",
	"channel.model.create",
	"channel.model.delete",
	"channel.model.disable",
	"channel.model.enable",
	"channel.model.export",
	"channel.model.import",
	"channel.model.list",
	"channel.model.read",
	"channel.model.update",
	"channel.model_alias.create",
	"channel.model_alias.delete",
	"channel.model_alias.read",
	"channel.model_mapping.create",
	"channel.model_mapping.delete",
	"channel.model_mapping.read",
	"channel.model_mapping.update",
	"channel.model_routing.create",
	"channel.model_routing.delete",
	"channel.model_routing.read",
	"channel.model_routing.update",
	"channel.model_usage.read",
	"channel.routing_group.archive",
	"channel.routing_group.create",
	"channel.routing_group.disable",
	"channel.routing_group.enable",
	"channel.routing_group.list",
	"channel.routing_group.members.read",
	"channel.routing_group.members.update",
	"channel.routing_group.read",
	"channel.routing_group.resource_override.update",
	"channel.routing_group.update",
	"channel.usage_semantic_block.list",
	"channel.usage_semantic_block.resolve",
	"iam.audit.export",
	"iam.audit.read",
	"iam.authorization.explain",
	"iam.authorization.self.read",
	"iam.authorization.simulate",
	"iam.authorization.user.read",
	"iam.constraint.create",
	"iam.constraint.delete",
	"iam.constraint.read",
	"iam.constraint.update",
	"iam.delegation.create",
	"iam.delegation.read",
	"iam.delegation.revoke",
	"iam.delegation.update",
	"iam.menu.archive",
	"iam.menu.create",
	"iam.menu.list",
	"iam.menu.read",
	"iam.menu.update",
	"iam.permission.archive",
	"iam.permission.create",
	"iam.permission.disable",
	"iam.permission.enable",
	"iam.permission.list",
	"iam.permission.metadata.update",
	"iam.permission.read",
	"iam.permission.references.read",
	"iam.role.archive",
	"iam.role.copy",
	"iam.role.create",
	"iam.role.disable",
	"iam.role.enable",
	"iam.role.hierarchy.update",
	"iam.role.list",
	"iam.role.members.read",
	"iam.role.permissions.read",
	"iam.role.permissions.update",
	"iam.role.read",
	"iam.role.update",
	"identity.routing_access.default.update",
	"identity.routing_access.grant",
	"identity.routing_access.public_access.update",
	"identity.routing_access.read",
	"identity.routing_access.revoke",
	"identity.session.roles.activate",
	"identity.session.roles.read",
	"identity.session.self.revoke",
	"identity.user.contact.read",
	"identity.user.create",
	"identity.user.credential.update",
	"identity.user.delete",
	"identity.user.disable",
	"identity.user.email_binding.update",
	"identity.user.enable",
	"identity.user.export",
	"identity.user.list",
	"identity.user.read",
	"identity.user.sessions.revoke",
	"identity.user.update",
	"identity.user_role.assign",
	"identity.user_role.batch_assign",
	"identity.user_role.read",
	"identity.user_role.revoke",
	"log.request.content.read",
	"log.request.delete",
	"log.request.export",
	"log.request.list",
	"log.request.purge",
	"log.request.read",
	"log.request.stats.read",
	"log.selection_event.list",
	"monitor.alert_rule.create",
	"monitor.alert_rule.delete",
	"monitor.alert_rule.list",
	"monitor.alert_rule.read",
	"monitor.alert_rule.update",
	"monitor.health.channel.read",
	"monitor.health.model.read",
	"monitor.health.selector.read",
	"monitor.health.service.read",
	"notify.notification.acknowledge",
	"notify.notification.list",
	"notify.notification.read",
	"notify.notification.rules.update",
	"notify.notification.test",
	"subscription.plan.create",
	"subscription.plan.delete",
	"subscription.plan.list",
	"subscription.plan.publish",
	"subscription.plan.read",
	"subscription.plan.unpublish",
	"subscription.plan.update",
	"subscription.quota_policy.create",
	"subscription.quota_policy.delete",
	"subscription.quota_policy.list",
	"subscription.quota_policy.read",
	"subscription.quota_policy.update",
	"subscription.user_subscription.assign",
	"subscription.user_subscription.change",
	"subscription.user_subscription.extend",
	"subscription.user_subscription.list",
	"subscription.user_subscription.quota.reset",
	"subscription.user_subscription.read",
	"subscription.user_subscription.report.read",
	"subscription.user_subscription.revoke",
	"system.content.about.update",
	"system.content.home.update",
	"system.content.notice.update",
	"system.option.payment.update",
	"system.option.pricing.update",
	"system.option.read",
	"system.option.security.update",
	"system.option.update",
}

var iamMigrationAdminCodes = []string{
	"admin.console.enter",
	"admin.overview.read",
	"billing.account.balance.adjust",
	"billing.account.balance.reset",
	"billing.account.cost.read",
	"billing.account.ledger.read",
	"billing.account.read",
	"billing.payment.list",
	"billing.payment.read",
	"billing.payment.refund",
	"billing.pricing.read",
	"billing.pricing.update",
	"billing.reconciliation.issues.read",
	"billing.reconciliation.read",
	"billing.reconciliation.run",
	"billing.redemption.batch_create",
	"billing.redemption.create",
	"billing.redemption.delete",
	"billing.redemption.export",
	"billing.redemption.list",
	"billing.redemption.read",
	"billing.redemption.update",
	"billing.report.export",
	"billing.routing_policy.publish",
	"billing.routing_policy.read",
	"billing.routing_policy.user_override.delete",
	"billing.routing_policy.user_override.read",
	"billing.routing_policy.user_override.update",
	"billing.upstream_cost.create",
	"billing.upstream_cost.delete",
	"billing.upstream_cost.migrate",
	"billing.upstream_cost.read",
	"billing.upstream_cost.update",
	"channel.account.create",
	"channel.account.credential.update",
	"channel.account.delete",
	"channel.account.disable",
	"channel.account.enable",
	"channel.account.list",
	"channel.account.oauth.bind",
	"channel.account.quota.reset",
	"channel.account.read",
	"channel.account.recovery.clear",
	"channel.account.update",
	"channel.channel.balance.refresh",
	"channel.channel.batch_delete",
	"channel.channel.create",
	"channel.channel.delete",
	"channel.channel.disable",
	"channel.channel.enable",
	"channel.channel.export",
	"channel.channel.list",
	"channel.channel.read",
	"channel.channel.secret.read",
	"channel.channel.secret.rotate",
	"channel.channel.test",
	"channel.channel.update",
	"channel.model.batch_update",
	"channel.model.canonical.merge",
	"channel.model.canonical.preflight",
	"channel.model.create",
	"channel.model.delete",
	"channel.model.disable",
	"channel.model.enable",
	"channel.model.list",
	"channel.model.read",
	"channel.model.update",
	"channel.model_alias.create",
	"channel.model_alias.delete",
	"channel.model_alias.read",
	"channel.model_mapping.create",
	"channel.model_mapping.delete",
	"channel.model_mapping.read",
	"channel.model_mapping.update",
	"channel.model_routing.create",
	"channel.model_routing.delete",
	"channel.model_routing.read",
	"channel.model_routing.update",
	"channel.model_usage.read",
	"channel.routing_group.archive",
	"channel.routing_group.create",
	"channel.routing_group.disable",
	"channel.routing_group.enable",
	"channel.routing_group.list",
	"channel.routing_group.members.read",
	"channel.routing_group.members.update",
	"channel.routing_group.read",
	"channel.routing_group.resource_override.update",
	"channel.routing_group.update",
	"channel.usage_semantic_block.list",
	"channel.usage_semantic_block.resolve",
	"identity.routing_access.read",
	"identity.user.contact.read",
	"identity.user.create",
	"identity.user.delete",
	"identity.user.disable",
	"identity.user.enable",
	"identity.user.export",
	"identity.user.list",
	"identity.user.read",
	"identity.user.update",
	"identity.user_role.read",
	"log.request.content.read",
	"log.request.delete",
	"log.request.export",
	"log.request.list",
	"log.request.purge",
	"log.request.read",
	"log.request.stats.read",
	"log.selection_event.list",
	"monitor.alert_rule.create",
	"monitor.alert_rule.delete",
	"monitor.alert_rule.list",
	"monitor.alert_rule.read",
	"monitor.alert_rule.update",
	"monitor.health.channel.read",
	"monitor.health.model.read",
	"monitor.health.selector.read",
	"monitor.health.service.read",
	"notify.notification.list",
	"notify.notification.read",
	"subscription.plan.create",
	"subscription.plan.delete",
	"subscription.plan.list",
	"subscription.plan.publish",
	"subscription.plan.read",
	"subscription.plan.unpublish",
	"subscription.plan.update",
	"subscription.quota_policy.create",
	"subscription.quota_policy.delete",
	"subscription.quota_policy.list",
	"subscription.quota_policy.read",
	"subscription.quota_policy.update",
	"subscription.user_subscription.assign",
	"subscription.user_subscription.change",
	"subscription.user_subscription.extend",
	"subscription.user_subscription.list",
	"subscription.user_subscription.quota.reset",
	"subscription.user_subscription.read",
	"subscription.user_subscription.report.read",
	"subscription.user_subscription.revoke",
	"system.content.about.update",
	"system.content.home.update",
	"system.content.notice.update",
	"system.option.payment.update",
	"system.option.pricing.update",
	"system.option.read",
	"system.option.security.update",
	"system.option.update",
}

var iamMigrationSelfCodes = []string{"iam.authorization.self.read", "identity.session.roles.read", "identity.session.roles.activate", "identity.session.self.revoke"}

func IAMMigrationBuiltinGrants() map[string][]IAMGrant {
	out := map[string][]IAMGrant{}
	for code, operations := range map[string][]string{"root": iamMigrationRootCodes, "platform_admin": iamMigrationAdminCodes, "guest": iamMigrationSelfCodes, "member": iamMigrationSelfCodes} {
		for _, op := range operations {
			scope := authorization.Scope{Clauses: []authorization.Clause{{All: true}}}
			if code == "guest" || code == "member" {
				scope = authorization.Scope{Clauses: []authorization.Clause{{Self: true}}}
				if op == "iam.authorization.self.read" {
					scope = authorization.Scope{Clauses: []authorization.Clause{{All: true}}}
				}
			}
			out[code] = append(out[code], IAMGrant{Operation: op, Effect: authorization.Allow, Scope: scope})
		}
	}
	// Session introspection is an explicit new self facility for every account.
	for _, op := range iamMigrationSelfCodes {
		if !slices.Contains(iamMigrationAdminCodes, op) {
			out["platform_admin"] = append(out["platform_admin"], IAMGrant{Operation: op, Effect: authorization.Allow, Scope: authorization.Scope{Clauses: []authorization.Clause{{All: true}}}})
		}
	}
	return out
}

// Shadow evaluates the actual source algorithm over frozen legacy actions and
// owner fact fixtures (self/another user and shared groups). It never writes a
// session, changes a response decision, or repairs a difference with a grant.
func iamMigrationShadow(users []IAMMigrationUser, state IAMConstraintState, now time.Time) []IAMMigrationDifference {
	out := []IAMMigrationDifference{}
	roles := map[int64]IAMRole{}
	for _, r := range state.Roles {
		roles[r.ID] = r
	}
	for _, u := range users {
		assignments := []IAMAssignment{}
		active := []int64{}
		for _, a := range state.Assignments {
			if a.UserID == u.ID {
				assignments = append(assignments, a)
				if !a.Revoked && a.Validity.Contains(now) {
					active = append(active, a.RoleID)
				}
			}
		}
		sources, err := IAMSources(authorization.Platform(), roles, assignments, active)
		if err != nil {
			out = append(out, IAMMigrationDifference{UserID: u.ID, Kind: "source_error"})
			continue
		}
		for _, code := range iamMigrationRootCodes {
			if !IAMExecutionBound(code) {
				continue
			}
			op, _ := authorization.Lookup(code)
			op.Binding = "bound"
			actor := authorization.Actor{UserID: u.ID, SessionID: "migration-shadow", ExpiresAt: now.Add(time.Hour)}
			fixtures := []authorization.ObjectFacts{{Context: authorization.Platform(), ResourceID: 1, OwnerUserID: u.ID, RoutingGroupIDs: []int64{1}}, {Context: authorization.Platform(), ResourceID: 2, OwnerUserID: u.ID + 1, RoutingGroupIDs: []int64{1, 2}}}
			for _, facts := range fixtures {
				legacy := u.Status == UserStatusEnabled && (u.Role == 100 || (u.Role == 10 && slices.Contains(iamMigrationAdminCodes, code)))
				self := slices.Contains(iamMigrationSelfCodes, code)
				if self {
					legacy = u.Status == UserStatusEnabled && facts.OwnerUserID == u.ID
				}
				decision := authorization.Decide(authorization.Input{Actor: actor, Context: authorization.Platform(), Operation: op, Object: facts, Sources: sources, Now: now, IdentityValid: u.Status == UserStatusEnabled, SessionValid: true, ConstraintsPass: true, BusinessRulesPass: true, OperationEnabled: true})
				allowed := decision.Allowed
				if self && facts.OwnerUserID != u.ID {
					allowed = false
				}
				if legacy != allowed {
					kind := "unexpected"
					if self {
						kind = "new_self_facility"
					}
					if u.Role == 100 && code[:4] == "iam." {
						kind = "root_governance"
					}
					out = append(out, IAMMigrationDifference{UserID: u.ID, Operation: code, Kind: kind, LegacyAllowed: legacy, IAMAllowed: allowed})
				}
			}
		}
		if u.Role == 10 && u.Status == UserStatusEnabled {
			for _, code := range []string{"identity.user.email_binding.update", "identity.user.credential.update", "identity.routing_access.grant", "identity.routing_access.revoke", "identity.routing_access.default.update", "identity.routing_access.public_access.update"} {
				out = append(out, IAMMigrationDifference{UserID: u.ID, Operation: code, Kind: "expected_security_tightening", LegacyAllowed: true})
			}
		}
	}
	return out
}

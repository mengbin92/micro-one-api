package authorization

import "slices"

// ExecutionPoint declarations are code-owned resource entry points. A browser
// cannot publish one by sending an operation or fabricated object facts.
type ExecutionPoint struct {
	Owner      string
	Operations []string
}

var executionPoints = map[string]ExecutionPoint{
	"admin.subscription.self": {"admin", nil},
	"admin.upstream_costs":    {"admin", []string{"billing.upstream_cost.read", "billing.upstream_cost.create", "billing.upstream_cost.update", "billing.upstream_cost.delete", "billing.upstream_cost.migrate"}},
	"admin.system_options":    {"admin", []string{"system.option.read", "system.option.update", "system.option.security.update", "system.option.payment.update", "system.option.pricing.update"}},
	"admin.content":           {"admin", []string{"system.content.notice.update", "system.content.about.update", "system.content.home.update"}},
	"admin.routing_policy":    {"admin", []string{"billing.routing_policy.read", "billing.routing_policy.publish"}},
	"admin.pricing":           {"admin", []string{"billing.pricing.read", "billing.pricing.update", "billing.pricing.import", "billing.pricing.export"}},
	"billing.self":            {"billing", nil},
	"log.self":                {"log", nil},
	"admin.console":           {"admin", []string{"admin.console.enter"}},
	"identity.routing_access": {"identity", []string{"identity.routing_access.read", "identity.routing_access.grant", "identity.routing_access.revoke", "identity.routing_access.default.update", "identity.routing_access.public_access.update"}},
	"identity.users.create":   {"identity", []string{"identity.user.create", "identity.user.credential.update", "identity.user.email_binding.update", "identity.routing_access.default.update", "identity.routing_access.grant"}},
	"identity.users.delete":   {"identity", []string{"identity.user.delete"}},
	"identity.users.list":     {"identity", []string{"identity.user.list", "identity.user.contact.read"}},
	"identity.users.export":   {"identity", []string{"identity.user.export", "identity.user.contact.read"}},
	"identity.users.read":     {"identity", []string{"identity.user.read", "identity.user.contact.read"}},
	"identity.users.update":   {"identity", []string{"identity.user.update", "identity.user.enable", "identity.user.disable", "identity.user.email_binding.update", "identity.user.credential.update", "identity.routing_access.default.update", "identity.routing_access.grant", "identity.routing_access.revoke"}},

	// B2 channel owner. Every entry point maps to a reviewed handler slice;
	// secret and test/balance actions stay separate from plain reads.
	"channel.channels.list":        {"channel", []string{"channel.channel.list"}},
	"channel.channels.read":        {"channel", []string{"channel.channel.read"}},
	"channel.channels.create":      {"channel", []string{"channel.channel.create"}},
	"channel.channels.update":      {"channel", []string{"channel.channel.update", "channel.channel.enable", "channel.channel.disable"}},
	"channel.channels.delete":      {"channel", []string{"channel.channel.delete", "channel.channel.batch_delete"}},
	"channel.channels.export":      {"channel", []string{"channel.channel.export"}},
	"channel.channels.test":        {"channel", []string{"channel.channel.test"}},
	"channel.channels.balance":     {"channel", []string{"channel.channel.balance.refresh"}},
	"channel.channels.secret":      {"channel", []string{"channel.channel.secret.read", "channel.channel.secret.rotate"}},
	"channel.accounts.list":        {"channel", []string{"channel.account.list"}},
	"channel.accounts.read":        {"channel", []string{"channel.account.read"}},
	"channel.accounts.create":      {"channel", []string{"channel.account.create"}},
	"channel.accounts.update":      {"channel", []string{"channel.account.update", "channel.account.enable", "channel.account.disable"}},
	"channel.accounts.delete":      {"channel", []string{"channel.account.delete"}},
	"channel.accounts.quota":       {"channel", []string{"channel.account.quota.reset"}},
	"channel.accounts.recovery":    {"channel", []string{"channel.account.recovery.clear"}},
	"channel.accounts.credential":  {"channel", []string{"channel.account.credential.update"}},
	"channel.accounts.oauth":       {"channel", []string{"channel.account.oauth.bind"}},
	"channel.accounts.cost":        {"channel", []string{"billing.upstream_cost.read"}},
	"channel.models.pricing":       {"channel", []string{"billing.pricing.read", "billing.pricing.update", "billing.pricing.import", "billing.pricing.export"}},
	"channel.models.list":          {"channel", []string{"channel.model.list"}},
	"channel.models.read":          {"channel", []string{"channel.model.read"}},
	"channel.models.create":        {"channel", []string{"channel.model.create"}},
	"channel.models.update":        {"channel", []string{"channel.model.update", "channel.model.enable", "channel.model.disable", "channel.model.batch_update"}},
	"channel.models.delete":        {"channel", []string{"channel.model.delete"}},
	"channel.models.exchange":      {"channel", []string{"channel.model.import", "channel.model.export"}},
	"channel.models.canonical":     {"channel", []string{"channel.model.canonical.preflight", "channel.model.canonical.merge"}},
	"channel.model_aliases":        {"channel", []string{"channel.model_alias.read", "channel.model_alias.create", "channel.model_alias.delete"}},
	"channel.model_usage":          {"channel", []string{"channel.model_usage.read"}},
	"channel.semantic_blocks":      {"channel", []string{"channel.usage_semantic_block.list", "channel.usage_semantic_block.resolve"}},
	"channel.model_mappings":       {"channel", []string{"channel.model_mapping.read", "channel.model_mapping.create", "channel.model_mapping.update", "channel.model_mapping.delete"}},
	"channel.model_routings":       {"channel", []string{"channel.model_routing.read", "channel.model_routing.create", "channel.model_routing.update", "channel.model_routing.delete"}},
	"channel.routing_groups.list":  {"channel", []string{"channel.routing_group.list"}},
	"channel.routing_groups.read":  {"channel", []string{"channel.routing_group.read", "channel.routing_group.members.read"}},
	"channel.routing_groups.write": {"channel", []string{"channel.routing_group.create", "channel.routing_group.update", "channel.routing_group.enable", "channel.routing_group.disable", "channel.routing_group.archive", "channel.routing_group.members.update", "channel.routing_group.resource_override.update"}},
	"channel.health":               {"channel", []string{"monitor.health.channel.read", "monitor.health.model.read", "monitor.health.selector.read"}},

	// B3 billing owner. Financial reads, writes and the protected backend
	// report export stay independent operations.
	"billing.accounts.read":           {"billing", []string{"billing.account.read", "billing.account.cost.read"}},
	"billing.accounts.adjust":         {"billing", []string{"billing.account.balance.adjust", "billing.account.balance.reset"}},
	"billing.ledger.read":             {"billing", []string{"billing.account.ledger.read"}},
	"billing.payments.list":           {"billing", []string{"billing.payment.list"}},
	"billing.payments.read":           {"billing", []string{"billing.payment.read"}},
	"billing.payments.refund":         {"billing", []string{"billing.payment.refund"}},
	"billing.redemption.list":         {"billing", []string{"billing.redemption.list"}},
	"billing.redemption.read":         {"billing", []string{"billing.redemption.read"}},
	"billing.redemption.write":        {"billing", []string{"billing.redemption.create", "billing.redemption.batch_create", "billing.redemption.update", "billing.redemption.delete"}},
	"billing.redemption.export":       {"billing", []string{"billing.redemption.export"}},
	"billing.reconciliation":          {"billing", []string{"billing.reconciliation.read", "billing.reconciliation.run", "billing.reconciliation.issues.read"}},
	"billing.pricing.read":            {"billing", []string{"billing.pricing.read"}},
	"billing.pricing.update":          {"billing", []string{"billing.pricing.update", "billing.pricing.import", "billing.pricing.export"}},
	"billing.upstream_costs":          {"billing", []string{"billing.upstream_cost.read", "billing.upstream_cost.create", "billing.upstream_cost.update", "billing.upstream_cost.delete", "billing.upstream_cost.migrate"}},
	"billing.routing_policy":          {"billing", []string{"billing.routing_policy.read", "billing.routing_policy.publish", "billing.routing_policy.user_override.read", "billing.routing_policy.user_override.update", "billing.routing_policy.user_override.delete"}},
	"subscription.quota_policies":     {"billing", []string{"subscription.quota_policy.list", "subscription.quota_policy.read", "subscription.quota_policy.create", "subscription.quota_policy.update", "subscription.quota_policy.delete"}},
	"subscription.plans":              {"billing", []string{"subscription.plan.list", "subscription.plan.read", "subscription.plan.create", "subscription.plan.update", "subscription.plan.publish", "subscription.plan.unpublish", "subscription.plan.delete"}},
	"subscription.user_subscriptions": {"billing", []string{"subscription.user_subscription.list", "subscription.user_subscription.read", "subscription.user_subscription.assign", "subscription.user_subscription.change", "subscription.user_subscription.extend", "subscription.user_subscription.revoke", "subscription.user_subscription.quota.reset", "subscription.user_subscription.report.read"}},
	"billing.report.export":           {"billing", []string{"billing.report.export"}},

	"admin.subscription.quota_policies":     {"admin", []string{"subscription.quota_policy.list", "subscription.quota_policy.read", "subscription.quota_policy.create", "subscription.quota_policy.update", "subscription.quota_policy.delete"}},
	"admin.subscription.plans":              {"admin", []string{"subscription.plan.list", "subscription.plan.read", "subscription.plan.create", "subscription.plan.update", "subscription.plan.publish", "subscription.plan.unpublish", "subscription.plan.delete"}},
	"admin.subscription.user_subscriptions": {"admin", []string{"subscription.user_subscription.list", "subscription.user_subscription.read", "subscription.user_subscription.assign", "subscription.user_subscription.change", "subscription.user_subscription.extend", "subscription.user_subscription.revoke", "subscription.user_subscription.quota.reset", "subscription.user_subscription.report.read"}},
	"billing.request_attempts":              {"billing", []string{"billing.account.ledger.read", "billing.account.cost.read"}},

	"admin.summary":     {"admin", []string{"identity.user.list", "channel.channel.list", "channel.account.list", "billing.account.ledger.read", "billing.account.cost.read", "billing.payment.list", "billing.reconciliation.read", "system.option.read", "channel.channel.read", "channel.account.read"}},
	"admin.routing_ops": {"admin", []string{"billing.account.ledger.read", "billing.account.cost.read", "channel.model.list", "billing.pricing.read", "monitor.health.selector.read"}},

	// B4 remaining owners.
	"log.requests.list":      {"log", []string{"log.request.list", "log.request.stats.read"}},
	"log.requests.read":      {"log", []string{"log.request.read", "log.request.content.read"}},
	"log.requests.export":    {"log", []string{"log.request.export"}},
	"log.requests.delete":    {"log", []string{"log.request.delete", "log.request.purge"}},
	"log.selection_events":   {"log", []string{"log.selection_event.list"}},
	"system.options.read":    {"config", []string{"system.option.read"}},
	"system.options.update":  {"config", []string{"system.option.update", "system.option.security.update", "system.option.payment.update", "system.option.pricing.update"}},
	"system.content":         {"config", []string{"system.content.notice.update", "system.content.about.update", "system.content.home.update"}},
	"monitor.health.service": {"monitor", []string{"monitor.health.service.read"}},
	"monitor.alert_rules":    {"monitor", []string{"monitor.alert_rule.list", "monitor.alert_rule.read", "monitor.alert_rule.create", "monitor.alert_rule.update", "monitor.alert_rule.delete"}},
	"notify.notifications":   {"notify", []string{"notify.notification.list", "notify.notification.read", "notify.notification.acknowledge", "notify.notification.test", "notify.notification.rules.update"}},
}

func Execution(code string) (ExecutionPoint, bool) {
	e, ok := executionPoints[code]
	e.Operations = slices.Clone(e.Operations)
	return e, ok
}
func ResourceBound(operation string) bool {
	if _, ok := completedResourceOperations[operation]; !ok {
		return false
	}
	for _, e := range executionPoints {
		if slices.Contains(e.Operations, operation) {
			return true
		}
	}
	return false
}

type ResourceRequest struct {
	ExecutionPoint, Operation string
	Object                    *ObjectFacts
}
type ResourceAuthorization struct {
	Mode     string
	Query    QueryScope
	Decision *Decision
}

// Planned execution declarations are separate from completed bindings. Draft
// operations cannot be enabled by catalog metadata before their owner slice
// and its scope/field tests exist.
var completedResourceOperations = func() map[string]bool {
	out := map[string]bool{}
	for _, point := range []string{
		"admin.console", "identity.routing_access", "identity.users.create", "identity.users.delete", "identity.users.list", "identity.users.read", "identity.users.update", "identity.users.export",
		"channel.channels.list", "channel.channels.read", "channel.channels.create", "channel.channels.update", "channel.channels.delete", "channel.channels.export", "channel.channels.secret",
		"channel.accounts.list", "channel.accounts.read", "channel.accounts.create", "channel.accounts.update", "channel.accounts.delete", "channel.accounts.quota", "channel.accounts.recovery", "channel.accounts.credential",
		"channel.routing_groups.list", "channel.routing_groups.read", "channel.routing_groups.write",
		"channel.models.list", "channel.models.read", "channel.models.create", "channel.models.update", "channel.models.delete", "channel.models.pricing", "channel.model_aliases", "channel.model_usage", "channel.model_mappings", "channel.accounts.oauth", "channel.health", "channel.channels.test", "channel.channels.balance", "channel.semantic_blocks", "channel.model_routings", "channel.models.exchange", "channel.models.canonical",
		"admin.subscription.quota_policies", "admin.subscription.plans", "admin.subscription.user_subscriptions", "billing.request_attempts", "admin.upstream_costs", "admin.pricing", "admin.system_options", "admin.content", "admin.routing_policy", "billing.redemption.list", "billing.redemption.read", "billing.redemption.write", "billing.redemption.export", "billing.reconciliation", "billing.routing_policy",
		"billing.accounts.read", "billing.accounts.adjust", "billing.ledger.read", "billing.payments.list", "billing.payments.read", "billing.payments.refund", "billing.report.export", "billing.pricing.read",
		"log.requests.list", "log.requests.read", "log.requests.delete", "log.requests.export", "log.selection_events", "system.options.read", "system.options.update", "system.content", "monitor.health.service", "monitor.alert_rules", "notify.notifications",
	} {
		for _, op := range executionPoints[point].Operations {
			out[op] = true
		}
	}
	return out
}()

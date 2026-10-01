package authorization

import (
	"errors"
	"slices"
	"strings"
)

const CatalogRevision uint64 = 1

// Operation declarations are code-owned. Every first-delivery item is unbound;
// adding metadata cannot publish an execution point or extend its scope/context.
type Operation struct {
	Code         string
	Resource     string
	Action       string
	Owner        string
	Scopes       []ScopeKind
	ContextTypes []string
	Protected    bool
	WholeObject  bool
	Binding      string
}

var ErrProtected = errors.New("protected authorization core cannot be modified")

func (o Operation) CheckLifecycle(action string) error {
	if o.Protected && (action == "disable" || action == "archive" || action == "unbind") {
		return ErrProtected
	}
	return nil
}
func CheckRoleMutation(code string) error {
	if code == "root" {
		return ErrProtected
	}
	return nil
}

func Lookup(code string) (Operation, bool) {
	o, ok := registry[code]
	o.Scopes = slices.Clone(o.Scopes)
	o.ContextTypes = slices.Clone(o.ContextTypes)
	return o, ok
}
func Catalog() []Operation {
	codes := make([]string, 0, len(registry))
	for code := range registry {
		codes = append(codes, code)
	}
	slices.Sort(codes)
	out := make([]Operation, 0, len(codes))
	for _, code := range codes {
		o, _ := Lookup(code)
		out = append(out, o)
	}
	return out
}

var registry = func() map[string]Operation {
	global := []ScopeKind{All}
	resources := []ScopeKind{All, Resources}
	users := []ScopeKind{All, Self, Users, Resources}
	resourcesGroups := []ScopeKind{All, Resources, Groups}
	userGroups := []ScopeKind{All, Self, Users, Resources, Groups}
	out := map[string]Operation{}
	for _, entry := range []struct {
		resource, actions, owner string
		scopes                   []ScopeKind
	}{
		{"admin.console", "enter", "admin", global},
		{"admin.overview", "read", "admin", global},
		{"identity.user", "list read create update enable disable delete export contact.read email_binding.update credential.update sessions.revoke", "identity", userGroups},
		{"identity.user_role", "read assign revoke batch_assign", "identity", resources},
		{"identity.session", "roles.read roles.activate self.revoke", "identity", users},
		{"identity.routing_access", "read grant revoke default.update public_access.update", "identity", userGroups},
		{"channel.channel", "list read create update enable disable delete batch_delete export test balance.refresh secret.read secret.rotate", "channel", resourcesGroups},
		{"channel.account", "list read create update enable disable delete quota.reset recovery.clear credential.update oauth.bind", "channel", resourcesGroups},
		{"channel.model", "list read create update enable disable delete batch_update import export canonical.preflight canonical.merge", "channel", resources},
		{"channel.model_alias", "read create delete", "channel", resources},
		{"channel.model_usage", "read", "channel", resources},
		{"channel.usage_semantic_block", "list resolve", "channel", resources},
		{"log.selection_event", "list", "log", users},
		{"channel.model_mapping", "read create update delete", "channel", resourcesGroups},
		{"channel.model_routing", "read create update delete", "channel", resourcesGroups},
		{"channel.routing_group", "list read create update enable disable archive members.read members.update resource_override.update", "channel", resources},
		{"monitor.health", "channel.read model.read selector.read", "monitor", resourcesGroups},
		{"billing.pricing", "read update import export", "billing", resources},
		{"billing.upstream_cost", "read create update delete migrate", "billing", resources},
		{"billing.routing_policy", "read publish user_override.read user_override.update user_override.delete", "billing", userGroups},
		{"billing.account", "read balance.adjust balance.reset ledger.read cost.read", "billing", users},
		{"billing.payment", "list read refund", "billing", users},
		{"billing.reconciliation", "read run issues.read", "billing", global},
		{"billing.redemption", "list read create batch_create update delete export", "billing", resources},
		{"subscription.quota_policy", "list read create update delete", "billing", resources},
		{"subscription.plan", "list read create update publish unpublish delete", "billing", resources},
		{"subscription.user_subscription", "list read assign change extend revoke quota.reset report.read", "billing", users},
		{"log.request", "list read stats.read export delete purge content.read", "log", users},
		{"system.option", "read update security.update payment.update pricing.update", "config", global},
		{"system.content", "notice.update about.update home.update", "config", global},
		{"notify.notification", "list read acknowledge test rules.update", "notify", resources},
		{"iam.permission", "list read create metadata.update enable disable archive references.read", "identity", resources},
		{"iam.role", "list read create update copy enable disable archive permissions.read permissions.update hierarchy.update members.read", "identity", resources},
		{"iam.delegation", "read create update revoke", "identity", resources},
		{"iam.constraint", "read create update delete", "identity", resources},
		{"iam.menu", "list read create update archive", "identity", resources},
		{"iam.authorization", "self.read user.read explain simulate", "identity", resources},
		{"iam.audit", "read export", "identity", resources},
		{"organization.organization", "list read create profile.update enable disable archive ownership.transfer", "identity", resources},
		{"organization.member", "list read invite update suspend restore remove export contact.read", "identity", resources},
		{"organization.member_role", "read assign revoke batch_assign", "identity", resources},
		{"organization.unit", "list read create update move archive members.read members.update", "identity", resources},
		{"billing.report", "export", "billing", users},
		{"monitor.health.service", "read", "monitor", resources},
		{"monitor.alert_rule", "list read create update delete", "monitor", resources},
	} {
		for _, action := range strings.Fields(entry.actions) {
			code := entry.resource + "." + action
			if _, exists := out[code]; exists {
				panic("duplicate authorization code: " + code)
			}
			// Read existence is separate from sensitive fields and from secret operations.
			read := slices.Contains([]string{"list", "read", "export", "import_preview", "enter", "roles.read", "self.read", "user.read", "explain", "simulate", "contact.read", "secret.read", "content.read", "stats.read", "cost.read", "ledger.read", "report.read", "references.read", "permissions.read", "members.read", "channel.read", "model.read", "selector.read", "issues.read", "user_override.read"}, action)
			protected := entry.resource == "iam.permission" || code == "iam.authorization.self.read" || strings.HasPrefix(code, "identity.session.")
			contexts := []string{"platform"}
			if strings.HasPrefix(entry.resource, "organization.") && entry.resource != "organization.organization" {
				contexts = []string{"organization"}
			}
			out[code] = Operation{Code: code, Resource: entry.resource, Action: action, Owner: entry.owner, Scopes: slices.Clone(entry.scopes), ContextTypes: contexts, Protected: protected, WholeObject: !read, Binding: "unbound"}
		}
	}
	return out
}()

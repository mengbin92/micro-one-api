package biz

import (
	"math"
	"regexp"
	"sort"
	"strconv"
	"strings"

	"micro-one-api/domain/routing"
)

// Audit-only projections. Numeric quota-policy IDs never enter the routing-key
// set. In particular, plan names and platforms cannot create routing grants.
type GroupAuditToken struct {
	ID     int64 `json:"id"`
	UserID int64 `json:"user_id"`
}

type GroupAuditQuotaPolicy struct {
	ID              int64    `json:"id"`
	Status          int32    `json:"status"`
	DailyLimitUSD   *float64 `json:"daily_limit_usd"`
	WeeklyLimitUSD  *float64 `json:"weekly_limit_usd"`
	MonthlyLimitUSD *float64 `json:"monthly_limit_usd"`
	RateMultiplier  float64  `json:"rate_multiplier"`
}

type GroupAuditPlan struct {
	ID            int64 `json:"id"`
	QuotaPolicyID int64 `json:"quota_policy_id"`
	ForSale       bool  `json:"for_sale"`
}

type GroupAuditSubscription struct {
	ID            int64  `json:"id"`
	UserID        int64  `json:"user_id"`
	QuotaPolicyID int64  `json:"quota_policy_id"`
	Status        string `json:"status"`
	StartsAt      int64  `json:"starts_at"`
	ExpiresAt     int64  `json:"expires_at"`
}

// Both pending and closed orders can still carry historical references. Paid
// but unissued orders must also retain their purchase-time contract.
type GroupAuditOrder struct {
	ID                    int64  `json:"id"`
	UserID                string `json:"user_id"`
	QuotaPolicyID         int64  `json:"quota_policy_id"`
	PlanID                int64  `json:"plan_id"`
	SubscriptionID        int64  `json:"subscription_id"`
	Status                string `json:"status"`
	AssetIssueStatus      string `json:"asset_issue_status"`
	SnapshotState         string `json:"snapshot_state"`
	SnapshotPlanID        int64  `json:"snapshot_plan_id,omitempty"`
	SnapshotQuotaPolicyID int64  `json:"snapshot_quota_policy_id,omitempty"`
}

type GroupAuditMigrationGroup struct {
	LegacyKey      string  `json:"legacy_key"`
	KeyDisposition string  `json:"key_disposition"`
	InitialStatus  string  `json:"initial_status"`
	AccessMode     string  `json:"access_mode"`
	BillingMode    string  `json:"billing_mode"`
	EffectivePrice float64 `json:"effective_price"`
	PriceSource    string  `json:"price_source"`
}

type GroupAuditMigrationUser struct {
	UserID            int64  `json:"user_id"`
	DefaultGroupKey   string `json:"default_group_key"`
	GrantSource       string `json:"grant_source"`
	PublicGroupAccess string `json:"public_group_access"`
}

type GroupAuditMigration struct {
	ReadyForBackfill         bool                       `json:"ready_for_backfill"`
	BlockingIssues           int                        `json:"blocking_issues"`
	Groups                   []GroupAuditMigrationGroup `json:"groups"`
	Users                    []GroupAuditMigrationUser  `json:"users"`
	TokenRoutingMode         string                     `json:"token_routing_mode"`
	SubscriptionCoverage     string                     `json:"subscription_coverage"`
	SubscriptionGrantsAccess bool                       `json:"subscription_grants_access"`
	QuotaPolicySemantics     string                     `json:"quota_policy_semantics"`
}

var auditNewGroupKey = regexp.MustCompile(`^[A-Za-z0-9._-]{1,64}$`)

func groupAuditDisposition(code string) (string, bool) {
	switch code {
	case "model_grant_outside_account_groups":
		return "preserve_model_scoped_mapping_without_adding_account_membership", false
	case "price_display_mismatch":
		return "freeze_effective_billing_price_and_source_not_displayed_price", false
	case "price_without_resource":
		return "retain_price_only_key_as_disabled_draft_unless_other_routing_references_exist", false
	case "resource_without_price_override":
		return "freeze_effective_base_or_missing_key_fallback", false
	case "duplicate_membership":
		return "deduplicate_exact_members_without_changing_candidate_weight", false
	case "legacy_order_without_snapshot":
		return "retain_legacy_fulfillment_without_inferring_routing_coverage", false
	case "legacy_group_key":
		return "import_original_bytes_via_legacy_key_mapping_never_normalize_or_truncate", false
	case "group_whitespace", "empty_group", "multiple_groups_in_single_key", "pattern_sensitive_group", "case_or_whitespace_collision", "grant_without_exact_group_reference":
		return "isolate_and_compare_effective_candidates_before_approving_exact_key_mapping", true
	case "billing_base_not_verified":
		return "supply_actual_billing_base_ratios_before_freezing_prices", true
	case "invalid_price_ratio", "invalid_quota_policy":
		return "record_current_effective_value_and_resolve_invalid_configuration_before_backfill", true
	case "invalid_order_snapshot", "order_snapshot_reference_mismatch":
		return "isolate_order_and_resolve_original_purchase_contract", true
	case "multiple_active_subscription_rows", "invalid_subscription_period":
		return "resolve_subscription_lifecycle_without_reassigning_or_duplicating_entitlements", true
	default:
		return "resolve_dangling_reference_before_backfill_without_inventing_grants", true
	}
}

func (report *GroupAuditReport) buildMigration(inventory *GroupInventory, issue func(string, GroupReference)) {
	migration := GroupAuditMigration{Groups: []GroupAuditMigrationGroup{}, Users: []GroupAuditMigrationUser{}, TokenRoutingMode: "inherit", SubscriptionCoverage: "legacy_all_authorized", QuotaPolicySemantics: "legacy_live"}
	users, routingKeys := map[int64]bool{}, map[string]bool{}
	// Canonical references are kept distinct from derived abilities. A grant
	// that only exists due to database collation / LIKE behaviour is quarantined.
	exactSources := map[string]map[routing.Source]bool{}
	modelSources := map[string]map[string]map[routing.Source]bool{}
	for _, ref := range inventory.References {
		if ref.Table == "users" {
			users[ref.ID] = true
			migration.Users = append(migration.Users, GroupAuditMigrationUser{UserID: ref.ID, DefaultGroupKey: ref.Group, GrantSource: "migration", PublicGroupAccess: "explicit_only"})
		}
		keys := []string{ref.Group}
		if ref.CSV {
			keys = routing.Groups(ref.Group)
		}
		for _, key := range keys {
			if key == "" {
				continue
			}
			if ref.Table != "GroupRatio" && ref.Table != "billing_base_ratios" {
				routingKeys[key] = true
			}
			if ref.Table == "channels" || ref.Table == "subscription_accounts" {
				if exactSources[key] == nil {
					exactSources[key] = map[routing.Source]bool{}
				}
				exactSources[key][ref.Source] = true
			}
			if ref.Table == "model_subscription_mapping" {
				if modelSources[key] == nil {
					modelSources[key] = map[string]map[routing.Source]bool{}
				}
				model := strings.ToLower(ref.Model)
				if modelSources[key][model] == nil {
					modelSources[key][model] = map[routing.Source]bool{}
				}
				modelSources[key][model][ref.Source] = true
			}
		}
	}
	for _, price := range report.Prices {
		keyDisposition := "exact_key"
		if !auditNewGroupKey.MatchString(price.Group) || price.Group == "auto" {
			keyDisposition = "preserve_legacy_key"
			issue("legacy_group_key", GroupReference{Table: "group_keys", Group: price.Group})
		}
		status := "disabled"
		if routingKeys[price.Group] {
			status = "enabled"
		}
		migration.Groups = append(migration.Groups, GroupAuditMigrationGroup{LegacyKey: price.Group, KeyDisposition: keyDisposition, InitialStatus: status, AccessMode: "restricted", BillingMode: "subscription_first", EffectivePrice: price.Effective, PriceSource: price.Source})
	}
	for _, grant := range report.Grants {
		if !exactSources[grant.Group][grant.Source] && !modelSources[grant.Group][strings.ToLower(grant.Model)][grant.Source] {
			issue("grant_without_exact_group_reference", GroupReference{Table: "effective_grants", Group: grant.Group, Model: grant.Model, Source: grant.Source})
		}
	}
	for _, token := range inventory.Tokens {
		if !users[token.UserID] {
			issue("missing_token_user", GroupReference{Table: "tokens", ID: token.ID})
		}
	}
	policies, plans, subscriptions := map[int64]bool{}, map[int64]bool{}, map[int64]bool{}
	for _, policy := range inventory.QuotaPolicies {
		policies[policy.ID] = true
		invalid := policy.RateMultiplier <= 0 || math.IsNaN(policy.RateMultiplier) || math.IsInf(policy.RateMultiplier, 0)
		for _, limit := range []*float64{policy.DailyLimitUSD, policy.WeeklyLimitUSD, policy.MonthlyLimitUSD} {
			invalid = invalid || (limit != nil && (*limit < 0 || math.IsNaN(*limit) || math.IsInf(*limit, 0)))
		}
		if invalid {
			issue("invalid_quota_policy", GroupReference{Table: "subscription_groups", ID: policy.ID})
		}
	}
	for _, plan := range inventory.Plans {
		plans[plan.ID] = true
		if !policies[plan.QuotaPolicyID] {
			issue("missing_plan_quota_policy", GroupReference{Table: "subscription_plans", ID: plan.ID})
		}
	}
	activeUsers := map[int64]bool{}
	for _, sub := range inventory.Subscriptions {
		subscriptions[sub.ID] = true
		ref := GroupReference{Table: "user_subscriptions", ID: sub.ID}
		if !policies[sub.QuotaPolicyID] {
			issue("missing_subscription_quota_policy", ref)
		}
		if !users[sub.UserID] {
			issue("missing_subscription_user", ref)
		}
		if sub.ExpiresAt <= sub.StartsAt {
			issue("invalid_subscription_period", ref)
		}
		if sub.Status == "active" {
			if activeUsers[sub.UserID] {
				issue("multiple_active_subscription_rows", ref)
			}
			activeUsers[sub.UserID] = true
		}
	}
	for _, order := range inventory.Orders {
		ref := GroupReference{Table: "payment_orders", ID: order.ID}
		userID, err := strconv.ParseInt(order.UserID, 10, 64)
		if err != nil || !users[userID] {
			issue("missing_order_user", ref)
		}
		if order.SubscriptionID != 0 && !subscriptions[order.SubscriptionID] {
			issue("missing_order_subscription", ref)
		}
		policyID := order.QuotaPolicyID
		switch order.SnapshotState {
		case "invalid":
			issue("invalid_order_snapshot", ref)
		case "present":
			policyID = order.SnapshotQuotaPolicyID
			if order.SnapshotPlanID != order.PlanID || (order.QuotaPolicyID != 0 && order.QuotaPolicyID != policyID) {
				issue("order_snapshot_reference_mismatch", ref)
			}
		default:
			issue("legacy_order_without_snapshot", ref)
			if order.PlanID != 0 && !plans[order.PlanID] {
				issue("missing_order_plan_without_snapshot", ref)
			}
		}
		if policyID != 0 && !policies[policyID] {
			issue("missing_order_quota_policy", ref)
		}
	}
	sort.Slice(migration.Users, func(i, j int) bool { return migration.Users[i].UserID < migration.Users[j].UserID })
	sort.Slice(report.Tokens, func(i, j int) bool { return report.Tokens[i].ID < report.Tokens[j].ID })
	sort.Slice(report.QuotaPolicies, func(i, j int) bool { return report.QuotaPolicies[i].ID < report.QuotaPolicies[j].ID })
	sort.Slice(report.Plans, func(i, j int) bool { return report.Plans[i].ID < report.Plans[j].ID })
	sort.Slice(report.Subscriptions, func(i, j int) bool { return report.Subscriptions[i].ID < report.Subscriptions[j].ID })
	sort.Slice(report.Orders, func(i, j int) bool { return report.Orders[i].ID < report.Orders[j].ID })
	for _, finding := range report.Issues {
		if finding.Blocking {
			migration.BlockingIssues++
		}
	}
	migration.ReadyForBackfill = migration.BlockingIssues == 0
	report.Migration = migration
}

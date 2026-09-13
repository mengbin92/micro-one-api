package biz

import (
	"context"
	"testing"

	"micro-one-api/domain/routing"

	"github.com/stretchr/testify/require"
)

type auditInventoryFake struct{ inventory *GroupInventory }

func (f auditInventoryFake) LoadGroupInventory(context.Context) (*GroupInventory, error) {
	return f.inventory, nil
}

type auditPermissionFake struct{}

func (auditPermissionFake) CanRoute(context.Context, string, string, routing.Source) (routing.Permission, error) {
	return routing.Permission{}, nil
}

func TestGroupAuditMigrationPreservesIndependentContracts(t *testing.T) {
	inv := &GroupInventory{
		References:     []GroupReference{{Table: "users", ID: 1, Group: "vip"}, {Table: "channels", ID: 1, Group: "vip", CSV: true, Source: routing.Source{Kind: routing.Channel, ID: 1}}},
		Sources:        []routing.Source{{Kind: routing.Channel, ID: 1}},
		GroupRatioJSON: `{"vip":2,"price-only":3}`,
		Tokens:         []GroupAuditToken{{ID: 5, UserID: 1}},
		QuotaPolicies:  []GroupAuditQuotaPolicy{{ID: 100, Status: 1, RateMultiplier: 2}},
		Plans:          []GroupAuditPlan{{ID: 101, QuotaPolicyID: 100, ForSale: true}},
		Subscriptions:  []GroupAuditSubscription{{ID: 102, UserID: 1, QuotaPolicyID: 100, Status: "active", StartsAt: 100, ExpiresAt: 200}},
		Orders:         []GroupAuditOrder{{ID: 103, UserID: "1", QuotaPolicyID: 100, PlanID: 101, SnapshotState: "absent"}},
	}
	report, err := NewGroupAuditUsecase(auditInventoryFake{inv}, auditPermissionFake{}).Run(context.Background(), nil, map[string]float64{"default": 1, "vip": 1, "base-only": .75}, 100)
	require.NoError(t, err)
	require.True(t, report.Migration.ReadyForBackfill, "%+v", report.Issues)
	require.Equal(t, "inherit", report.Migration.TokenRoutingMode)
	require.Equal(t, "legacy_all_authorized", report.Migration.SubscriptionCoverage)
	require.Equal(t, "legacy_live", report.Migration.QuotaPolicySemantics)
	require.False(t, report.Migration.SubscriptionGrantsAccess)
	require.Equal(t, []GroupAuditMigrationUser{{UserID: 1, DefaultGroupKey: "vip", GrantSource: "migration", PublicGroupAccess: "explicit_only"}}, report.Migration.Users)
	byKey := map[string]GroupAuditMigrationGroup{}
	for _, group := range report.Migration.Groups {
		byKey[group.LegacyKey] = group
	}
	require.Equal(t, "enabled", byKey["vip"].InitialStatus)
	require.Equal(t, "restricted", byKey["vip"].AccessMode)
	require.Equal(t, "subscription_first", byKey["vip"].BillingMode)
	require.Equal(t, 2., byKey["vip"].EffectivePrice)
	require.Equal(t, "disabled", byKey["price-only"].InitialStatus)
	require.Equal(t, "disabled", byKey["base-only"].InitialStatus)
	// Dynamic GroupRatio replaces the base map; missing keys use 1, not
	// the supplied base entry or the admin page's synthetic display defaults.
	require.Equal(t, 1., byKey["base-only"].EffectivePrice)
	require.NotContains(t, byKey, "100")
}

func TestGroupAuditQuarantinesInvalidReferencesWithoutGrantInference(t *testing.T) {
	inv := &GroupInventory{
		References:    []GroupReference{{Table: "users", ID: 1, Group: "VIP"}, {Table: "channels", ID: 1, Group: "vip, a_b, a%b,中文", CSV: true}},
		Tokens:        []GroupAuditToken{{ID: 1, UserID: 2}},
		QuotaPolicies: []GroupAuditQuotaPolicy{{ID: 1, RateMultiplier: 0}},
		Plans:         []GroupAuditPlan{{ID: 1, QuotaPolicyID: 404}},
		Subscriptions: []GroupAuditSubscription{{ID: 1, UserID: 1, QuotaPolicyID: 1, Status: "active", StartsAt: 100, ExpiresAt: 200}, {ID: 2, UserID: 1, QuotaPolicyID: 1, Status: "active", StartsAt: 200, ExpiresAt: 100}},
		Orders:        []GroupAuditOrder{{ID: 1, UserID: "2", SubscriptionID: 404, SnapshotState: "invalid"}, {ID: 2, UserID: "1", PlanID: 1, QuotaPolicyID: 1, SnapshotState: "present", SnapshotPlanID: 2, SnapshotQuotaPolicyID: 404}},
	}
	report, err := NewGroupAuditUsecase(auditInventoryFake{inv}, auditPermissionFake{}).Run(context.Background(), nil, nil, 10)
	require.NoError(t, err)
	require.False(t, report.Migration.ReadyForBackfill)
	codes := map[string]bool{}
	for _, finding := range report.Issues {
		codes[finding.Code] = true
		require.NotEmpty(t, finding.Action)
	}
	for _, code := range []string{"billing_base_not_verified", "case_or_whitespace_collision", "pattern_sensitive_group", "legacy_group_key", "missing_token_user", "invalid_quota_policy", "missing_plan_quota_policy", "multiple_active_subscription_rows", "invalid_subscription_period", "missing_order_user", "missing_order_subscription", "invalid_order_snapshot", "order_snapshot_reference_mismatch", "missing_order_quota_policy"} {
		require.True(t, codes[code], code)
	}
	for _, group := range report.Migration.Groups {
		require.NotEqual(t, "public", group.AccessMode)
	}
}

func TestGroupAuditDynamicRatioResolutionMatchesBillingFallback(t *testing.T) {
	for _, tc := range []struct {
		raw      string
		expected float64
		source   string
	}{
		{"", .5, "supplied_billing_base"}, {"null", .5, "supplied_billing_base"}, {"{}", .5, "supplied_billing_base"},
		{`{"default":2}`, 1, "GroupRatio:missing_key_fallback_1"}, {`{"vip":0}`, 1, "GroupRatio:missing_key_fallback_1"}, {`{"vip":2}`, 2, "GroupRatio"},
	} {
		t.Run(tc.raw, func(t *testing.T) {
			inv := &GroupInventory{References: []GroupReference{{Table: "users", ID: 1, Group: "vip"}}, GroupRatioJSON: tc.raw}
			report, err := NewGroupAuditUsecase(auditInventoryFake{inv}, auditPermissionFake{}).Run(context.Background(), nil, map[string]float64{"vip": .5}, 10)
			require.NoError(t, err)
			for _, price := range report.Prices {
				if price.Group == "vip" {
					require.Equal(t, tc.expected, price.Effective)
					require.Equal(t, tc.source, price.Source)
					return
				}
			}
			t.Fatal("missing vip price")
		})
	}
}

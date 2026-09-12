package biz

import (
	"cmp"
	"context"
	"fmt"
	"math"
	"sort"
	"strings"

	"micro-one-api/domain/routing"
	"micro-one-api/pkg/jsonx"
	"micro-one-api/pkg/wildcard"
)

// GroupReference contains only routing metadata, never usernames or secrets.
type GroupReference struct {
	Table   string         `json:"table"`
	ID      int64          `json:"id"`
	Group   string         `json:"group"`
	CSV     bool           `json:"csv,omitempty"`
	Source  routing.Source `json:"source,omitempty"`
	Model   string         `json:"model,omitempty"`
	ModelPK int64          `json:"model_pk,omitempty"`
}

type GroupInventory struct {
	References     []GroupReference
	Sources        []routing.Source
	Models         []string
	GroupRatioJSON string
	Tokens         []GroupAuditToken
	QuotaPolicies  []GroupAuditQuotaPolicy
	Plans          []GroupAuditPlan
	Subscriptions  []GroupAuditSubscription
	Orders         []GroupAuditOrder
}

type GroupInventoryRepo interface {
	LoadGroupInventory(context.Context) (*GroupInventory, error)
}

type RoutePermissionChecker interface {
	CanRoute(context.Context, string, string, routing.Source) (routing.Permission, error)
}

type GroupAuditIssue struct {
	Code      string         `json:"code"`
	Reference GroupReference `json:"reference"`
	Action    string         `json:"action"`
	Blocking  bool           `json:"blocking"`
}

type GroupAuditGrant struct {
	Group           string         `json:"group"`
	Model           string         `json:"model"`
	Source          routing.Source `json:"source"`
	UpstreamModelID string         `json:"upstream_model_id,omitempty"`
}

type GroupAuditPrice struct {
	Group            string   `json:"group"`
	Configured       *float64 `json:"configured,omitempty"`
	AdminDisplayed   *float64 `json:"admin_displayed,omitempty"`
	AccountDisplayed float64  `json:"account_displayed"`
	Effective        float64  `json:"effective"`
	Source           string   `json:"source"`
}

type GroupAuditReport struct {
	Version       int                      `json:"version"`
	Coverage      string                   `json:"coverage"`
	Groups        []string                 `json:"groups"`
	Models        []string                 `json:"models"`
	References    []GroupReference         `json:"references"`
	Issues        []GroupAuditIssue        `json:"issues"`
	Grants        []GroupAuditGrant        `json:"grants"`
	Prices        []GroupAuditPrice        `json:"prices"`
	Probes        int                      `json:"probes"`
	Migration     GroupAuditMigration      `json:"migration"`
	Tokens        []GroupAuditToken        `json:"tokens"`
	QuotaPolicies []GroupAuditQuotaPolicy  `json:"quota_policies"`
	Plans         []GroupAuditPlan         `json:"plans"`
	Subscriptions []GroupAuditSubscription `json:"subscriptions"`
	Orders        []GroupAuditOrder        `json:"orders"`
}

type GroupAuditUsecase struct {
	repo    GroupInventoryRepo
	checker RoutePermissionChecker
}

func NewGroupAuditUsecase(repo GroupInventoryRepo, checker RoutePermissionChecker) *GroupAuditUsecase {
	return &GroupAuditUsecase{repo: repo, checker: checker}
}

// Run freezes an effective authorization matrix through the production policy
// implementation, within the caller's read-only database snapshot. Scheduling
// health and quota are deliberately excluded; wildcard coverage is finite and
// explicit. An incomplete probe run is an error, never a successful report.
func (uc *GroupAuditUsecase) Run(ctx context.Context, extraModels []string, baseRatios map[string]float64, maxProbes int) (*GroupAuditReport, error) {
	inventory, err := uc.repo.LoadGroupInventory(ctx)
	if err != nil {
		return nil, err
	}
	configured := map[string]float64{}
	if strings.TrimSpace(inventory.GroupRatioJSON) != "" {
		if err := jsonx.Unmarshal([]byte(inventory.GroupRatioJSON), &configured); err != nil {
			return nil, fmt.Errorf("invalid GroupRatio JSON")
		}
	}
	for key := range configured {
		inventory.References = append(inventory.References, GroupReference{Table: "GroupRatio", Group: key})
	}
	for key := range baseRatios {
		inventory.References = append(inventory.References, GroupReference{Table: "billing_base_ratios", Group: key})
	}
	report := &GroupAuditReport{Version: 2, Coverage: "registered and literal ability models plus explicit probes; wildcard rules are retained in references; excludes health/quota/concurrency; subscription references retain legacy coverage without routing access grants", References: inventory.References, Issues: []GroupAuditIssue{}, Grants: []GroupAuditGrant{}, Prices: []GroupAuditPrice{}, Tokens: inventory.Tokens, QuotaPolicies: inventory.QuotaPolicies, Plans: inventory.Plans, Subscriptions: inventory.Subscriptions, Orders: inventory.Orders}
	groups, resourceGroups := map[string]bool{}, map[string]bool{}
	memberships := map[routing.Source]string{}
	sources := map[routing.Source]bool{}
	for _, source := range inventory.Sources {
		sources[source] = true
	}
	issue := func(code string, ref GroupReference) {
		action, blocking := groupAuditDisposition(code)
		report.Issues = append(report.Issues, GroupAuditIssue{Code: code, Reference: ref, Action: action, Blocking: blocking})
	}
	for _, ref := range inventory.References {
		if ref.CSV {
			memberships[ref.Source] = ref.Group
		}
	}
	caseKeys := map[string]map[string]bool{}
	for _, ref := range inventory.References {
		if ref.Source.Kind != "" && !sources[ref.Source] {
			issue("missing_source_reference", ref)
		}
		if ref.ModelPK != 0 && ref.Model == "" {
			issue("missing_model_reference", ref)
		}
		values := []string{ref.Group}
		if ref.CSV {
			values = strings.Split(ref.Group, ",")
		}
		seen := map[string]bool{}
		for _, raw := range values {
			key := raw
			if ref.CSV {
				key = strings.TrimSpace(raw)
			}
			if strings.TrimSpace(raw) == "" {
				issue("empty_group", ref)
				continue
			}
			if raw != strings.TrimSpace(raw) {
				issue("group_whitespace", ref)
			}
			if seen[key] {
				issue("duplicate_membership", ref)
			}
			seen[key] = true
			if !ref.CSV && strings.Contains(key, ",") {
				issue("multiple_groups_in_single_key", ref)
			}
			if strings.ContainsAny(key, "%_*?!") {
				issue("pattern_sensitive_group", ref)
			}
			groups[key] = true
			if sources[ref.Source] {
				resourceGroups[key] = true
			}
			fold := strings.ToLower(strings.TrimSpace(key))
			if caseKeys[fold] == nil {
				caseKeys[fold] = map[string]bool{}
			}
			caseKeys[fold][key] = true
		}
		if ref.Table == "model_subscription_mapping" && !routing.ContainsGroup(memberships[ref.Source], ref.Group) {
			issue("model_grant_outside_account_groups", ref)
		}
	}
	for _, keys := range caseKeys {
		if len(keys) > 1 {
			for key := range keys {
				issue("case_or_whitespace_collision", GroupReference{Table: "group_keys", Group: key})
			}
		}
	}
	if len(positiveAuditRatios(baseRatios)) == 0 {
		issue("billing_base_not_verified", GroupReference{Table: "billing_base_ratios"})
	}
	for key, ratio := range baseRatios {
		if key == "" || ratio <= 0 || math.IsNaN(ratio) || math.IsInf(ratio, 0) {
			issue("invalid_price_ratio", GroupReference{Table: "billing_base_ratios", Group: key})
		}
	}
	for key := range configured {
		groups[key] = true
	}
	groups[routing.DefaultGroup] = true
	for key := range groups {
		report.Groups = append(report.Groups, key)
	}
	sort.Strings(report.Groups)
	defaults := map[string]float64{"default": 1, "vip": .5, "svip": .3}
	base := positiveAuditRatios(baseRatios)
	baseSource := "supplied_billing_base"
	if len(base) == 0 {
		base = defaults
		baseSource = "builtin_base_assumption"
	}
	effective := base
	if len(configured) > 0 {
		effective = positiveAuditRatios(configured)
		baseSource = "GroupRatio"
	}
	for _, key := range report.Groups {
		entry := GroupAuditPrice{Group: key, AccountDisplayed: 1, Effective: 1, Source: baseSource}
		if ratio, ok := configured[key]; ok {
			entry.Configured = &ratio
			entry.AdminDisplayed = &ratio
			if ratio <= 0 || math.IsNaN(ratio) || math.IsInf(ratio, 0) {
				issue("invalid_price_ratio", GroupReference{Table: "GroupRatio", Group: key})
			}
			if !resourceGroups[key] {
				issue("price_without_resource", GroupReference{Table: "GroupRatio", Group: key})
			}
		} else if resourceGroups[key] {
			issue("resource_without_price_override", GroupReference{Table: "GroupRatio", Group: key})
		}
		if key == routing.DefaultGroup && entry.AdminDisplayed == nil {
			ratio := 1.
			entry.AdminDisplayed = &ratio
		}
		if ratio, ok := effective[key]; ok {
			entry.Effective = ratio
		} else {
			entry.Source += ":missing_key_fallback_1"
		}
		if ratio, ok := defaults[key]; ok {
			entry.AccountDisplayed = ratio
		}
		if entry.AccountDisplayed != entry.Effective || (entry.AdminDisplayed != nil && *entry.AdminDisplayed != entry.Effective) {
			issue("price_display_mismatch", GroupReference{Table: "GroupRatio", Group: key})
		}
		report.Prices = append(report.Prices, entry)
	}
	modelSet := map[string]bool{}
	for _, model := range append(inventory.Models, extraModels...) {
		if model != "" && !wildcard.IsPattern(model) {
			modelSet[model] = true
		}
	}
	for model := range modelSet {
		report.Models = append(report.Models, model)
	}
	sort.Strings(report.Models)
	sort.Slice(inventory.Sources, func(i, j int) bool {
		a, b := inventory.Sources[i], inventory.Sources[j]
		if a.Kind != b.Kind {
			return a.Kind < b.Kind
		}
		return a.ID < b.ID
	})
	for _, group := range report.Groups {
		for _, model := range report.Models {
			for _, source := range inventory.Sources {
				if report.Probes >= maxProbes {
					return nil, fmt.Errorf("authorization probe limit %d exceeded; narrow data or explicitly increase --max-probes", maxProbes)
				}
				if err := ctx.Err(); err != nil {
					return nil, err
				}
				permission, err := uc.checker.CanRoute(ctx, group, model, source)
				if err != nil {
					return nil, fmt.Errorf("authorization probe failed: %w", err)
				}
				report.Probes++
				if permission.Allowed {
					report.Grants = append(report.Grants, GroupAuditGrant{Group: group, Model: model, Source: source, UpstreamModelID: permission.UpstreamModelID})
				}
			}
		}
	}
	report.buildMigration(inventory, issue)
	sort.Slice(report.References, func(i, j int) bool { return auditReferenceLess(report.References[i], report.References[j]) })
	sort.Slice(report.Issues, func(i, j int) bool {
		a, b := report.Issues[i], report.Issues[j]
		if a.Code != b.Code {
			return a.Code < b.Code
		}
		return auditReferenceLess(a.Reference, b.Reference)
	})
	return report, nil
}

func positiveAuditRatios(values map[string]float64) map[string]float64 {
	result := map[string]float64{}
	for key, value := range values {
		if key != "" && value > 0 && !math.IsNaN(value) && !math.IsInf(value, 0) {
			result[key] = value
		}
	}
	return result
}

func auditReferenceLess(a, b GroupReference) bool {
	return cmp.Or(cmp.Compare(a.Table, b.Table), cmp.Compare(a.ID, b.ID), cmp.Compare(a.Group, b.Group), cmp.Compare(a.Model, b.Model), cmp.Compare(a.ModelPK, b.ModelPK), cmp.Compare(a.Source.Kind, b.Source.Kind), cmp.Compare(a.Source.ID, b.Source.ID)) < 0
}

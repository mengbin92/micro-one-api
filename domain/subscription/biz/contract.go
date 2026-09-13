package biz

import (
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"math"
	"os"
	"sort"
	"strings"

	"micro-one-api/domain/routing"
	"micro-one-api/pkg/jsonx"
)

const (
	CoverageSelectedGroups      = "selected_groups"
	CoverageLegacyAllAuthorized = "legacy_all_authorized"
)

// RoutingCoverage is both a payment coverage and, optionally, an access grant.
// Numeric IDs belong to channel; they are unrelated to quota policy IDs.
type RoutingCoverage struct {
	GroupID      int64  `json:"routing_group_id"`
	GroupKey     string `json:"routing_group_key"`
	GrantsAccess bool   `json:"grants_access"`
}

type QuotaPolicySnapshot struct {
	ID             int64    `json:"id"`
	Version        string   `json:"version"`
	DailyLimit     *float64 `json:"daily_limit_usd"`
	WeeklyLimit    *float64 `json:"weekly_limit_usd"`
	MonthlyLimit   *float64 `json:"monthly_limit_usd"`
	RateMultiplier float64  `json:"rate_multiplier"`
}

type SubscriptionContract struct {
	Version      int32               `json:"version"`
	CoverageMode string              `json:"coverage_mode"`
	Coverage     []RoutingCoverage   `json:"coverage"`
	QuotaPolicy  QuotaPolicySnapshot `json:"quota_policy"`
	Digest       string              `json:"digest"`
}

func EntitlementsEnabled() bool {
	return strings.EqualFold(strings.TrimSpace(os.Getenv("SUBSCRIPTION_ENTITLEMENTS_V2")), "true")
}

// ContractGroupReader supplies current route identity and payment eligibility.
// Implementations use service contracts, never another service's tables.
type ContractGroupReader interface {
	ValidateSubscriptionGroup(context.Context, int64) (string, error)
}

func contractHash(v any) string {
	raw, err := jsonx.Marshal(v)
	if err != nil {
		return ""
	}
	return fmt.Sprintf("%x", sha256.Sum256(raw))
}

func NewContract(group *SubscriptionGroup, coverage []RoutingCoverage) (*SubscriptionContract, error) {
	if group == nil || group.ID <= 0 || len(coverage) == 0 {
		return nil, ErrSubscriptionContractInvalid
	}
	q := QuotaPolicySnapshot{ID: group.ID, DailyLimit: group.DailyLimitUSD, WeeklyLimit: group.WeeklyLimitUSD, MonthlyLimit: group.MonthlyLimitUSD, RateMultiplier: group.RateMultiplier}
	q.Version = contractHash(q)
	c := &SubscriptionContract{Version: 2, CoverageMode: CoverageSelectedGroups, Coverage: append([]RoutingCoverage(nil), coverage...), QuotaPolicy: q}
	sort.Slice(c.Coverage, func(i, j int) bool { return c.Coverage[i].GroupID < c.Coverage[j].GroupID })
	c.Digest = contractHash(c)
	if err := c.Validate(); err != nil {
		return nil, err
	}
	return CloneContract(c), nil
}

func (c *SubscriptionContract) Validate() error {
	if c == nil || c.Version != 2 || c.CoverageMode != CoverageSelectedGroups || len(c.Coverage) == 0 || c.QuotaPolicy.ID <= 0 {
		return ErrSubscriptionContractInvalid
	}
	q := c.QuotaPolicy
	if q.RateMultiplier <= 0 || math.IsNaN(q.RateMultiplier) || math.IsInf(q.RateMultiplier, 0) {
		return ErrSubscriptionContractInvalid
	}
	for _, n := range []*float64{q.DailyLimit, q.WeeklyLimit, q.MonthlyLimit} {
		if n != nil && (*n < 0 || math.IsNaN(*n) || math.IsInf(*n, 0)) {
			return ErrSubscriptionContractInvalid
		}
	}
	version := q.Version
	q.Version = ""
	if version == "" || version != contractHash(q) {
		return ErrSubscriptionContractInvalid
	}
	var previous int64
	for _, g := range c.Coverage {
		if g.GroupID <= previous || strings.TrimSpace(g.GroupKey) == "" {
			return ErrSubscriptionContractInvalid
		}
		previous = g.GroupID
	}
	copy := *c
	copy.Digest = ""
	if c.Digest == "" || c.Digest != contractHash(&copy) {
		return ErrSubscriptionContractInvalid
	}
	return nil
}
func CloneContract(c *SubscriptionContract) *SubscriptionContract {
	if c == nil {
		return nil
	}
	raw, _ := jsonx.Marshal(c)
	var copy SubscriptionContract
	_ = jsonx.Unmarshal(raw, &copy)
	return &copy
}
func (c *SubscriptionContract) Covers(groupID int64) bool {
	if c == nil {
		return true
	} // legacy fees only; never an access grant
	for _, g := range c.Coverage {
		if g.GroupID == groupID {
			return true
		}
	}
	return false
}
func (c *SubscriptionContract) Group() *SubscriptionGroup {
	if c == nil {
		return nil
	}
	q := c.QuotaPolicy
	return &SubscriptionGroup{ID: q.ID, Status: SubscriptionGroupStatusEnabled, DailyLimitUSD: q.DailyLimit, WeeklyLimitUSD: q.WeeklyLimit, MonthlyLimitUSD: q.MonthlyLimit, RateMultiplier: q.RateMultiplier}
}
func (s *UserSubscription) RoutingGrants(now int64) []routing.UserGroupGrant {
	if s == nil || s.Contract == nil || s.Contract.Validate() != nil || s.Status != SubscriptionStatusActive || s.StartsAt > now || s.ExpiresAt <= now {
		return nil
	}
	var grants []routing.UserGroupGrant
	for _, c := range s.Contract.Coverage {
		if c.GrantsAccess {
			grants = append(grants, routing.UserGroupGrant{GroupID: c.GroupID, SourceType: "subscription", SourceRef: fmt.Sprintf("%d", s.ID), StartsAt: s.StartsAt, ExpiresAt: s.ExpiresAt, Status: "active"})
		}
	}
	return grants
}

func (uc *SubscriptionUsecase) prepareAssignment(ctx context.Context, tx Tx, req *AssignSubscriptionRequest) (*SubscriptionGroup, error) {
	if req.Contract != nil {
		if req.Contract.Validate() != nil || req.Contract.QuotaPolicy.ID != req.GroupID {
			return nil, ErrSubscriptionContractInvalid
		}
		return req.Contract.Group(), nil
	}
	if !EntitlementsEnabled() && len(req.Coverage) > 0 {
		return nil, ErrSubscriptionRoutingUnavailable
	}
	var group *SubscriptionGroup
	var err error
	if tx != nil {
		group, err = uc.groupRepo.GetGroupByIDInTx(ctx, tx, req.GroupID)
	} else {
		group, err = uc.groupRepo.GetGroupByID(ctx, req.GroupID)
	}
	if err != nil {
		return nil, err
	}
	if !EntitlementsEnabled() || req.LegacyPurchase {
		return group, nil
	}
	if len(req.Coverage) == 0 {
		return nil, ErrSubscriptionContractInvalid
	}
	if uc.routingGroups == nil {
		return nil, ErrSubscriptionRoutingUnavailable
	}
	coverage := append([]RoutingCoverage(nil), req.Coverage...)
	for i := range coverage {
		key, err := uc.routingGroups.ValidateSubscriptionGroup(ctx, coverage[i].GroupID)
		if err != nil {
			return nil, err
		}
		coverage[i].GroupKey = key
	}
	req.Contract, err = NewContract(group, coverage)
	return group, err
}

func (uc *SubscriptionUsecase) GetRoutingEntitlements(ctx context.Context, userID int64) (*routing.EntitlementFacts, error) {
	s, err := uc.repo.GetActiveSubscriptionByUser(ctx, userID)
	if errors.Is(err, ErrSubscriptionNotFound) {
		return &routing.EntitlementFacts{}, nil
	}
	if err != nil {
		return nil, err
	}
	if s == nil || s.Status != SubscriptionStatusActive || s.StartsAt > uc.now().Unix() || s.ExpiresAt <= uc.now().Unix() {
		return &routing.EntitlementFacts{}, nil
	}
	if s.Contract != nil && s.Contract.Validate() != nil {
		return nil, ErrSubscriptionContractInvalid
	}
	return &routing.EntitlementFacts{SubscriptionID: s.ID, Revision: s.EntitlementRevision, Grants: s.RoutingGrants(uc.now().Unix())}, nil
}

// ValidateRenewalContract rejects a paid order that would require an unscheduled
// contract replacement. Fulfilment repeats this check inside its transaction.
func (uc *SubscriptionUsecase) ValidateRenewalContract(ctx context.Context, userID, groupID int64, contract *SubscriptionContract) error {
	active, err := uc.repo.GetActiveSubscriptionByUser(ctx, userID)
	if errors.Is(err, ErrSubscriptionNotFound) {
		return nil
	}
	if err != nil {
		return err
	}
	if active == nil {
		return nil
	}
	if active.Contract == nil && contract == nil && active.GroupID == groupID {
		return nil
	}
	if active.Contract != nil && contract != nil && active.Contract.Digest == contract.Digest {
		return nil
	}
	if pending, ok := pendingChangeMetadata(active.Metadata); ok && pending.ToGroupID == groupID {
		if pending.Contract == nil && contract == nil {
			return nil
		}
		if pending.Contract != nil && contract != nil && pending.Contract.Digest == contract.Digest {
			return nil
		}
	}
	return ErrSubscriptionContractConflict
}

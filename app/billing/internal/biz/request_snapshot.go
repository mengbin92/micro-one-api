package biz

import (
	"context"
	"crypto/sha256"
	"fmt"
	"math"
	"os"
	"strconv"
	"strings"

	"github.com/go-kratos/kratos/v3/errors"
	billingv1 "micro-one-api/api/billing/v1"
	"micro-one-api/domain/routing"
	subscriptionbiz "micro-one-api/domain/subscription/biz"
	"micro-one-api/pkg/jsonx"
)

var (
	ErrRoutingContextInvalid      = errors.BadRequest(billingv1.RoutingBillingErrorReason_ROUTING_CONTEXT_INVALID.String(), "invalid routing context")
	ErrRoutingContextConflict     = errors.Conflict(billingv1.RoutingBillingErrorReason_ROUTING_CONTEXT_CONFLICT.String(), "request id already belongs to another model or routing context")
	ErrRequestSnapshotUnavailable = errors.ServiceUnavailable(billingv1.RoutingBillingErrorReason_REQUEST_SNAPSHOT_UNAVAILABLE.String(), "request snapshot capability unavailable")
	ErrRequestSnapshotInvalid     = errors.InternalServer(billingv1.RoutingBillingErrorReason_REQUEST_SNAPSHOT_INVALID.String(), "invalid persisted request snapshot")
)

type RoutingGroupReader interface {
	GetRoutingGroup(context.Context, int64) (*routing.Group, error)
}

type FrozenReservationRepo interface {
	SumActiveFrozenAccountingInTx(context.Context, subscriptionbiz.Tx, string, int64, int64, int64, int64, float64) (float64, float64, float64, error)
}

type FrozenSubscriptionRecorder interface {
	RecordFrozenUsageInTx(context.Context, subscriptionbiz.Tx, subscriptionbiz.FrozenWindowCharge) error
}

func (uc *BillingUsecase) frozenAccounting(ctx context.Context, tx subscriptionbiz.Tx, userID string, subID, daily, weekly, monthly int64, multiplier float64, useSnapshot bool) (float64, float64, float64, error) {
	if useSnapshot || RequestSnapshotsEnabled() {
		repo, ok := uc.reservationRepo.(FrozenReservationRepo)
		if !ok {
			return 0, 0, 0, ErrRequestSnapshotUnavailable
		}
		return repo.SumActiveFrozenAccountingInTx(ctx, tx, userID, subID, daily, weekly, monthly, multiplier)
	}
	d, w, m, _, err := uc.reservationRepo.SumActiveFrozenInTx(ctx, tx, userID, subID, daily, weekly, monthly)
	return d * multiplier, w * multiplier, m * multiplier, err
}

func (uc *BillingUsecase) recordReservedSubscriptionUsage(ctx context.Context, tx subscriptionbiz.Tx, r *Reservation, usd float64, now int64) error {
	if r.RequestSnapshot == nil {
		return uc.subscription.RecordUsageForSubscriptionInTx(ctx, tx, r.SubscriptionID, usd, now)
	}
	f := r.RequestSnapshot.Subscription
	if f == nil {
		return ErrRequestSnapshotInvalid
	}
	recorder, ok := uc.subscription.(FrozenSubscriptionRecorder)
	if !ok {
		return ErrRequestSnapshotUnavailable
	}
	return recorder.RecordFrozenUsageInTx(ctx, tx, subscriptionbiz.FrozenWindowCharge{ReservationID: r.ReservationID, SubscriptionID: r.SubscriptionID, QuotaPolicyID: f.PolicyID, DailyWindowStart: r.SubscriptionDailyWindowStart, WeeklyWindowStart: r.SubscriptionWeeklyWindowStart, MonthlyWindowStart: r.SubscriptionMonthlyWindowStart, AccountingUSD: usd * f.RateMultiplier, CreatedAt: now})
}

func (uc *BillingUsecase) frozenSubscriptionAbsorbUSD(ctx context.Context, tx subscriptionbiz.Tx, r *Reservation, costUSD float64) (float64, error) {
	if r.SubscriptionID <= 0 {
		return 0, nil
	}
	f := r.RequestSnapshot.Subscription
	if f == nil || uc.subscription == nil {
		return 0, ErrRequestSnapshotInvalid
	}
	reserved := math.Min(costUSD, r.SubscriptionAmountUSD)
	if costUSD <= reserved {
		return reserved, nil
	}
	if err := uc.reservationRepo.LockSubscriptionRow(ctx, tx, r.SubscriptionID); err != nil {
		return 0, err
	}
	userID, err := strconv.ParseInt(r.UserID, 10, 64)
	if err != nil {
		return 0, ErrRequestSnapshotInvalid
	}
	sub, err := uc.subscription.GetActiveSubscriptionForUserInTx(ctx, tx, userID)
	if errors.Is(err, subscriptionbiz.ErrSubscriptionNotFound) {
		return reserved, nil
	}
	if err != nil {
		return 0, err
	}
	if sub == nil || sub.ID != r.SubscriptionID || sub.GroupID != f.PolicyID || (sub.Contract != nil && sub.Contract.Digest != f.ContractVersion) {
		return reserved, nil
	}
	rolled := subscriptionbiz.RollUsageWindowsPure(sub, uc.Now().Unix())
	// Additional absorption is permitted only within all original windows.
	// Admitted work keeps its reserved entitlement even after it is revoked.
	if rolled.DailyWindowStart != r.SubscriptionDailyWindowStart || rolled.WeeklyWindowStart != r.SubscriptionWeeklyWindowStart || rolled.MonthlyWindowStart != r.SubscriptionMonthlyWindowStart {
		return reserved, nil
	}
	d, w, m, err := uc.frozenAccounting(ctx, tx, r.UserID, sub.ID, rolled.DailyWindowStart, rolled.WeeklyWindowStart, rolled.MonthlyWindowStart, f.RateMultiplier, true)
	if err != nil {
		return 0, err
	}
	window := subscriptionbiz.AbsorbableWindow{DailyStart: rolled.DailyWindowStart, WeeklyStart: rolled.WeeklyWindowStart, MonthlyStart: rolled.MonthlyWindowStart, DailyUsageUSD: rolled.DailyUsageUSD, WeeklyUsageUSD: rolled.WeeklyUsageUSD, MonthlyUsageUSD: rolled.MonthlyUsageUSD, DailyLimit: f.DailyLimit, WeeklyLimit: f.WeeklyLimit, MonthlyLimit: f.MonthlyLimit, FrozenDailyAccountingUSD: d, FrozenWeeklyAccountingUSD: w, FrozenMonthlyAccountingUSD: m}
	available := subscriptionbiz.ComputeAbsorbablePure(window, f.RateMultiplier, 0).AbsorbableUSD
	return math.Min(costUSD, math.Max(reserved, available)), nil
}

// RequestSnapshot is the v2 reserve-time evidence. PricingSnapshot v1 remains
// readable and unchanged. The legacy_live policy is captured per request, not
// retroactively applied to the customer's purchased contract.
type RequestSnapshot struct {
	CostBound            *routing.CostBound `json:",omitempty"`
	MaxCost              int64              `json:",omitempty"`
	Version              int32
	Routing              *routing.ResolvedRoutingContext
	GroupKey             string
	Model                string
	BillingMode          string
	BillingPolicyVersion string
	Pricing              FrozenRequestPricing
	Subscription         *FrozenSubscription
}

type FrozenRequestPricing struct {
	Method                   string
	Price                    *ModelPrice
	GroupRatio               float64
	ModelRatio               float64
	CompletionRatio          float64
	CacheCreationMode        CacheCreationMode
	CanonicalUsageMode       CanonicalUsageMode
	CanonicalChargeAll       bool
	CanonicalChargeAllowlist map[string]struct{}
}

type FrozenSubscription struct {
	EntitlementRevision int64                             `json:",omitempty"`
	Coverage            []subscriptionbiz.RoutingCoverage `json:",omitempty"`
	SubscriptionID      int64
	PolicyID            int64
	PolicyVersion       string
	ContractVersion     string
	CoverageMode        string
	RateMultiplier      float64
	DailyLimit          *float64
	WeeklyLimit         *float64
	MonthlyLimit        *float64
	DailyWindowStart    int64
	WeeklyWindowStart   int64
	MonthlyWindowStart  int64
}

func RequestSnapshotsEnabled() bool {
	return strings.EqualFold(strings.TrimSpace(os.Getenv("BILLING_REQUEST_SNAPSHOT_V2")), "true")
}

func (uc *BillingUsecase) SetRoutingGroupReader(r RoutingGroupReader) { uc.routingGroups = r }

func (uc *BillingUsecase) RoutingSnapshotsAvailable() bool {
	return RequestSnapshotsEnabled() && uc.routingGroups != nil
}

func snapshotDigest(v any) (string, error) {
	b, err := jsonx.Marshal(v)
	if err != nil {
		return "", err
	}
	return fmt.Sprintf("%x", sha256.Sum256(b)), nil
}

func (s *RequestSnapshot) Digest() (string, error) { return snapshotDigest(s) }

func (uc *BillingUsecase) GetRequestSnapshot(ctx context.Context, reservationID string) (*RequestSnapshot, error) {
	if reservationID == "" {
		return nil, nil
	}
	r, err := uc.reservationRepo.GetReservation(ctx, reservationID)
	if errors.Is(err, ErrReservationNotFound) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	if r == nil {
		return nil, nil
	}
	return r.RequestSnapshot, nil
}

func (s *RequestSnapshot) Validate() error {
	if s == nil || s.Version != 2 || s.Model == "" || s.GroupKey == "" || !routing.ValidBillingMode(s.BillingMode) || s.BillingPolicyVersion == "" {
		return ErrRequestSnapshotInvalid
	}
	if s.Routing != nil && (s.Routing.Validate() != nil || s.Routing.GroupKey != s.GroupKey) {
		return ErrRequestSnapshotInvalid
	}
	if s.BillingMode == routing.SubscriptionOnly && (s.CostBound == nil || !s.CostBound.Valid() || s.MaxCost < 0) {
		return ErrRequestSnapshotInvalid
	}
	if s.Subscription != nil && s.Subscription.CoverageMode == "selected_groups" {
		if s.Routing == nil || s.Subscription.EntitlementRevision <= 0 {
			return ErrRequestSnapshotInvalid
		}
		covered := false
		for _, g := range s.Subscription.Coverage {
			if g.GroupID == s.Routing.GroupID {
				covered = true
			}
		}
		if !covered {
			return ErrRequestSnapshotInvalid
		}
	}
	p := s.Pricing
	if (p.Method != "model_price" && p.Method != "ratio") || (p.Method == "model_price") != (p.Price != nil) || !finitePositive(p.GroupRatio) || !finitePositive(p.ModelRatio) || !finitePositive(p.CompletionRatio) {
		return ErrRequestSnapshotInvalid
	}
	if s.Subscription != nil && (s.Subscription.SubscriptionID <= 0 || s.Subscription.PolicyID <= 0 || !finitePositive(s.Subscription.RateMultiplier) || s.Subscription.PolicyVersion == "" || s.Subscription.ContractVersion == "" || (s.Subscription.CoverageMode != "legacy_all_authorized" && s.Subscription.CoverageMode != "selected_groups")) {
		return ErrRequestSnapshotInvalid
	}
	if _, err := s.Digest(); err != nil {
		return ErrRequestSnapshotInvalid
	}
	return nil
}

func finitePositive(n float64) bool { return n > 0 && !math.IsInf(n, 0) && !math.IsNaN(n) }

func (uc *BillingUsecase) prepareRequestSnapshot(ctx context.Context, userID, legacyGroup, model string, routingContext *routing.ResolvedRoutingContext) (*RequestSnapshot, error) {
	if !RequestSnapshotsEnabled() {
		if routingContext != nil {
			return nil, ErrRequestSnapshotUnavailable
		}
		return nil, nil
	}
	group := legacyGroup
	if routingContext != nil {
		if routingContext.Validate() != nil || strconv.FormatInt(routingContext.UserID, 10) != userID {
			return nil, ErrRoutingContextInvalid
		}
		if uc.routingGroups == nil {
			return nil, ErrRequestSnapshotUnavailable
		}
		g, err := uc.routingGroups.GetRoutingGroup(ctx, routingContext.GroupID)
		if err != nil {
			return nil, ErrRequestSnapshotUnavailable
		}
		if g == nil || g.ID != routingContext.GroupID || g.Key != routingContext.GroupKey || g.Status != "enabled" || g.Revision != routingContext.GroupRevision {
			return nil, ErrRoutingContextInvalid
		}
		group = g.Key
	}
	// Do not turn a failed price read into an unreported fallback in v2. Reuse
	// the existing normalization/precedence rules with this one captured read.
	pricingUC := *uc
	if uc.pricingStore != nil {
		config, err := uc.pricingStore.GetPricingConfig(ctx)
		if err != nil {
			return nil, ErrRequestSnapshotUnavailable
		}
		pricingUC.pricingStore = capturedPricingStore{config}
	}
	config := pricingUC.pricingConfig(ctx)
	p := FrozenRequestPricing{Method: "ratio", GroupRatio: uc.getGroupRatio(config, group), ModelRatio: uc.getModelRatio(config, model), CompletionRatio: uc.getCompletionRatio(config, model), CacheCreationMode: uc.CacheCreationBillingMode(), CanonicalUsageMode: uc.CanonicalUsageMode(), CanonicalChargeAll: uc.canonicalCharge.all, CanonicalChargeAllowlist: uc.canonicalCharge.allowlist}
	if price, ok := config.ModelPrices[normalizePricingModelKey(model)]; ok {
		p.Method = "model_price"
		p.Price = &price
	}
	version, err := snapshotDigest(p)
	if err != nil {
		return nil, ErrRequestSnapshotInvalid
	}
	s := &RequestSnapshot{Version: 2, Routing: routingContext, GroupKey: group, Model: model, BillingMode: "subscription_first", BillingPolicyVersion: "legacy_projection:" + version, Pricing: p}
	if uc.routingPolicies != nil && routingContext != nil {
		policy, err := uc.routingPolicies.Get(ctx, routingContext.GroupID)
		if err != nil {
			return nil, err
		}
		if policy != nil {
			s.BillingMode = policy.BillingMode
			s.Pricing.GroupRatio = policy.PriceRatio
			s.BillingPolicyVersion = fmt.Sprintf("routing_policy:%d:%d", policy.GroupID, policy.Version)
		}
	}
	if s.BillingMode == routing.SubscriptionOnly {
		bound := routing.GetCostBound(ctx)
		if !bound.Valid() {
			return nil, ErrSubscriptionBoundRequired
		}
		s.CostBound = &bound
	}
	// Own the input values, including optional bucket prices and allowlist.
	b, err := jsonx.Marshal(s)
	if err != nil {
		return nil, ErrRequestSnapshotInvalid
	}
	var owned RequestSnapshot
	if jsonx.Unmarshal(b, &owned) != nil {
		return nil, ErrRequestSnapshotInvalid
	}
	return &owned, owned.Validate()
}

type capturedPricingStore struct{ config PricingConfig }

func (s capturedPricingStore) GetPricingConfig(context.Context) (PricingConfig, error) {
	return s.config, nil
}

// requestCalculator has no live config store. Only the immutable user pricing
// inputs are supplied; upstream vendor cost continues to have its own audit.
func (uc *BillingUsecase) requestCalculator(s *RequestSnapshot) *BillingUsecase {
	p := s.Pricing
	c := &BillingUsecase{groupRatios: map[string]float64{s.GroupKey: p.GroupRatio}, modelRatios: map[string]float64{normalizePricingModelKey(s.Model): p.ModelRatio}, completionRatios: map[string]float64{normalizePricingModelKey(s.Model): p.CompletionRatio}, cacheCreationMode: p.CacheCreationMode, canonicalUsageMode: p.CanonicalUsageMode, canonicalCharge: canonicalUsageChargeGate{all: p.CanonicalChargeAll, allowlist: p.CanonicalChargeAllowlist}, pricingSnapshotRepo: uc.pricingSnapshotRepo}
	if p.Price != nil {
		c.modelPrices = map[string]ModelPrice{normalizePricingModelKey(s.Model): *p.Price}
	}
	return c
}

func (uc *BillingUsecase) reservationCost(ctx context.Context, r *Reservation, legacyGroup string, tokens int64, usage LedgerUsage) (int64, canonicalCostBreakdown, ledgerUsageAudit, error) {
	calculator, group := uc, legacyGroup
	if r.RequestSnapshot != nil {
		if err := r.RequestSnapshot.Validate(); err != nil {
			return 0, canonicalCostBreakdown{}, ledgerUsageAudit{}, err
		}
		calculator, group = uc.requestCalculator(r.RequestSnapshot), r.RequestSnapshot.GroupKey
	}
	cost, breakdown, audit := calculator.calculateCostWithUsage(ctx, group, r.Model, tokens, usage)
	return cost, breakdown, audit, nil
}

func validateReservationReplay(r *Reservation, model string, c *routing.ResolvedRoutingContext) error {
	if r.Model != model {
		return ErrRoutingContextConflict
	}
	var previous *routing.ResolvedRoutingContext
	if r.RequestSnapshot != nil {
		previous = r.RequestSnapshot.Routing
	}
	if (previous == nil) != (c == nil) || (previous != nil && previous.Digest() != c.Digest()) {
		return ErrRoutingContextConflict
	}
	return nil
}

func freezeSubscription(group *subscriptionbiz.SubscriptionGroup, sub *subscriptionbiz.UserSubscription, multiplier float64) (*FrozenSubscription, error) {
	policyVersion, err := snapshotDigest(group)
	if err != nil {
		return nil, ErrRequestSnapshotInvalid
	}
	contractVersion, err := snapshotDigest(struct{ ID, GroupID, StartsAt, ExpiresAt int64 }{sub.ID, sub.GroupID, sub.StartsAt, sub.ExpiresAt})
	if err != nil {
		return nil, ErrRequestSnapshotInvalid
	}
	copyLimit := func(v *float64) *float64 {
		if v == nil {
			return nil
		}
		n := *v
		return &n
	}
	f := &FrozenSubscription{SubscriptionID: sub.ID, PolicyID: group.ID, PolicyVersion: policyVersion, ContractVersion: contractVersion, CoverageMode: "legacy_all_authorized", RateMultiplier: multiplier, DailyLimit: copyLimit(group.DailyLimitUSD), WeeklyLimit: copyLimit(group.WeeklyLimitUSD), MonthlyLimit: copyLimit(group.MonthlyLimitUSD), DailyWindowStart: sub.DailyWindowStart, WeeklyWindowStart: sub.WeeklyWindowStart, MonthlyWindowStart: sub.MonthlyWindowStart}
	if sub.Contract != nil {
		if sub.Contract.Validate() != nil {
			return nil, ErrRequestSnapshotInvalid
		}
		f.CoverageMode = sub.Contract.CoverageMode
		f.Coverage = append([]subscriptionbiz.RoutingCoverage(nil), sub.Contract.Coverage...)
		f.ContractVersion = sub.Contract.Digest
		f.PolicyVersion = sub.Contract.QuotaPolicy.Version
		f.EntitlementRevision = sub.EntitlementRevision
	}
	return f, nil
}

type RequestSnapshotBatchReader interface {
	RequestSnapshots(context.Context, []string) (map[string]*RequestSnapshot, error)
}

func (uc *BillingUsecase) LedgerRequestSnapshots(ctx context.Context, ledgers []*Ledger) (map[string]*RequestSnapshot, error) {
	ids := make([]string, 0, len(ledgers))
	for _, l := range ledgers {
		if l.ReferenceID != "" {
			ids = append(ids, l.ReferenceID)
		}
	}
	if len(ids) == 0 {
		return map[string]*RequestSnapshot{}, nil
	}
	if r, ok := uc.reservationRepo.(RequestSnapshotBatchReader); ok {
		return r.RequestSnapshots(ctx, ids)
	}
	// Compatibility for older in-memory adapters; production always batches.
	out := map[string]*RequestSnapshot{}
	if uc.reservationRepo == nil {
		return out, nil
	}
	for _, id := range ids {
		s, err := uc.GetRequestSnapshot(ctx, id)
		if err != nil {
			return nil, err
		}
		if s != nil {
			out[id] = s
		}
	}
	return out, nil
}

package biz

import (
	"context"
	"fmt"
	"math"
	"strconv"
	"time"

	"github.com/go-kratos/kratos/v3/errors"
	billingv1 "micro-one-api/api/billing/v1"
	billingdomain "micro-one-api/domain/billing"
	"micro-one-api/domain/requesttrace"
)

var (
	ErrServiceTierUnsupported   = errors.BadRequest(billingv1.ServiceTierErrorReason_SERVICE_TIER_UNSUPPORTED.String(), "service tier is not supported by this source or entry point")
	ErrServiceTierBoundRequired = errors.BadRequest(billingv1.ServiceTierErrorReason_SERVICE_TIER_BOUND_REQUIRED.String(), "service tier requires bounded text input and an explicit output token limit")
	ErrServiceTierBoundExceeded = errors.BadRequest(billingv1.ServiceTierErrorReason_SERVICE_TIER_BOUND_EXCEEDED.String(), "service tier usage exceeds the frozen token bound")
)

type serviceTierSource struct {
	Kind, Model string
	ID          int64
}
type serviceTierSourceKey struct{}

func withServiceTierSource(ctx context.Context, channelID string, accountID int64, model string) context.Context {
	t := requesttrace.FromContext(ctx)
	source := serviceTierSource{Kind: t.SourceKind, Model: t.UpstreamModelID}
	if source.Kind == "" {
		source.Kind = "channel"
	}
	if source.Model == "" {
		source.Model = model
	}
	source.ID, _ = strconv.ParseInt(channelID, 10, 64)
	if source.Kind == "subscription" {
		source.ID = accountID
	}
	return context.WithValue(ctx, serviceTierSourceKey{}, source)
}

type FrozenServiceTier struct {
	PricingTimeBand string                           `json:",omitempty"`
	PricingTimeUnix int64                            `json:",omitempty"`
	Config          billingdomain.ServiceTierConfig  `json:"config"`
	Request         billingdomain.ServiceTierRequest `json:"request"`
	ConfigHash      string                           `json:"config_hash"`
	Policy          string                           `json:"policy"`
	MaxCost         int64                            `json:"max_cost"`
}

func (s *FrozenServiceTier) Validate() error {
	if s == nil || !s.Config.Enabled || s.Config.Validate() != nil || s.ConfigHash != s.Config.Digest() || s.Policy != billingdomain.ServiceTierPolicy || s.MaxCost < 1 || !validTierBound(s.Request) {
		return ErrRequestSnapshotInvalid
	}
	if s.Config.TimePricing != nil {
		band, err := s.Config.TimePricing.BandAt(time.Unix(s.PricingTimeUnix, 0))
		if err != nil || s.PricingTimeUnix <= 0 || band != s.PricingTimeBand {
			return ErrRequestSnapshotInvalid
		}
	} else if s.PricingTimeBand != "" || s.PricingTimeUnix != 0 {
		return ErrRequestSnapshotInvalid
	}
	return nil
}
func validTierBound(r billingdomain.ServiceTierRequest) bool {
	return r.Version == 1 && billingdomain.ValidRequestedServiceTier(r.RequestedServiceTier) && (r.Protocol == "openai_chat" || r.Protocol == "openai_responses") && r.InputTokenBound > 0 && r.OutputTokenBound > 0 && r.InputTokenBound <= math.MaxInt32 && r.OutputTokenBound <= math.MaxInt32
}
func tierScope(ctx context.Context, c billingdomain.ServiceTierConfig) bool {
	src, _ := ctx.Value(serviceTierSourceKey{}).(serviceTierSource)
	return c.Enabled && src.Kind == c.SourceKind && src.ID == c.SourceID && src.Model == c.Model
}
func (uc *BillingUsecase) freezeServiceTier(ctx context.Context, c billingdomain.ServiceTierConfig, multiplier float64) (*FrozenServiceTier, error) {
	r := billingdomain.GetServiceTierRequest(ctx)
	if !billingdomain.ValidRequestedServiceTier(r.RequestedServiceTier) {
		return nil, ErrServiceTierUnsupported
	}
	if c.Validate() != nil {
		return nil, ErrRequestSnapshotInvalid
	}
	if !tierScope(ctx, c) {
		if r.RequestedServiceTier == "priority" || r.RequestedServiceTier == "flex" {
			return nil, ErrServiceTierUnsupported
		}
		return nil, nil
	}
	if !RequestSnapshotsEnabled() {
		return nil, ErrRequestSnapshotUnavailable
	}
	if ExecutionIntent(ctx) != nil {
		return nil, ErrServiceTierUnsupported
	}
	if !validTierBound(r) {
		return nil, ErrServiceTierBoundRequired
	}
	frozen := &FrozenServiceTier{Config: c, Request: r, ConfigHash: c.Digest(), Policy: billingdomain.ServiceTierPolicy}
	if c.TimePricing != nil {
		now := uc.Now()
		band, err := c.TimePricing.BandAt(now)
		if err != nil {
			return nil, ErrRequestSnapshotUnavailable
		}
		frozen.PricingTimeBand, frozen.PricingTimeUnix = band, now.Unix()
	}
	maximum := c.ForTimeBand(frozen.PricingTimeBand).MaximumPrice()
	// An input bound covers any possible input bucket, not only uncached input.
	maxInput := maximum.InputPrice
	for _, v := range []*float64{maximum.CacheReadPrice, maximum.CacheCreation5mPrice, maximum.CacheCreation1hPrice} {
		if v != nil {
			maxInput = math.Max(maxInput, *v)
		}
	}
	cost := saturatingCostSum(roundScaled(r.InputTokenBound, maxInput, multiplier), roundScaled(r.OutputTokenBound, maximum.OutputPrice, multiplier))
	// Bucket rounding can add at most four units beyond rounding the aggregate.
	if cost < math.MaxInt64-4 {
		cost += 4
	}
	if cost < 1 {
		cost = 1
	}
	frozen.MaxCost = cost
	return frozen, nil
}

func tierSelection(f *FrozenServiceTier, usage LedgerUsage) (ModelPrice, string, string) {
	prices, _ := f.Config.ForTimeBand(f.PricingTimeBand).PricesForInput(contextInputTokens(usage))
	actual, status := usage.ActualServiceTier, usage.ServiceTierStatus
	if status == "reported" && billingdomain.KnownActualServiceTier(actual) {
		return prices[actual], actual, status
	}
	if status != "conflict" && status != "unknown" {
		if actual == "" {
			status = "missing"
		} else {
			status = "unknown"
		}
	}
	return billingdomain.MaxTierPrice(prices), "max", status
}

// The canonical buckets are mutually exclusive: cached input contributes to
// the context window exactly once. Output never contributes to the threshold.
// Callers must first validate the canonical usage contract and frozen bounds.
func contextInputTokens(usage LedgerUsage) int64 {
	if usage.Envelope == nil || usage.Envelope.Canonical == nil {
		return 0
	}
	b := usage.Envelope.Canonical
	return saturatingCostSum(b.UncachedInputTokens, b.CacheReadTokens, b.CacheCreation5mTokens, b.CacheCreation1hTokens)
}
func tierUsageBounds(f *FrozenServiceTier, usage LedgerUsage, tokens int64) error {
	if usage.UsageContractVersion != 1 || usage.Envelope == nil || usage.Envelope.ParseStatus != "verified" || usage.Envelope.Canonical == nil || validateV1Envelope(usage.Envelope) != "" {
		return ErrServiceTierUnsupported
	}
	b := legacyCanonicalBuckets(usage, tokens)
	if usage.Envelope != nil && usage.Envelope.Canonical != nil {
		b = *usage.Envelope.Canonical
	}
	if b.UncachedInputTokens < 0 || b.CacheReadTokens < 0 || b.CacheCreation5mTokens < 0 || b.CacheCreation1hTokens < 0 || b.OutputTokens < 0 {
		return ErrServiceTierBoundExceeded
	}
	input := saturatingCostSum(b.UncachedInputTokens, b.CacheReadTokens, b.CacheCreation5mTokens, b.CacheCreation1hTokens)
	if input > f.Request.InputTokenBound || b.OutputTokens > f.Request.OutputTokenBound {
		return ErrServiceTierBoundExceeded
	}
	return nil
}
func applyTierAudit(a *ledgerUsageAudit, f *FrozenServiceTier, usage LedgerUsage, tier, status string) {
	a.RequestedServiceTier = f.Request.RequestedServiceTier
	a.ActualServiceTier = usage.ActualServiceTier
	a.ServiceTierStatus = status
	a.BilledServiceTier = tier
	a.ServiceTierPolicy = f.Policy
	a.ServiceTierConfigHash = f.ConfigHash
	if c := f.Config.TimePricing; c != nil {
		a.PricingTimeBand, a.PricingTimeUnix, a.PricingTimezone = f.PricingTimeBand, f.PricingTimeUnix, c.Timezone
	}
	if c := f.Config.LongContext; c != nil {
		a.LongContextPolicy = c.Policy
		a.LongContextThreshold = c.ThresholdTokens
		a.LongContextInputTokens = contextInputTokens(usage)
		_, a.LongContextTier = f.Config.PricesForInput(a.LongContextInputTokens)
	}
	if a.pricingSnapshot != nil {
		s := a.pricingSnapshot
		s.ServiceTier = tier
		s.ServiceTierPolicy = f.Policy
		s.ServiceTierConfigHash = f.ConfigHash
		s.SnapshotVersion = 2
		s.ConfigHash = fmt.Sprintf("%x", snapshotTierHash(s))
		if a.LongContextPolicy != "" {
			s.LongContextPolicy = a.LongContextPolicy
			s.LongContextThreshold = a.LongContextThreshold
			s.LongContextTier = a.LongContextTier
			s.SnapshotVersion = 3
			s.ConfigHash = fmt.Sprintf("%x", snapshotLongContextHash(s))
		}
		if a.PricingTimeBand != "" {
			s.PricingTimeBand, s.PricingTimezone = a.PricingTimeBand, a.PricingTimezone
			s.SnapshotVersion = 4
			s.ConfigHash = fmt.Sprintf("%x", snapshotTimePricingHash(s))
		}
		a.PricingConfigHash = s.ConfigHash
	}
}

// A partial procurement tariff cannot prove a complete estimate. Presence and
// amount are separate: an explicit zero price is known, a missing positive
// bucket price makes the entire supplier estimate unknown.
func frozenTierSupplierPrice(usage LedgerUsage) (ModelPrice, bool) {
	tier := usage.FrozenServiceTier
	if tier == nil || usage.ServiceTierStatus != "reported" || !billingdomain.KnownActualServiceTier(usage.ActualServiceTier) || usage.Envelope == nil || usage.Envelope.ParseStatus != "verified" || usage.Envelope.Canonical == nil || validateV1Envelope(usage.Envelope) != "" {
		return ModelPrice{}, false
	}
	selected := tier.ForTimeBand(usage.FrozenPricingTimeBand)
	tier = &selected
	prices := tier.UpstreamPrices
	if c := tier.LongContext; c != nil && contextInputTokens(usage) > c.ThresholdTokens {
		// A long-context procurement tariff is independent evidence. Missing
		// long prices must never inherit the cheaper base estimate.
		prices = c.UpstreamPrices
	}
	price, ok := prices[usage.ActualServiceTier]
	if !ok {
		return ModelPrice{}, false
	}
	buckets := usage.Envelope.Canonical
	if buckets.CacheReadTokens > 0 && price.CacheReadPrice == nil || buckets.CacheCreation5mTokens > 0 && price.CacheCreation5mPrice == nil || buckets.CacheCreation1hTokens > 0 && price.CacheCreation1hPrice == nil {
		return ModelPrice{}, false
	}
	return price, true
}

package biz

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"
	billingdomain "micro-one-api/domain/billing"
	"micro-one-api/domain/requesttrace"
)

func tierTestConfig() billingdomain.ServiceTierConfig {
	return billingdomain.ServiceTierConfig{Version: 1, Enabled: true, SourceKind: "channel", SourceID: 11, Model: "gpt-5.1", Prices: map[string]ModelPrice{
		"default":  {InputPrice: .001, OutputPrice: .003, CacheReadPrice: fp(.0002), CacheCreation5mPrice: fp(.002), CacheCreation1hPrice: fp(.004)},
		"priority": {InputPrice: .002, OutputPrice: .005, CacheReadPrice: fp(.0004), CacheCreation5mPrice: fp(.003), CacheCreation1hPrice: fp(.006)},
		"flex":     {InputPrice: .0005, OutputPrice: .001, CacheReadPrice: fp(.0001), CacheCreation5mPrice: fp(.001), CacheCreation1hPrice: fp(.002)},
	}}
}
func tierTestContext(tier string) context.Context {
	ctx := requesttrace.WithAttempt(context.Background(), requesttrace.Attempt{RootRequestID: "tier-root", Number: 1, SourceKind: "channel", UpstreamModelID: "gpt-5.1"})
	return billingdomain.WithServiceTierRequest(ctx, billingdomain.ServiceTierRequest{RequestedServiceTier: tier, Protocol: "openai_chat", InputTokenBound: 1000, OutputTokenBound: 100, Version: 1})
}
func tierTestUsecase(t *testing.T) (*BillingUsecase, *mockLedgerRepo) {
	t.Helper()
	t.Setenv("BILLING_REQUEST_SNAPSHOT_V2", "true")
	ledger := &mockLedgerRepo{}
	uc := NewBillingUsecaseWithPricing(&mockAccountRepo{account: &Account{UserID: "42", Group: "default", Balance: 1_000_000}}, &mockReservationRepo{reservations: map[string]*Reservation{}}, ledger, nil, PricingConfig{ServiceTier: tierTestConfig()})
	uc.SetPricingSnapshotRepo(&recordingPricingSnapshotRepo{})
	return uc, ledger
}
func tierTestUsage(actual, status string) LedgerUsage {
	return LedgerUsage{ActualServiceTier: actual, ServiceTierStatus: status, UsageContractVersion: 1, Envelope: &UsageEnvelopeData{ParseStatus: "verified", Semantics: UsageSemanticsOpenAISubset, Canonical: &CanonicalBuckets{UncachedInputTokens: 100, CacheReadTokens: 20, CacheCreation5mTokens: 30, CacheCreation1hTokens: 40, OutputTokens: 50}}}
}

func TestServiceTierFreezesFiveBucketsAndSeparatesRequestActual(t *testing.T) {
	for _, tc := range []struct {
		actual, status, billed string
		want                   int64
	}{{"flex", "reported", "flex", 2120}, {"default", "reported", "default", 4740}, {"priority", "reported", "priority", 7880}, {"", "missing", "max", 7880}, {"future", "unknown", "max", 7880}, {"", "unknown", "max", 7880}, {"priority", "conflict", "max", 7880}} {
		t.Run(tc.actual+tc.status, func(t *testing.T) {
			uc, ledger := tierTestUsecase(t)
			uc.SetTxRunner(&mockTxRunner{})
			ctx := tierTestContext("priority")
			r, err := uc.ReserveQuota(ctx, "42", "tier-charge", 1, "gpt-5.1", "11", 0)
			require.NoError(t, err)
			require.NotNil(t, r.RequestSnapshot.ServiceTier)
			frozenHash, _ := r.RequestSnapshot.Digest()
			// Mutate all live prices, disable the feature, and change canonical modes.
			uc.serviceTier.Prices["flex"] = ModelPrice{InputPrice: 9, OutputPrice: 9, CacheReadPrice: fp(9)}
			uc.serviceTier.Enabled = false
			uc.canonicalUsageMode = CanonicalUsageModeLegacy
			uc.cacheCreationMode = CacheCreationModeObserve
			cost, _, err := uc.CommitQuotaWithUsage(ctx, r.ReservationID, 240, true, tierTestUsage(tc.actual, tc.status))
			require.NoError(t, err)
			require.EqualValues(t, tc.want, cost)
			require.LessOrEqual(t, cost, r.Amount)
			row := ledger.ledgers[len(ledger.ledgers)-1]
			require.Equal(t, "priority", row.RequestedServiceTier)
			require.Equal(t, tc.actual, row.ActualServiceTier)
			require.Equal(t, tc.status, row.ServiceTierStatus)
			require.Equal(t, tc.billed, row.BilledServiceTier)
			require.NotEmpty(t, row.PricingConfigHash)
			after, _ := r.RequestSnapshot.Digest()
			require.Equal(t, frozenHash, after)
			before := len(ledger.ledgers)
			again, _, err := uc.CommitQuotaWithUsage(ctx, r.ReservationID, 999, true, tierTestUsage("priority", "reported"))
			require.NoError(t, err)
			require.Equal(t, cost, again)
			require.Len(t, ledger.ledgers, before)
		})
	}
}

func TestServiceTierAdmissionRejectsUnsupportedSourcesAndUnboundedEntryPoints(t *testing.T) {
	uc, _ := tierTestUsecase(t)
	_, err := uc.ReserveQuota(tierTestContext("priority"), "42", "wrong-source", 1, "gpt-5.1", "12", 0)
	require.ErrorIs(t, err, ErrServiceTierUnsupported)
	for _, tier := range []string{"", "auto", "default"} {
		_, err = uc.ReserveQuota(tierTestContext(tier), "42", "legacy-"+tier, 1, "other", "12", 0)
		require.NoError(t, err)
	}
	for _, r := range []billingdomain.ServiceTierRequest{{Version: 1, RequestedServiceTier: "priority", Protocol: "unsupported", InputTokenBound: 100, OutputTokenBound: 50}, {Version: 1, Protocol: "openai_chat", InputTokenBound: 100}, {Protocol: "openai_chat", InputTokenBound: 100, OutputTokenBound: 50}} {
		ctx := billingdomain.WithServiceTierRequest(tierTestContext(""), r)
		_, err = uc.ReserveQuota(ctx, "42", "unbounded", 1, "gpt-5.1", "11", 0)
		require.ErrorIs(t, err, ErrServiceTierBoundRequired)
	}
	t.Setenv("BILLING_REQUEST_SNAPSHOT_V2", "false")
	_, err = uc.ReserveQuota(tierTestContext(""), "42", "scope-without-snapshot", 1, "gpt-5.1", "11", 0)
	require.ErrorIs(t, err, ErrRequestSnapshotUnavailable)
}
func TestServiceTierReplayAndUsageCannotCrossFrozenBounds(t *testing.T) {
	uc, _ := tierTestUsecase(t)
	ctx := tierTestContext("priority")
	r, err := uc.ReserveQuota(ctx, "42", "bounded", 1, "gpt-5.1", "11", 0)
	require.NoError(t, err)
	_, err = uc.ReserveQuota(tierTestContext("flex"), "42", "bounded", 1, "gpt-5.1", "11", 0)
	require.ErrorIs(t, err, ErrRoutingContextConflict)
	_, err = uc.ReserveQuota(ctx, "42", "bounded", 1, "gpt-5.1", "12", 0)
	require.ErrorIs(t, err, ErrRoutingContextConflict)
	u := tierTestUsage("default", "reported")
	u.Envelope.Canonical.OutputTokens = 101
	_, _, err = uc.CommitQuotaWithUsage(ctx, r.ReservationID, 100, true, u)
	require.ErrorIs(t, err, ErrServiceTierBoundExceeded)
	require.True(t, r.IsReserved())
	u = tierTestUsage("default", "reported")
	u.Envelope.ParseStatus = "estimated"
	_, _, err = uc.CommitQuotaWithUsage(ctx, r.ReservationID, 100, true, u)
	require.ErrorIs(t, err, ErrServiceTierUnsupported)
}
func TestServiceTierProcurementUsesFrozenTariffAndNeverClientOverride(t *testing.T) {
	uc, _ := tierTestUsecase(t)
	cfg := tierTestConfig()
	cfg.UpstreamPrices = cfg.Prices
	uc.serviceTier = cfg
	r, err := uc.ReserveQuota(tierTestContext("flex"), "42", "supplier", 1, "gpt-5.1", "11", 0)
	require.NoError(t, err)
	u := tierTestUsage("flex", "reported")
	u.UpstreamCost = 999999
	u = usageForReservation(u, r)
	require.EqualValues(t, 2120, uc.calculateUpstreamCostWithUsage(context.Background(), 11, "gpt-5.1", 240, u))
	u.ActualServiceTier = ""
	u.ServiceTierStatus = "missing"
	require.Zero(t, uc.calculateUpstreamCostWithUsage(context.Background(), 11, "gpt-5.1", 240, u))
	require.Equal(t, CostAuditUnpriced, upstreamCostAuditStatus(0, u))
}

// A3 preserves the existing successful-request minimum: zero tariffs or a
// verified zero-token response still settle one fixed-point unit (USD .0001).
func TestServiceTierPreservesMinimumForZeroPricesAndZeroUsage(t *testing.T) {
	for _, zeroPrices := range []bool{true, false} {
		t.Run(map[bool]string{true: "zero_prices", false: "zero_usage"}[zeroPrices], func(t *testing.T) {
			uc, _ := tierTestUsecase(t)
			uc.SetTxRunner(&mockTxRunner{})
			if zeroPrices {
				for _, tier := range []string{"default", "priority", "flex"} {
					uc.serviceTier.Prices[tier] = ModelPrice{CacheReadPrice: fp(0)}
				}
			}
			ctx := tierTestContext("default")
			r, err := uc.ReserveQuota(ctx, "42", "zero", 1, "gpt-5.1", "11", 0)
			require.NoError(t, err)
			usage := tierTestUsage("default", "reported")
			if !zeroPrices {
				usage.Envelope.Canonical = &CanonicalBuckets{}
			}
			cost, _, err := uc.CommitQuotaWithUsage(ctx, r.ReservationID, 0, true, usage)
			require.NoError(t, err)
			require.EqualValues(t, 1, cost)
			replay, _, err := uc.CommitQuotaWithUsage(ctx, r.ReservationID, 0, true, usage)
			require.NoError(t, err)
			require.EqualValues(t, 1, replay)
		})
	}
}

func TestServiceTierProcurementNeedsPricesForEveryPositiveBucket(t *testing.T) {
	for _, bucket := range []string{"creation5m", "creation1h"} {
		t.Run(bucket, func(t *testing.T) {
			uc, _ := tierTestUsecase(t)
			cfg := tierTestConfig()
			cfg.UpstreamPrices = make(map[string]ModelPrice, len(cfg.Prices))
			for key, value := range cfg.Prices {
				cfg.UpstreamPrices[key] = value
			}
			price := cfg.UpstreamPrices["flex"]
			if bucket == "creation5m" {
				price.CacheCreation5mPrice = nil
			} else {
				price.CacheCreation1hPrice = nil
			}
			cfg.UpstreamPrices["flex"] = price
			uc.serviceTier = cfg
			r, err := uc.ReserveQuota(tierTestContext("flex"), "42", "partial", 1, "gpt-5.1", "11", 0)
			require.NoError(t, err)
			usage := usageForReservation(tierTestUsage("flex", "reported"), r)
			cost := uc.calculateUpstreamCostWithUsage(context.Background(), 11, "gpt-5.1", 240, usage)
			require.Zero(t, cost)
			require.Equal(t, CostAuditUnpriced, upstreamCostAuditStatus(cost, usage))
			// The missing price is harmless when its bucket is absent.
			if bucket == "creation5m" {
				usage.Envelope.Canonical.CacheCreation5mTokens = 0
			} else {
				usage.Envelope.Canonical.CacheCreation1hTokens = 0
			}
			cost = uc.calculateUpstreamCostWithUsage(context.Background(), 11, "gpt-5.1", 240, usage)
			require.Positive(t, cost)
			require.Equal(t, CostAuditPriced, upstreamCostAuditStatus(cost, usage))
			// An explicitly configured zero bucket price remains a known estimate.
			if bucket == "creation5m" {
				price.CacheCreation5mPrice = fp(0)
			} else {
				price.CacheCreation1hPrice = fp(0)
			}
			usage.FrozenServiceTier.UpstreamPrices["flex"] = price
			usage.Envelope.Canonical = tierTestUsage("flex", "reported").Envelope.Canonical
			cost = uc.calculateUpstreamCostWithUsage(context.Background(), 11, "gpt-5.1", 240, usage)
			require.Positive(t, cost)
			require.Equal(t, CostAuditPriced, upstreamCostAuditStatus(cost, usage))
		})
	}
}

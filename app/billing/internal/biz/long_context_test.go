package biz

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	billingdomain "micro-one-api/domain/billing"
	"micro-one-api/domain/requesttrace"
)

func contextTestConfig() billingdomain.ServiceTierConfig {
	c := tierTestConfig()
	c.Version, c.Model = 2, "deepseek-flash"
	c.LongContext = &billingdomain.LongContextConfig{ThresholdTokens: 100, Policy: billingdomain.LongContextPolicy, Prices: doubledContextPrices(c.Prices)}
	return c
}

func doubledContextPrices(prices map[string]ModelPrice) map[string]ModelPrice {
	result := map[string]ModelPrice{}
	for key, p := range prices {
		result[key] = ModelPrice{InputPrice: p.InputPrice * 2, OutputPrice: p.OutputPrice * 2, CacheReadPrice: fp(*p.CacheReadPrice * 2), CacheCreation5mPrice: fp(*p.CacheCreation5mPrice * 2), CacheCreation1hPrice: fp(*p.CacheCreation1hPrice * 2)}
	}
	return result
}

func contextTestRequest() context.Context {
	ctx := requesttrace.WithAttempt(context.Background(), requesttrace.Attempt{RootRequestID: "context-root", Number: 1, SourceKind: "channel", UpstreamModelID: "deepseek-flash"})
	return billingdomain.WithServiceTierRequest(ctx, billingdomain.ServiceTierRequest{Protocol: "openai_chat", InputTokenBound: 1000, OutputTokenBound: 1000, Version: 1})
}

func contextTestUsage(input int64) LedgerUsage {
	usage := tierTestUsage("default", "reported")
	usage.Envelope.Canonical = &CanonicalBuckets{UncachedInputTokens: 25, CacheReadTokens: 25, CacheCreation5mTokens: 25, CacheCreation1hTokens: input - 75, OutputTokens: 10}
	// Contradictory legacy fields must never decide the context tier.
	usage.PromptTokens, usage.CompletionTokens = 999999, 999999
	return usage
}

func TestLongContextWholeRequestThresholdAndFrozenReplay(t *testing.T) {
	for _, tc := range []struct {
		input, want int64
		tier        string
	}{{99, 2060, "base"}, {100, 2100, "base"}, {101, 4280, "long"}} {
		t.Run(fmt.Sprint(tc.input), func(t *testing.T) {
			uc, ledger := tierTestUsecase(t)
			uc.serviceTier = contextTestConfig()
			uc.SetTxRunner(&mockTxRunner{})
			snapshots := &recordingPricingSnapshotRepo{}
			uc.SetPricingSnapshotRepo(snapshots)
			ctx := contextTestRequest()
			reservation, err := uc.ReserveQuota(ctx, "42", "context", 1, "deepseek-flash", "11", 0)
			require.NoError(t, err)
			digest, err := reservation.RequestSnapshot.Digest()
			require.NoError(t, err)
			uc.serviceTier.LongContext.ThresholdTokens = 1
			uc.serviceTier.LongContext.Prices["default"] = ModelPrice{InputPrice: 99}
			uc.serviceTier.Enabled = false
			cost, _, err := uc.CommitQuotaWithUsage(ctx, reservation.ReservationID, 1, true, contextTestUsage(tc.input))
			require.NoError(t, err)
			require.Equal(t, tc.want, cost)
			require.LessOrEqual(t, cost, reservation.Amount)
			row := ledger.ledgers[len(ledger.ledgers)-1]
			require.Equal(t, tc.tier, row.LongContextTier)
			require.EqualValues(t, 100, row.LongContextThreshold)
			require.Equal(t, tc.input, row.LongContextInputTokens)
			require.Equal(t, billingdomain.LongContextPolicy, row.LongContextPolicy)
			require.NotEmpty(t, snapshots.claims)
			snapshot := snapshots.claims[0]
			require.EqualValues(t, 3, snapshot.SnapshotVersion)
			require.Equal(t, tc.tier, snapshot.LongContextTier)
			require.Equal(t, row.PricingConfigHash, snapshot.ConfigHash)
			after, _ := reservation.RequestSnapshot.Digest()
			require.Equal(t, digest, after)
			replay, _, err := uc.CommitQuotaWithUsage(ctx, reservation.ReservationID, 999999, true, contextTestUsage(500))
			require.NoError(t, err)
			require.Equal(t, cost, replay)
		})
	}
}

func TestLongContextOutputDoesNotTriggerAndUnknownTierUsesContextMaximum(t *testing.T) {
	uc, _ := tierTestUsecase(t)
	uc.serviceTier = contextTestConfig()
	ctx := contextTestRequest()
	r, err := uc.ReserveQuota(ctx, "42", "context", 1, "deepseek-flash", "11", 0)
	require.NoError(t, err)
	u := contextTestUsage(100)
	u.Envelope.Canonical.OutputTokens = 999
	_, _, audit, err := uc.reservationCost(ctx, r, "default", 1, u)
	require.NoError(t, err)
	require.Equal(t, "base", audit.LongContextTier)
	u = contextTestUsage(101)
	u.ActualServiceTier, u.ServiceTierStatus = "", "missing"
	cost, _, audit, err := uc.reservationCost(ctx, r, "default", 1, u)
	require.NoError(t, err)
	require.EqualValues(t, 6820, cost)
	require.Equal(t, "max", audit.BilledServiceTier)
	require.Equal(t, "long", audit.LongContextTier)
}

func TestLongContextProcurementNeverFallsBackToBase(t *testing.T) {
	uc, _ := tierTestUsecase(t)
	cfg := contextTestConfig()
	cfg.UpstreamPrices = cfg.Prices
	uc.serviceTier = cfg
	ctx := contextTestRequest()
	r, err := uc.ReserveQuota(ctx, "42", "supplier-context", 1, "deepseek-flash", "11", 0)
	require.NoError(t, err)
	u := usageForReservation(contextTestUsage(101), r)
	require.Zero(t, uc.calculateUpstreamCostWithUsage(ctx, 11, cfg.Model, 1, u))
	require.Equal(t, CostAuditUnpriced, upstreamCostAuditStatus(0, u))
	u.FrozenServiceTier.LongContext.UpstreamPrices = doubledContextPrices(cfg.Prices)
	require.EqualValues(t, 4280, uc.calculateUpstreamCostWithUsage(ctx, 11, cfg.Model, 1, u))
}

func timeTestConfig() billingdomain.ServiceTierConfig {
	c := contextTestConfig()
	c.Version = 3
	c.TimePricing = &billingdomain.TimePricingConfig{Timezone: "Asia/Shanghai", CalendarValidThrough: "2026-12-31", Holidays: []string{"2026-10-30"}, PeakWindows: []billingdomain.PricingTimeWindow{{Weekdays: []int{1, 2, 3, 4, 5}, StartMinute: 540, EndMinute: 720}}, PeakPrices: doubledContextPrices(c.Prices), PeakLongContextPrices: doubledContextPrices(c.LongContext.Prices)}
	return c
}

func TestTimePricingFreezesAdmissionBandAcrossBoundaryAndRestart(t *testing.T) {
	uc, ledger := tierTestUsecase(t)
	cfg := timeTestConfig()
	uc.serviceTier = cfg
	uc.SetTxRunner(&mockTxRunner{})
	admitted := time.Date(2026, 10, 29, 3, 59, 59, 0, time.UTC) // 11:59:59 Shanghai, peak.
	uc.options.Now = func() time.Time { return admitted }
	ctx := contextTestRequest()
	r, err := uc.ReserveQuota(ctx, "42", "time-context", 1, cfg.Model, "11", 0)
	require.NoError(t, err)
	require.Equal(t, "peak", r.RequestSnapshot.ServiceTier.PricingTimeBand)
	// Persisted JSON owns its tariff and clock. Live config/clock no longer decide settlement.
	uc.options.Now = func() time.Time { return admitted.Add(24 * time.Hour) }
	uc.serviceTier = billingdomain.ServiceTierConfig{}
	u := contextTestUsage(101)
	cost, _, err := uc.CommitQuotaWithUsage(ctx, r.ReservationID, 1, true, u)
	require.NoError(t, err)
	require.EqualValues(t, 8560, cost)
	require.LessOrEqual(t, cost, r.Amount)
	row := ledger.ledgers[len(ledger.ledgers)-1]
	require.Equal(t, "peak", row.PricingTimeBand)
	require.Equal(t, "Asia/Shanghai", row.PricingTimezone)
	require.Equal(t, admitted.Unix(), row.PricingTimeUnix)
	replay, _, err := uc.CommitQuotaWithUsage(ctx, r.ReservationID, 1, true, contextTestUsage(99))
	require.NoError(t, err)
	require.Equal(t, cost, replay)
	r.RequestSnapshot.ServiceTier.PricingTimeBand = "off_peak"
	require.ErrorIs(t, r.RequestSnapshot.Validate(), ErrRequestSnapshotInvalid)
}

func TestTimePricingExpiredCalendarRejectsNewAdmission(t *testing.T) {
	uc, _ := tierTestUsecase(t)
	uc.serviceTier = timeTestConfig()
	uc.options.Now = func() time.Time { return time.Date(2027, 1, 1, 0, 0, 0, 0, time.UTC) }
	_, err := uc.ReserveQuota(contextTestRequest(), "42", "expired-calendar", 1, "deepseek-flash", "11", 0)
	require.ErrorIs(t, err, ErrRequestSnapshotUnavailable)
}

func TestTimeOnlyPricingRetainsBandEvidenceForEqualPrices(t *testing.T) {
	uc, ledger := tierTestUsecase(t)
	cfg := timeTestConfig()
	cfg.LongContext = nil
	cfg.UpstreamPrices = cfg.Prices
	cfg.TimePricing.PeakPrices = cfg.Prices
	cfg.TimePricing.PeakLongContextPrices = nil
	uc.serviceTier = cfg
	uc.SetTxRunner(&mockTxRunner{})
	snapshots := &recordingPricingSnapshotRepo{}
	uc.SetPricingSnapshotRepo(snapshots)
	ctx := contextTestRequest()
	var hashes []string
	for index, hour := range []int{1, 4} { // 09:00 peak / 12:00 off-peak.
		uc.options.Now = func() time.Time { return time.Date(2026, 10, 29, hour, 0, 0, 0, time.UTC) }
		r, err := uc.ReserveQuota(ctx, "42", fmt.Sprintf("time-only-%d", index), 1, cfg.Model, "11", 0)
		require.NoError(t, err)
		u := usageForReservation(contextTestUsage(100), r)
		supplier := uc.calculateUpstreamCostWithUsage(ctx, 11, cfg.Model, 1, u)
		if index == 0 {
			require.Zero(t, supplier, "missing peak procurement must not inherit off-peak prices")
			require.Equal(t, CostAuditUnpriced, upstreamCostAuditStatus(supplier, u))
		} else {
			require.EqualValues(t, 2100, supplier)
			require.Equal(t, CostAuditPriced, upstreamCostAuditStatus(supplier, u))
		}
		cost, _, err := uc.CommitQuotaWithUsage(ctx, r.ReservationID, 1, true, u)
		require.NoError(t, err)
		require.EqualValues(t, 2100, cost)
		row := ledger.ledgers[len(ledger.ledgers)-1]
		require.Empty(t, row.LongContextPolicy)
		hashes = append(hashes, row.PricingConfigHash)
	}
	require.NotEqual(t, hashes[0], hashes[1], "equal money must retain distinct time-band evidence")
	for _, snapshot := range snapshots.claims {
		require.EqualValues(t, 4, snapshot.SnapshotVersion)
	}
}

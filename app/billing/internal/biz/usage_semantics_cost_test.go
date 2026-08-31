package biz

import (
	"context"
	"math"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Pricing used across the usage-semantics tests: 1m/1tok input, 2m/1tok
// output, 0.1m cache-read, no cache-creation prices (observe mode collapses
// to v0.10.2 so assertions stay simple).
var usageSemanticsTestPrice = ModelPrice{
	InputPrice:     0.001,
	OutputPrice:    0.002,
	CacheReadPrice: floatPtr(0.0001),
}

func TestResolveUserCost_InvalidV1EnvelopeNeverTrustsCanonical(t *testing.T) {
	uc := newUsageSemanticsTestUsecase()
	uc.canonicalUsageMode = CanonicalUsageModeCharge
	cases := []struct {
		name string
		env  *UsageEnvelopeData
	}{
		{
			name: "verified cache without semantics",
			env: &UsageEnvelopeData{ParseStatus: UsageParseStatusVerified,
				Canonical: &CanonicalBuckets{UncachedInputTokens: 10, CacheReadTokens: 2}},
		},
		{
			name: "estimated fabricates cache",
			env: &UsageEnvelopeData{ParseStatus: UsageParseStatusEstimated,
				Canonical: &CanonicalBuckets{UncachedInputTokens: 10, CacheReadTokens: 2}},
		},
		{
			name: "negative canonical bucket",
			env: &UsageEnvelopeData{ParseStatus: UsageParseStatusVerified, Semantics: UsageSemanticsOpenAISubset,
				Canonical: &CanonicalBuckets{UncachedInputTokens: -1}},
		},
		{
			name: "canonical total overflow",
			env: &UsageEnvelopeData{ParseStatus: UsageParseStatusVerified,
				Canonical: &CanonicalBuckets{UncachedInputTokens: math.MaxInt64, OutputTokens: 1}},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, _, audit := uc.resolveUserCost(context.Background(), usageSemanticsTestPrice, 1, "m", 0, LedgerUsage{
				PromptTokens: 100, CompletionTokens: 10,
				UsageContractVersion: UsageContractVersionV1,
				Envelope:             tc.env,
			})
			assert.Equal(t, UsageParseStatusAmbiguous, audit.UsageParseStatus)
			assert.NotEmpty(t, audit.UsageDecisionReason)
		})
	}
}

func TestResolveUserCost_ContractErrorCandidatesIgnorePromptExclusive(t *testing.T) {
	uc := newUsageSemanticsTestUsecase()
	uc.canonicalUsageMode = CanonicalUsageModeCharge
	cost, _, audit := uc.resolveUserCost(context.Background(), usageSemanticsTestPrice, 1, "m", 0, LedgerUsage{
		PromptTokens: 100, CompletionTokens: 10, CacheReadTokens: 40,
		PromptExclusive:      true, // must not collapse both candidates to exclusive
		UsageContractVersion: UsageContractVersionV1,
	})
	assert.Equal(t, UsageParseStatusAmbiguous, audit.UsageParseStatus)
	assert.Equal(t, int64(840), audit.SubsetCandidateCost)
	assert.Equal(t, int64(1240), audit.ExclusiveCandidateCost)
	assert.Equal(t, int64(840), cost)
}

func TestResolveRatioUserCost_AmbiguousUsesLowerCandidate(t *testing.T) {
	uc := NewBillingUsecaseWithPricing(nil, nil, nil, nil, PricingConfig{
		GroupRatios: map[string]float64{"default": 1},
		ModelRatios: map[string]float64{"m": 1},
	})
	uc.canonicalUsageMode = CanonicalUsageModeCharge
	cost, _, audit := uc.calculateCostWithUsage(context.Background(), "default", "m", 0, LedgerUsage{
		PromptTokens: 100, CompletionTokens: 10, CacheReadTokens: 40,
		PromptExclusive:      true,
		UsageContractVersion: UsageContractVersionV1,
	})
	assert.Equal(t, int64(110), cost)
	assert.Equal(t, int64(110), audit.SubsetCandidateCost)
	assert.Equal(t, int64(150), audit.ExclusiveCandidateCost)
}

func newUsageSemanticsTestUsecase() *BillingUsecase {
	return NewBillingUsecaseWithPricing(nil, nil, nil, nil, PricingConfig{
		ModelPrices: map[string]ModelPrice{"m": usageSemanticsTestPrice},
	})
}

// F18: ambiguous + cache must not fabricate a single canonical; both
// candidates go through the SAME pure function and the user pays the LOWER
// final cost, with both candidate costs on the audit trail.
func TestResolveUserCost_AmbiguousSettlesAtLowerCandidate(t *testing.T) {
	uc := newUsageSemanticsTestUsecase()
	// GLM-shaped anomaly: reported prompt=130, cache_read=45056, output=9.
	// subset candidate: uncached=0 -> 45056*0.0001 + 9*0.002 = 4.5056+0.018 = 4.5236 -> 45236
	// exclusive candidate: uncached=130 -> +130*0.001=0.13 -> 46536
	usage := LedgerUsage{
		SourceKind:           CostSourceChannel,
		PromptTokens:         130,
		CompletionTokens:     9,
		CacheReadTokens:      45056,
		UsageContractVersion: UsageContractVersionV1,
		Envelope: &UsageEnvelopeData{
			ParseStatus:          UsageParseStatusAmbiguous,
			DecisionReason:       "cached_exceeds_reported_prompt",
			ReportedPromptTokens: 130,
			SubsetCandidate:      &CanonicalBuckets{UncachedInputTokens: 0, CacheReadTokens: 45056, OutputTokens: 9},
			ExclusiveCandidate:   &CanonicalBuckets{UncachedInputTokens: 130, CacheReadTokens: 45056, OutputTokens: 9},
		},
	}
	for _, mode := range []CanonicalUsageMode{CanonicalUsageModeObserve, CanonicalUsageModeCharge} {
		uc.canonicalUsageMode = mode
		cost, breakdown, audit := uc.resolveUserCost(context.Background(), usageSemanticsTestPrice, 1, "m", 0, usage)
		assert.Equal(t, int64(45236), cost, "mode=%s: user pays the LOWER (subset) candidate", mode)
		assert.Equal(t, int64(45236), breakdown.V0_10_2Cost)
		assert.Equal(t, UsageParseStatusAmbiguous, audit.UsageParseStatus)
		assert.Equal(t, "cached_exceeds_reported_prompt", audit.UsageDecisionReason)
		assert.Equal(t, int64(45236), audit.SubsetCandidateCost)
		assert.Equal(t, int64(46536), audit.ExclusiveCandidateCost)
		assert.Less(t, audit.SubsetCandidateCost, audit.ExclusiveCandidateCost)
	}
}

// The emergency legacy rollback is the ONLY mode allowed to charge an
// ambiguous request with the old formula.
func TestResolveUserCost_AmbiguousLegacyRollbackKeepsOldCharge(t *testing.T) {
	uc := newUsageSemanticsTestUsecase()
	uc.canonicalUsageMode = CanonicalUsageModeLegacy
	usage := LedgerUsage{
		PromptTokens:         130,
		CompletionTokens:     9,
		CacheReadTokens:      45056,
		UsageContractVersion: UsageContractVersionV1,
		Envelope: &UsageEnvelopeData{
			ParseStatus:        UsageParseStatusAmbiguous,
			DecisionReason:     "cached_exceeds_reported_prompt",
			SubsetCandidate:    &CanonicalBuckets{UncachedInputTokens: 0, CacheReadTokens: 45056, OutputTokens: 9},
			ExclusiveCandidate: &CanonicalBuckets{UncachedInputTokens: 130, CacheReadTokens: 45056, OutputTokens: 9},
		},
	}
	cost, _, audit := uc.resolveUserCost(context.Background(), usageSemanticsTestPrice, 1, "m", 0, usage)
	// Old behavior: prompt_exclusive=false -> uncached = max(130-45056,0)=0 -> 45236.
	// (Same as subset here; the point is the legacy branch ran, not the
	// ambiguous policy.)
	assert.Equal(t, int64(45236), cost)
	assert.Equal(t, UsageParseStatusAmbiguous, audit.UsageParseStatus)
}

// F20: a version=0 legacy producer keeps the legacy charge in every mode and
// is recorded as legacy_producer — the only input allowed onto the legacy
// branch.
func TestResolveUserCost_LegacyProducer(t *testing.T) {
	uc := newUsageSemanticsTestUsecase()
	// OpenAI subset shape: prompt=1000 inclusive of 200 cached, output=100.
	// Legacy: uncached=800 -> 800*0.001 + 200*0.0001 + 100*0.002 = 0.8+0.02+0.2 = 1.02 -> 10200
	usage := LedgerUsage{PromptTokens: 1000, CompletionTokens: 100, CacheReadTokens: 200}
	for _, mode := range []CanonicalUsageMode{CanonicalUsageModeLegacy, CanonicalUsageModeObserve, CanonicalUsageModeCharge} {
		uc.canonicalUsageMode = mode
		cost, _, audit := uc.resolveUserCost(context.Background(), usageSemanticsTestPrice, 1, "m", 0, usage)
		assert.Equal(t, int64(10200), cost, "mode=%s: legacy producer keeps legacy charge", mode)
		assert.Equal(t, UsageParseStatusLegacy, audit.UsageParseStatus)
		assert.Equal(t, UsageReasonLegacyProducer, audit.UsageDecisionReason)
		assert.Equal(t, int32(0), audit.UsageContractVersion)
		assert.Equal(t, int64(800), audit.UncachedInputTokens)
	}
}

// F21: a v1 producer whose envelope/canonical is missing or invalid is a
// contract error — it must enter the ambiguous path and NEVER silently fall
// back to legacy (which would bypass the conservative policy).
func TestResolveUserCost_V1ContractErrorGoesAmbiguous(t *testing.T) {
	uc := newUsageSemanticsTestUsecase()
	uc.canonicalUsageMode = CanonicalUsageModeObserve
	cases := []struct {
		name  string
		usage LedgerUsage
	}{
		{
			name:  "v1 with nil envelope",
			usage: LedgerUsage{UsageContractVersion: UsageContractVersionV1, PromptTokens: 100, CompletionTokens: 10},
		},
		{
			name: "v1 verified but canonical nil",
			usage: LedgerUsage{
				UsageContractVersion: UsageContractVersionV1,
				PromptTokens:         100, CompletionTokens: 10,
				Envelope: &UsageEnvelopeData{ParseStatus: UsageParseStatusVerified},
			},
		},
		{
			name: "v1 with unknown parse status",
			usage: LedgerUsage{
				UsageContractVersion: UsageContractVersionV1,
				PromptTokens:         100, CompletionTokens: 10,
				Envelope: &UsageEnvelopeData{ParseStatus: "bogus"},
			},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, _, audit := uc.resolveUserCost(context.Background(), usageSemanticsTestPrice, 1, "m", 0, tc.usage)
			assert.Equal(t, UsageParseStatusAmbiguous, audit.UsageParseStatus)
			assert.Equal(t, UsageReasonV1ContractError, audit.UsageDecisionReason)
		})
	}
}

// Verified v1 canonical: observe charges legacy and records the delta;
// charge charges canonical. For consistent subset usage both agree (delta=0).
func TestResolveUserCost_VerifiedCanonicalModes(t *testing.T) {
	uc := newUsageSemanticsTestUsecase()
	// Anthropic exclusive shape that legacy fields ALSO represent correctly
	// via prompt_exclusive=true: uncached=130, cache=45056, output=9.
	canonical := &CanonicalBuckets{UncachedInputTokens: 130, CacheReadTokens: 45056, OutputTokens: 9}
	usage := LedgerUsage{
		PromptTokens:         130,
		CompletionTokens:     9,
		CacheReadTokens:      45056,
		PromptExclusive:      true, // legacy dual-write carries old semantics
		UsageContractVersion: UsageContractVersionV1,
		Envelope: &UsageEnvelopeData{
			ParseStatus: UsageParseStatusVerified,
			Semantics:   UsageSemanticsAnthropicExclusive,
			Canonical:   canonical,
		},
	}
	uc.canonicalUsageMode = CanonicalUsageModeObserve
	observeCost, _, audit := uc.resolveUserCost(context.Background(), usageSemanticsTestPrice, 1, "m", 0, usage)
	assert.Equal(t, int64(46536), observeCost, "observe charges legacy-derived cost")
	assert.Equal(t, UsageParseStatusVerified, audit.UsageParseStatus)

	uc.canonicalUsageMode = CanonicalUsageModeCharge
	chargeCost, breakdown, _ := uc.resolveUserCost(context.Background(), usageSemanticsTestPrice, 1, "m", 0, usage)
	assert.Equal(t, int64(46536), chargeCost, "charge uses canonical buckets; consistent usage -> delta 0")
	assert.Equal(t, int64(1300), breakdown.PromptCost)
	assert.Equal(t, int64(45056), breakdown.CacheReadCost)
	assert.Equal(t, int64(180), breakdown.CompletionCost)
}

// Per-bucket integer rounding must be asserted directly (§10): each bucket
// rounds independently and the total is their integer sum.
func TestCalculateCanonicalCost_PerBucketRounding(t *testing.T) {
	price := ModelPrice{
		InputPrice:           0.001,
		OutputPrice:          0.002,
		CacheReadPrice:       floatPtr(0.0001),
		CacheCreation5mPrice: floatPtr(0.00125),
		CacheCreation1hPrice: floatPtr(0.0015),
	}
	bd := calculateCanonicalCost(price, CanonicalBuckets{
		UncachedInputTokens: 130, CacheReadTokens: 45056, CacheCreation5mTokens: 40, CacheCreation1hTokens: 70, OutputTokens: 9,
	}, 1.0)
	assert.Equal(t, int64(1300), bd.PromptCost)
	assert.Equal(t, int64(45056), bd.CacheReadCost)
	assert.Equal(t, int64(500), bd.CacheCreation5mCost)
	assert.Equal(t, int64(1050), bd.CacheCreation1hCost)
	assert.Equal(t, int64(180), bd.CompletionCost)
	assert.Equal(t, int64(1300+45056+500+1050+180), bd.CanonicalCost)
	assert.Equal(t, int64(1300+45056+180), bd.V0_10_2Cost)
	assert.Equal(t, bd.CanonicalCost-bd.V0_10_2Cost, bd.ShadowCost)
}

// Commit-level integration: an ambiguous v1 commit writes the audit trail
// (status, candidates, contract version) onto the ledger row.
func TestCommitQuotaWithUsage_AmbiguousLedgerAudit(t *testing.T) {
	t.Setenv("BILLING_CACHE_CREATION_MODE", "observe")
	account := &Account{UserID: "u1", Balance: 1_000_000, Group: "default"}
	accountRepo := &mockAccountRepo{account: account}
	reservationRepo := &mockReservationRepo{reservations: make(map[string]*Reservation)}
	ledgerRepo := &mockLedgerRepo{}
	redeemRepo := &mockRedeemRepo{}
	uc := NewBillingUsecaseWithPricing(accountRepo, reservationRepo, ledgerRepo, redeemRepo, PricingConfig{
		ModelPrices: map[string]ModelPrice{"m": usageSemanticsTestPrice},
	})

	reservation, err := uc.ReserveQuota(context.Background(), "u1", "req-amb", 100000, "m", "ch1", 0)
	require.NoError(t, err)
	_, _, err = uc.CommitQuotaWithUsage(context.Background(), reservation.ReservationID, 45195, true, LedgerUsage{
		SourceKind:           CostSourceChannel,
		PromptTokens:         130,
		CompletionTokens:     9,
		CacheReadTokens:      45056,
		UsageContractVersion: UsageContractVersionV1,
		Envelope: &UsageEnvelopeData{
			ParseStatus:        UsageParseStatusAmbiguous,
			DecisionReason:     "cached_exceeds_reported_prompt",
			SubsetCandidate:    &CanonicalBuckets{UncachedInputTokens: 0, CacheReadTokens: 45056, OutputTokens: 9},
			ExclusiveCandidate: &CanonicalBuckets{UncachedInputTokens: 130, CacheReadTokens: 45056, OutputTokens: 9},
		},
	})
	require.NoError(t, err)
	require.Len(t, ledgerRepo.ledgers, 1)
	ledger := ledgerRepo.ledgers[0]
	assert.Equal(t, int64(-45236), ledger.Amount, "user charged the lower candidate")
	assert.Equal(t, UsageParseStatusAmbiguous, ledger.UsageParseStatus)
	assert.Equal(t, int32(UsageContractVersionV1), ledger.UsageContractVersion)
	assert.Equal(t, int64(45236), ledger.SubsetCandidateCost)
	assert.Equal(t, int64(46536), ledger.ExclusiveCandidateCost)
	assert.Equal(t, "cached_exceeds_reported_prompt", ledger.UsageDecisionReason)
}

package biz

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestRequestSnapshotFreezesFiveBucketsAndChargeModes(t *testing.T) {
	for _, contract := range []int32{UsageContractVersionLegacy, UsageContractVersionV1} {
		t.Run(map[int32]string{0: "legacy_usage", 1: "canonical_usage"}[contract], func(t *testing.T) {
			t.Setenv("BILLING_REQUEST_SNAPSHOT_V2", "true")
			t.Setenv("BILLING_CACHE_CREATION_MODE", "charge")
			t.Setenv("BILLING_CANONICAL_USAGE_MODE", "charge")
			account := &Account{UserID: "42", Group: "default", Balance: 1_000_000}
			ledger := &mockLedgerRepo{}
			uc := NewBillingUsecaseWithPricing(&mockAccountRepo{account: account}, &mockReservationRepo{reservations: map[string]*Reservation{}}, ledger, nil, PricingConfig{
				GroupRatios: map[string]float64{"default": 0.5},
				ModelPrices: map[string]ModelPrice{"m": {InputPrice: 0.001, OutputPrice: 0.003, CacheReadPrice: fp(0.0001), CacheCreation5mPrice: fp(0.0012), CacheCreation1hPrice: fp(0.002)}},
			})
			uc.canonicalCharge.all = true
			ctx := context.Background()
			r, err := uc.ReserveQuota(ctx, "42", "five-buckets", 100, "m", "1", 0)
			require.NoError(t, err)
			originalHash, err := r.RequestSnapshot.Digest()
			require.NoError(t, err)
			// Change every source of live price/selection and turn both charge
			// gates off. Accepted work must still use its reserve-time evidence.
			account.Group = "changed"
			uc.groupRatios["default"] = 10
			price := uc.modelPrices["m"]
			*price.CacheReadPrice, *price.CacheCreation5mPrice, *price.CacheCreation1hPrice = 1, 2, 3
			uc.modelPrices["m"] = ModelPrice{InputPrice: 5, OutputPrice: 6}
			uc.cacheCreationMode = CacheCreationModeObserve
			uc.canonicalUsageMode = CanonicalUsageModeLegacy
			uc.canonicalCharge.all = false
			t.Setenv("BILLING_REQUEST_SNAPSHOT_V2", "false")
			usage := LedgerUsage{PromptTokens: 120, CompletionTokens: 50, CacheReadTokens: 20, CacheCreation5mTokens: 30, CacheCreation1hTokens: 40, UsageContractVersion: contract}
			if contract == UsageContractVersionV1 {
				usage.Envelope = &UsageEnvelopeData{ParseStatus: UsageParseStatusVerified, Semantics: UsageSemanticsOpenAISubset, Canonical: &CanonicalBuckets{UncachedInputTokens: 100, CacheReadTokens: 20, CacheCreation5mTokens: 30, CacheCreation1hTokens: 40, OutputTokens: 50}}
			}
			cost, _, err := uc.CommitQuotaWithUsage(ctx, r.ReservationID, 240, true, usage)
			require.NoError(t, err)
			// Five independently rounded buckets: 500 + 10 + 180 + 400 + 750.
			require.EqualValues(t, 1840, cost)
			require.EqualValues(t, -1840, ledger.ledgers[len(ledger.ledgers)-1].Amount)
			after, err := r.RequestSnapshot.Digest()
			require.NoError(t, err)
			require.Equal(t, originalHash, after)
		})
	}
}

func TestRequestSnapshotReleasedRequestCannotBeReused(t *testing.T) {
	r := &Reservation{Model: "m", Status: ReservationStatusReleased, UserID: "42", RequestID: "released", ReservationID: "r"}
	uc := NewBillingUsecaseWithPricing(&mockAccountRepo{account: &Account{UserID: "42", Balance: 1000, Group: "default"}}, &mockReservationRepo{reservations: map[string]*Reservation{"r": r}}, &mockLedgerRepo{}, nil, PricingConfig{})
	_, err := uc.ReserveQuota(context.Background(), "42", "released", 1, "other", "1", 0)
	require.ErrorIs(t, err, ErrRoutingContextConflict)
	_, err = uc.ReserveQuota(context.Background(), "42", "released", 1, "m", "1", 0)
	require.ErrorIs(t, err, ErrReservationReleased)
}

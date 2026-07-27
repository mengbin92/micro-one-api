package biz

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// floatPtr helper for building optional price fields in tests.
func floatPtr(v float64) *float64 { return &v }

// TestCalculateCanonicalCost_NoCreationPriceKeepsV0_10_2 verifies ADR §5 /
// roadmap §1.3: when cache-creation tokens are present but no price is
// configured, the canonical cost equals the v0.10.2 cost (no fallback to
// InputPrice) and the breakdown is marked unpriced.
func TestCalculateCanonicalCost_NoCreationPriceKeepsV0_10_2(t *testing.T) {
	price := ModelPrice{
		InputPrice:     0.001,
		OutputPrice:    0.002,
		CacheReadPrice: floatPtr(0.0001),
	}
	bd := calculateCanonicalCost(price, 100, 50, 10, 40, 70, 1.0)
	assert.Equal(t, bd.V0_10_2Cost, bd.CanonicalCost, "unpriced canonical must equal v0.10.2")
	assert.Equal(t, int64(0), bd.ShadowCost, "unpriced shadow cost must be 0")
	assert.True(t, bd.CacheCreationUnpriced, "must be flagged unpriced")
}

// TestCalculateCanonicalCost_5m1hPricingInChargeMode verifies the canonical
// cost sums all five buckets when both TTL prices are configured.
func TestCalculateCanonicalCost_5m1hPricingInChargeMode(t *testing.T) {
	// Per-token prices chosen so each bucket contributes a distinct amount.
	price := ModelPrice{
		InputPrice:           0.001, // 1m/1tok
		OutputPrice:          0.002,
		CacheReadPrice:       floatPtr(0.0001),
		CacheCreation5mPrice: floatPtr(0.00125),
		CacheCreation1hPrice: floatPtr(0.0015),
	}
	bd := calculateCanonicalCost(price, 100, 50, 10, 40, 70, 1.0)
	// input = (100-10)*0.001 = 0.09 ; cacheRead = 10*0.0001 = 0.001
	// output = 50*0.002 = 0.1 ; creation5m = 40*0.00125 = 0.05 ; creation1h = 70*0.0015 = 0.105
	// canonical raw = 0.09+0.001+0.1+0.05+0.105 = 0.346 ; *AmountScale(10000) = 3460
	assert.Equal(t, int64(3460), bd.CanonicalCost)
	// v0.10.2 raw = 0.09+0.001+0.1 = 0.191 ; *10000 = 1910
	assert.Equal(t, int64(1910), bd.V0_10_2Cost)
	assert.Equal(t, int64(3460-1910), bd.ShadowCost)
	assert.False(t, bd.CacheCreationUnpriced)
}

// TestCalculateCanonicalCost_OnlyOneTTLPrice verifies that when only the 5m
// price is set, the 1h tokens are unpriced and the whole breakdown collapses
// to v0.10.2 (charge mode never bills unpriced tokens).
func TestCalculateCanonicalCost_OnlyOneTTLPrice(t *testing.T) {
	price := ModelPrice{
		InputPrice:           0.001,
		OutputPrice:          0.002,
		CacheCreation5mPrice: floatPtr(0.00125),
		// CacheCreation1hPrice intentionally nil
	}
	bd := calculateCanonicalCost(price, 100, 50, 0, 40, 70, 1.0)
	assert.True(t, bd.CacheCreationUnpriced, "1h tokens unpriced -> whole breakdown unpriced")
	assert.Equal(t, bd.V0_10_2Cost, bd.CanonicalCost)
}

// TestCalculateCanonicalCost_NegativePriceClamped guards normalizeModelPrices
// so a misconfigured negative cache-creation price cannot produce a negative
// bill.
func TestCalculateCanonicalCost_NegativePriceClamped(t *testing.T) {
	prices := map[string]ModelPrice{
		"m": {
			InputPrice:           0.001,
			OutputPrice:          0.002,
			CacheCreation5mPrice: floatPtr(-0.5),
		},
	}
	out := normalizeModelPrices(prices)
	require.Contains(t, out, "m")
	assert.NotNil(t, out["m"].CacheCreation5mPrice)
	assert.Equal(t, 0.0, *out["m"].CacheCreation5mPrice)
}

// TestBillingUsecase_ObserveModeKeepsV0_10_2Cost is the end-to-end guard for
// roadmap §1.3: in observe mode (default), a request whose model has a
// configured cache-creation price is still charged the v0.10.2 cost — the
// cache-creation tokens are free to the user. The shadow cost is recorded via
// metrics/logging but the balance delta equals v0.10.2.
func TestBillingUsecase_ObserveModeKeepsV0_10_2Cost(t *testing.T) {
	t.Setenv("BILLING_CACHE_CREATION_MODE", "observe")
	account := &Account{UserID: "u1", Balance: 1_000_000, Group: "default"}
	accountRepo := &mockAccountRepo{account: account}
	reservationRepo := &mockReservationRepo{reservations: make(map[string]*Reservation)}
	ledgerRepo := &mockLedgerRepo{}
	redeemRepo := &mockRedeemRepo{}
	uc := NewBillingUsecaseWithPricing(accountRepo, reservationRepo, ledgerRepo, redeemRepo, PricingConfig{
		ModelPrices: map[string]ModelPrice{
			"glm-5.2": {
				InputPrice:           0.001,
				OutputPrice:          0.002,
				CacheCreation5mPrice: floatPtr(0.00125),
				CacheCreation1hPrice: floatPtr(0.0015),
			},
		},
	})
	assert.Equal(t, CacheCreationModeObserve, uc.CacheCreationBillingMode())

	reservation, err := uc.ReserveQuota(context.Background(), "u1", "req-obs", 1000, "glm-5.2", "ch1", 0)
	require.NoError(t, err)
	_, _, err = uc.CommitQuotaWithUsage(context.Background(), reservation.ReservationID, 1000, true, LedgerUsage{
		PromptTokens:          100,
		CompletionTokens:      50,
		CacheReadTokens:       10,
		CacheCreation5mTokens: 40,
		CacheCreation1hTokens: 70,
	})
	require.NoError(t, err)

	for i, l := range ledgerRepo.ledgers {
		t.Logf("observe ledger[%d] amount=%d upstream=%d type=%s", i, l.Amount, l.UpstreamCost, l.Type)
	}
	require.NotEmpty(t, ledgerRepo.ledgers)
	committed := -ledgerRepo.ledgers[len(ledgerRepo.ledgers)-1].Amount
	// v0.10.2 cost: no CacheReadPrice -> cacheRead (10) priced at InputPrice.
	// input=(100-10)*0.001=0.09 ; cacheRead=10*0.001=0.01 ; output=50*0.002=0.1
	// raw=0.2 ; *AmountScale=2000. Observe mode must charge exactly this and
	// NOT the canonical cost (which would add 1550 of cache-creation charge).
	assert.Equal(t, int64(2000), committed, "observe mode must charge v0.10.2 cost")
}

// TestBillingUsecase_ChargeModeAppliesCanonicalCost flips the env to charge
// and verifies the user balance now includes the cache-creation charge.
func TestBillingUsecase_ChargeModeAppliesCanonicalCost(t *testing.T) {
	t.Setenv("BILLING_CACHE_CREATION_MODE", "charge")
	account := &Account{UserID: "u1", Balance: 1_000_000, Group: "default"}
	accountRepo := &mockAccountRepo{account: account}
	reservationRepo := &mockReservationRepo{reservations: make(map[string]*Reservation)}
	ledgerRepo := &mockLedgerRepo{}
	redeemRepo := &mockRedeemRepo{}
	uc := NewBillingUsecaseWithPricing(accountRepo, reservationRepo, ledgerRepo, redeemRepo, PricingConfig{
		ModelPrices: map[string]ModelPrice{
			"glm-5.2": {
				InputPrice:           0.001,
				OutputPrice:          0.002,
				CacheCreation5mPrice: floatPtr(0.00125),
				CacheCreation1hPrice: floatPtr(0.0015),
			},
		},
	})
	assert.Equal(t, CacheCreationModeCharge, uc.CacheCreationBillingMode())

	reservation, err := uc.ReserveQuota(context.Background(), "u1", "req-charge", 1000, "glm-5.2", "ch1", 0)
	require.NoError(t, err)
	_, _, err = uc.CommitQuotaWithUsage(context.Background(), reservation.ReservationID, 1000, true, LedgerUsage{
		PromptTokens:          100,
		CompletionTokens:      50,
		CacheReadTokens:       10,
		CacheCreation5mTokens: 40,
		CacheCreation1hTokens: 70,
	})
	require.NoError(t, err)

	require.NotEmpty(t, ledgerRepo.ledgers)
	committed := -ledgerRepo.ledgers[len(ledgerRepo.ledgers)-1].Amount
	// canonical = v0.10.2 (2000) + cache_creation (40*0.00125 + 70*0.0015)*10000
	//           = 2000 + 500 + 1050 = 3550.
	assert.Equal(t, int64(3550), committed, "charge mode must apply canonical cost")
}

// TestResolveCacheCreationMode_DefaultsObserve guards the default-observe rule
// so a typo or unset env can never silently enable charging.
func TestResolveCacheCreationMode_DefaultsObserve(t *testing.T) {
	cases := map[string]CacheCreationMode{
		"":        CacheCreationModeObserve,
		"observe": CacheCreationModeObserve,
		"OBSERVE": CacheCreationModeObserve,
		"charge":  CacheCreationModeCharge,
		"Charge":  CacheCreationModeCharge,
		"bogus":   CacheCreationModeObserve, // typo -> observe
	}
	for env, want := range cases {
		t.Setenv("BILLING_CACHE_CREATION_MODE", env)
		assert.Equal(t, want, resolveCacheCreationMode(), "env=%q", env)
	}
}

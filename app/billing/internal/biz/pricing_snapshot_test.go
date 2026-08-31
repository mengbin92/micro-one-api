package biz

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	subscriptionbiz "micro-one-api/domain/subscription/biz"
)

func fp(v float64) *float64 { return &v }

// The snapshot must freeze the EFFECTIVE prices the charge consumed: a nil
// CacheReadPrice falls back to InputPrice, so both configurations hash and
// store identically — two configs that charge the same are one snapshot.
func TestBuildPricingSnapshot_EffectivePriceResolution(t *testing.T) {
	withExplicit := buildPricingSnapshot("m", ModelPrice{
		InputPrice:     0.001,
		OutputPrice:    0.002,
		CacheReadPrice: fp(0.001),
	}, 1, CacheCreationModeObserve)
	withFallback := buildPricingSnapshot("m", ModelPrice{
		InputPrice:  0.001,
		OutputPrice: 0.002,
	}, 1, CacheCreationModeObserve)
	assert.Equal(t, withExplicit.ConfigHash, withFallback.ConfigHash)
	assert.Equal(t, 0.001, withFallback.CacheReadPrice)

	unpricedCreation := buildPricingSnapshot("m", ModelPrice{InputPrice: 0.001}, 1, CacheCreationModeCharge)
	explicitZeroCreation := buildPricingSnapshot("m", ModelPrice{
		InputPrice:           0.001,
		CacheCreation5mPrice: fp(0),
		CacheCreation1hPrice: fp(0),
	}, 1, CacheCreationModeCharge)
	assert.Equal(t, unpricedCreation.ConfigHash, explicitZeroCreation.ConfigHash)
	assert.Zero(t, unpricedCreation.CacheCreation5mPrice)
}

// Any input that changes the settled amount must change the hash; identical
// inputs must be stable across calls (the dedup contract depends on it).
func TestBuildPricingSnapshot_HashDifferentiatesChargeInputs(t *testing.T) {
	base := buildPricingSnapshot("m", ModelPrice{InputPrice: 0.001, OutputPrice: 0.002}, 1, CacheCreationModeObserve)
	assert.Len(t, base.ConfigHash, 64)

	same := buildPricingSnapshot("m", ModelPrice{InputPrice: 0.001, OutputPrice: 0.002}, 1, CacheCreationModeObserve)
	assert.Equal(t, base.ConfigHash, same.ConfigHash)

	variants := map[string]*PricingSnapshot{
		"input price":  buildPricingSnapshot("m", ModelPrice{InputPrice: 0.002, OutputPrice: 0.002}, 1, CacheCreationModeObserve),
		"output price": buildPricingSnapshot("m", ModelPrice{InputPrice: 0.001, OutputPrice: 0.003}, 1, CacheCreationModeObserve),
		"cache read":   buildPricingSnapshot("m", ModelPrice{InputPrice: 0.001, CacheReadPrice: fp(0.0001)}, 1, CacheCreationModeObserve),
		"creation 5m":  buildPricingSnapshot("m", ModelPrice{InputPrice: 0.001, CacheCreation5mPrice: fp(0.0015)}, 1, CacheCreationModeObserve),
		"creation 1h":  buildPricingSnapshot("m", ModelPrice{InputPrice: 0.001, CacheCreation1hPrice: fp(0.002)}, 1, CacheCreationModeObserve),
		"group ratio":  buildPricingSnapshot("m", ModelPrice{InputPrice: 0.001}, 1.5, CacheCreationModeObserve),
		"cc mode":      buildPricingSnapshot("m", ModelPrice{InputPrice: 0.001}, 1, CacheCreationModeCharge),
		"model":        buildPricingSnapshot("other", ModelPrice{InputPrice: 0.001}, 1, CacheCreationModeObserve),
	}
	for name, snapshot := range variants {
		assert.NotEqual(t, base.ConfigHash, snapshot.ConfigHash, "%s must change the hash", name)
	}
}

func TestResolveUserCost_SetsPricingEvidence(t *testing.T) {
	uc := newUsageSemanticsTestUsecase()
	_, _, audit := uc.resolveUserCost(context.Background(), usageSemanticsTestPrice, 2, "Test-Model", 0, LedgerUsage{
		PromptTokens: 100, CompletionTokens: 10,
	})
	require.NotNil(t, audit.pricingSnapshot)
	assert.Equal(t, audit.pricingSnapshot.ConfigHash, audit.PricingConfigHash)
	// The hash covers the normalized pricing key, not the raw casing.
	_, _, lower := uc.resolveUserCost(context.Background(), usageSemanticsTestPrice, 2, "test-model", 0, LedgerUsage{
		PromptTokens: 100, CompletionTokens: 10,
	})
	assert.Equal(t, audit.PricingConfigHash, lower.PricingConfigHash)
	assert.Equal(t, 2.0, audit.pricingSnapshot.GroupRatio)
}

// Ratio-priced models have no per-bucket ModelPrice to freeze, so their
// ledger rows keep an empty hash instead of fabricated evidence (§6.3/§8.2).
func TestResolveRatioUserCost_LeavesPricingHashEmpty(t *testing.T) {
	uc := NewBillingUsecaseWithPricing(nil, nil, nil, nil, PricingConfig{
		GroupRatios: map[string]float64{"default": 1},
		ModelRatios: map[string]float64{"m": 2},
	})
	_, _, audit := uc.calculateCostWithUsage(context.Background(), "default", "m", 0, LedgerUsage{
		PromptTokens: 100, CompletionTokens: 10,
	})
	assert.Empty(t, audit.PricingConfigHash)
	assert.Nil(t, audit.pricingSnapshot)
}

type recordingPricingSnapshotRepo struct {
	claims    []*PricingSnapshot
	claimTxs  []subscriptionbiz.Tx
	fetchById map[string]*PricingSnapshot
}

func (r *recordingPricingSnapshotRepo) ClaimPricingSnapshotInTx(ctx context.Context, tx subscriptionbiz.Tx, snapshot *PricingSnapshot) error {
	r.claims = append(r.claims, snapshot)
	r.claimTxs = append(r.claimTxs, tx)
	return nil
}

func (r *recordingPricingSnapshotRepo) ClaimPricingSnapshot(ctx context.Context, snapshot *PricingSnapshot) error {
	r.claims = append(r.claims, snapshot)
	return nil
}

func (r *recordingPricingSnapshotRepo) GetPricingSnapshotByHash(ctx context.Context, configHash string) (*PricingSnapshot, error) {
	if snap, ok := r.fetchById[configHash]; ok {
		return snap, nil
	}
	return nil, ErrPricingSnapshotNotFound
}

// The snapshot must be claimed in the SAME transaction as the ledger rows,
// and every consume row of the request carries the claimed hash (§6.3).
func TestCommitQuotaDualTrack_ClaimsPricingSnapshotWithLedgers(t *testing.T) {
	now := time.Unix(1_000, 0)
	accountRepo := &mockAccountRepo{account: &Account{UserID: "42", Balance: 1_000_000, Group: "default"}}
	reservationRepo := &mockReservationRepo{reservations: map[string]*Reservation{
		"res-snap": {
			ReservationID: "res-snap",
			UserID:        "42",
			Amount:        1,
			Status:        ReservationStatusReserved,
			Model:         "public-model",
			ChannelID:     "1",
		},
	}}
	ledgerRepo := &mockLedgerRepo{}
	snapshotRepo := &recordingPricingSnapshotRepo{fetchById: map[string]*PricingSnapshot{}}
	uc := NewBillingUsecaseWithOptions(BillingOptions{
		AccountRepo:     accountRepo,
		ReservationRepo: reservationRepo,
		LedgerRepo:      ledgerRepo,
		TxRunner:        &mockTxRunner{},
		Now:             func() time.Time { return now },
	})
	uc.SetPricingSnapshotRepo(snapshotRepo)
	uc.modelPrices = normalizeModelPrices(map[string]ModelPrice{
		"public-model": {InputPrice: 0.001, OutputPrice: 0.002, CacheReadPrice: fp(0.0001)},
	})

	_, _, err := uc.CommitQuotaWithUsage(context.Background(), "res-snap", 100, true, LedgerUsage{
		PromptTokens:         100,
		CompletionTokens:     10,
		CacheReadTokens:      20,
		UsageContractVersion: UsageContractVersionV1,
		Envelope: &UsageEnvelopeData{
			ParseStatus: UsageParseStatusVerified,
			Semantics:   UsageSemanticsOpenAISubset,
			Canonical:   &CanonicalBuckets{UncachedInputTokens: 80, CacheReadTokens: 20, OutputTokens: 10},
		},
	})
	require.NoError(t, err)
	require.NotEmpty(t, ledgerRepo.ledgers)

	require.Len(t, snapshotRepo.claims, 1, "one snapshot claim per commit")
	claimed := snapshotRepo.claims[0]
	assert.Equal(t, "public-model", claimed.ModelName)
	assert.Equal(t, CacheCreationModeObserve, CacheCreationMode(claimed.CacheCreationMode))
	// The claim joined the same transaction as the ledger writes.
	require.Len(t, snapshotRepo.claimTxs, 1)
	assert.Equal(t, snapshotRepo.claimTxs[0], ledgerRepo.insertTxs[0])
	for _, ledger := range ledgerRepo.ledgers {
		assert.Equal(t, claimed.ConfigHash, ledger.PricingConfigHash,
			"every consume row of the request references the claimed snapshot")
	}
}

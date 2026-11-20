package biz

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	billingdomain "micro-one-api/domain/billing"
	"micro-one-api/domain/execution"
	"micro-one-api/domain/requesttrace"
	"micro-one-api/domain/routing"
)

func TestNativeCommittedReplaySurvivesEvidenceDeadline(t *testing.T) {
	now := time.Date(2026, 10, 15, 0, 0, 0, 0, time.UTC)
	r := &Reservation{ReservationID: "committed-native", UserID: "42", Status: ReservationStatusCommitted, Amount: 100, ActualCost: 7, RequestSnapshot: &RequestSnapshot{Execution: &execution.Intent{Policy: execution.Policy, EvidenceDeadline: now.Add(-time.Hour).Unix()}}}
	account := &Account{UserID: "42", Balance: 993, UsedAmount: 7}
	ledger := &mockLedgerRepo{}
	uc := NewBillingUsecaseWithOptions(BillingOptions{AccountRepo: &mockAccountRepo{account: account}, ReservationRepo: &mockReservationRepo{reservations: map[string]*Reservation{r.ReservationID: r}}, LedgerRepo: ledger, Now: func() time.Time { return now }})
	usage := LedgerUsage{UsageContractVersion: 2, Envelope: &UsageEnvelopeData{ParseStatus: "verified", Canonical: &CanonicalBuckets{UncachedInputTokens: 4, OutputTokens: 1}}}
	cost, refund, err := uc.CommitQuotaWithUsage(context.Background(), r.ReservationID, 5, true, usage)
	require.NoError(t, err, "a completed financial result must remain replayable when downstream side effects retry after the deadline")
	require.EqualValues(t, 7, cost)
	require.Zero(t, refund)
	require.Equal(t, ReservationStatusCommitted, r.Status)
	require.EqualValues(t, 993, account.Balance)
	require.Empty(t, ledger.ledgers)
}

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

func TestMeteredAudioReservesMeasuresAndSettlesFrozenTariff(t *testing.T) {
	t.Setenv("BILLING_REQUEST_SNAPSHOT_V2", "true")
	const model = "stepaudio-2.5-asr"
	ledger := &mockLedgerRepo{}
	uc := NewBillingUsecaseWithPricing(&mockAccountRepo{account: &Account{UserID: "42", Group: "default", Balance: 10_000}},
		&mockReservationRepo{reservations: map[string]*Reservation{}}, ledger, nil, PricingConfig{})
	uc.SetRoutingGroupReader(pricingGroupReader{})
	uc.pricingStore = capturedPricingStore{PricingConfig{AudioInputPrices: map[string]billingdomain.AudioInputPrice{model: {Yuan: "0.45", YuanPerUSD: "6.8"}}}}
	selected := &routing.ResolvedRoutingContext{Version: 2, UserID: 42, TokenID: 7, GroupID: 10, GroupKey: "default", TokenMode: "fixed",
		TokenRevision: 1, UserAccessRevision: 1, GroupRevision: 1, SelectionSource: "token_fixed"}
	ctx := requesttrace.WithAttempt(context.Background(), requesttrace.Attempt{RootRequestID: "asr", Number: 1, SourceKind: "channel", UpstreamModelID: model})
	_, err := uc.ReserveQuota(ctx, "42", "asr", 100, model, "13", 0, selected)
	require.ErrorIs(t, err, ErrRoutingContextInvalid, "generic token estimates must not admit measured models")

	bound := routing.CostBound{Protocol: "stepaudio_asr_sse", UpstreamModel: model, AudioUnits: 60_000_000}
	r, err := uc.ReserveQuota(routing.WithCostBound(ctx, bound), "42", "asr", 0, model, "13", 0, selected)
	require.NoError(t, err)
	require.EqualValues(t, 11, r.Amount)
	bound.AudioUnits *= 2
	_, err = uc.ReserveQuota(routing.WithCostBound(ctx, bound), "42", "asr", 0, model, "13", 0, selected)
	require.ErrorIs(t, err, ErrRoutingContextConflict)

	uc.pricingStore = capturedPricingStore{PricingConfig{}}
	cost, _, err := uc.CommitQuotaWithUsage(ctx, r.ReservationID, 9999, true, LedgerUsage{PromptTokens: 9999})
	require.NoError(t, err)
	require.EqualValues(t, 11, cost)
	require.EqualValues(t, -11, ledger.ledgers[len(ledger.ledgers)-1].Amount)
}

func TestStepAudioWebSocketSettlesActualUnitsWithinFrozenBound(t *testing.T) {
	t.Setenv("BILLING_REQUEST_SNAPSHOT_V2", "true")
	const model = routing.StepAudioTTSModel
	ledger := &mockLedgerRepo{}
	uc := NewBillingUsecaseWithPricing(&mockAccountRepo{account: &Account{UserID: "42", Group: "default", Balance: 100_000}},
		&mockReservationRepo{reservations: map[string]*Reservation{}}, ledger, nil, PricingConfig{})
	uc.SetRoutingGroupReader(pricingGroupReader{})
	rate := billingdomain.AudioInputPrice{Yuan: "580", YuanPerUSD: "6.8"}
	uc.pricingStore = capturedPricingStore{PricingConfig{AudioInputPrices: map[string]billingdomain.AudioInputPrice{model: rate}}}
	selected := &routing.ResolvedRoutingContext{Version: 2, UserID: 42, TokenID: 7, GroupID: 10, GroupKey: "default", TokenMode: "fixed",
		TokenRevision: 1, UserAccessRevision: 1, GroupRevision: 1, SelectionSource: "token_fixed"}
	ctx := requesttrace.WithAttempt(context.Background(), requesttrace.Attempt{RootRequestID: "tts-ws", Number: 1, SourceKind: "channel", UpstreamModelID: model})
	bound := routing.CostBound{Protocol: routing.StepAudioTTSWSProtocol, UpstreamModel: model, AudioUnits: routing.StepAudioTTSMaxUnits}
	ctx = routing.WithCostBound(ctx, bound)
	withNativeExecution := context.WithValue(ctx, executionIntentKey{}, &execution.Intent{Purpose: "user"})
	_, err := uc.ReserveQuota(withNativeExecution, "42", "tts-ws", 0, model, "13", 0, selected)
	require.ErrorIs(t, err, ErrSettlementReceiptInvalid)
	r, err := uc.ReserveQuota(ctx, "42", "tts-ws", 0, model, "13", 0, selected)
	require.NoError(t, err)
	wantReserve, err := rate.Quota(bound, r.RequestSnapshot.Pricing.GroupRatio, AmountScale)
	require.NoError(t, err)
	require.Equal(t, wantReserve, r.Amount)
	uc.pricingStore = capturedPricingStore{PricingConfig{}}
	for _, invalid := range []int64{-1, 0, 2001} {
		_, _, err = uc.CommitQuotaWithUsage(ctx, r.ReservationID, 0, true, LedgerUsage{ActualAudioUnits: invalid})
		require.ErrorIs(t, err, ErrRoutingContextInvalid)
	}
	actual := LedgerUsage{ActualAudioUnits: 14}
	bound.AudioUnits = actual.ActualAudioUnits
	want, err := rate.Quota(bound, r.RequestSnapshot.Pricing.GroupRatio, AmountScale)
	require.NoError(t, err)
	cost, refund, err := uc.CommitQuotaWithUsage(ctx, r.ReservationID, 0, true, actual)
	require.NoError(t, err)
	require.Equal(t, want, cost)
	require.Equal(t, wantReserve-want, refund)
	_, _, err = uc.CommitQuotaWithUsage(ctx, r.ReservationID, 0, true, actual)
	require.NoError(t, err)
	require.Len(t, ledger.ledgers, 1)
	require.Equal(t, -want, ledger.ledgers[0].Amount)
	require.Contains(t, ledger.ledgers[0].Remark, "actual_audio_units=14")
}

func TestStepAudioWebSocketCannotSettleClosedReservation(t *testing.T) {
	for _, status := range []string{ReservationStatusReleased, ReservationStatusExpired} {
		t.Run(status, func(t *testing.T) {
			closed := &Reservation{ReservationID: "tts-ws", Status: status, Amount: 43}
			uc := NewBillingUsecase(nil, &mockReservationRepo{reservations: map[string]*Reservation{closed.ReservationID: closed}}, nil, nil, nil)
			uc.SetTxRunner(&mockTxRunner{})
			_, _, err := uc.CommitQuotaWithUsage(context.Background(), closed.ReservationID, 0, true, LedgerUsage{ActualAudioUnits: 8})
			require.ErrorIs(t, err, ErrSettlementReservationClosed)
		})
	}
}

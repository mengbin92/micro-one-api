package data

import (
	"context"
	"fmt"
	"github.com/stretchr/testify/require"
	"micro-one-api/app/billing/internal/biz"
	billingdomain "micro-one-api/domain/billing"
	"micro-one-api/domain/execution"
	"micro-one-api/domain/requesttrace"
	subscriptionbiz "micro-one-api/domain/subscription/biz"
	"micro-one-api/domain/upstream/evidence"
	"micro-one-api/pkg/jsonx"
	"micro-one-api/platform/database/testutil"
	"testing"
	"time"
)

func executionFixture(t *testing.T) (*Data, execution.Intent) {
	t.Setenv("BILLING_EXECUTION_DELIVERY_V1", "true")
	d := settlementIntakeDB(t)
	require.NoError(t, d.db.AutoMigrate(&executionIntentModel{}, &executionEventModel{}))
	intakeReservation(t, d, biz.ReservationStatusReserved)
	now := time.Now()
	i := execution.Intent{ReservationID: "res-intake", RequestID: "attempt-1", RootRequestID: "root", AttemptNumber: 1, Purpose: "user", SourceKind: "channel", SourceID: 1, Model: "Exact", UsageContractVersion: 2, ParserRevision: evidence.ParserRevision, ContractRevision: evidence.ContractRevision, CostAlgorithmVersion: execution.AlgorithmVersion, Policy: execution.Policy, LeaseUntil: now.Add(time.Minute).Unix(), EvidenceDeadline: now.Add(time.Hour).Unix(), CostBasis: "unknown"}
	require.NoError(t, saveExecutionIntent(d.db, i))
	return d, i
}
func executionFinal(t *testing.T, i execution.Intent) execution.Event {
	usage, _ := jsonx.Marshal(biz.LedgerUsage{SourceKind: i.SourceKind, UpstreamModelID: i.Model, UsageContractVersion: 2, Envelope: &biz.UsageEnvelopeData{ParseStatus: "verified", Protocol: "openai_chat", Semantics: "openai_subset", ParserRevision: i.ParserRevision, ContractRevision: i.ContractRevision, Canonical: &biz.CanonicalBuckets{}}})
	p := billingdomain.SettlementPayload{ReservationID: i.ReservationID, Success: true, Usage: usage}
	raw, _ := jsonx.Marshal(p)
	record := &evidence.Record{Observation: evidence.Parse([]byte(`{"usage":{"prompt_tokens":0,"completion_tokens":0,"total_tokens":0}}`), "openai_chat"), Complete: true, HTTPStatus: 200}
	e := execution.Event{Record: record, Version: 1, Intent: i, Category: "final", ObservedAt: time.Now().Unix(), Final: raw}
	e.ID = e.Key()
	return e
}
func TestExecutionInboxFinalAtomicIdempotenceAndLateEvidence(t *testing.T) {
	d, i := executionFixture(t)
	r := &reservationRepo{data: d}
	ctx := context.Background()
	e := executionFinal(t, i)
	_, digest, err := e.Encode()
	require.NoError(t, err)
	first, err := r.AcceptExecution(ctx, e, digest)
	require.NoError(t, err)
	require.True(t, first.Received)
	require.True(t, first.SettlementAccepted)
	require.False(t, first.Settled)
	_, err = r.AcceptExecution(ctx, e, digest)
	require.NoError(t, err)
	var count int64
	require.NoError(t, d.db.Model(&settlementTaskModel{}).Count(&count).Error)
	require.EqualValues(t, 1, count)
	changed := e
	changed.BuildRevision = "changed"
	_, newDigest, _ := changed.Encode()
	_, err = r.AcceptExecution(ctx, changed, newDigest)
	require.ErrorIs(t, err, biz.ErrSettlementPayloadConflict)
	require.NoError(t, d.db.Model(&reservationModel{}).Where("reservation_id = ?", i.ReservationID).Update("status", biz.ReservationStatusReleased).Error)
	late, err := r.AcceptExecution(ctx, e, digest)
	require.NoError(t, err)
	require.True(t, late.Received)
	require.False(t, late.SettlementAccepted)
	require.False(t, late.Settled)
	require.Equal(t, "released", late.Status)
}
func TestExecutionInboxRejectsFinancialBypassAndPreservesInternalCosts(t *testing.T) {
	d, i := executionFixture(t)
	r := &reservationRepo{data: d}
	e := executionFinal(t, i)
	e.Intent.Model = "different"
	_, digest, _ := e.Encode()
	_, err := r.AcceptExecution(context.Background(), e, digest)
	require.ErrorIs(t, err, biz.ErrSettlementPayloadConflict)
	var count int64
	require.NoError(t, d.db.Model(&executionEventModel{}).Count(&count).Error)
	require.Zero(t, count, "financial rejection must roll back evidence inbox insertion")
	i.ReservationID = ""
	i.Purpose = "canary"
	e = execution.NewIntentEvent(i, time.Now())
	_, digest, _ = e.Encode()
	receipt, err := r.AcceptExecution(context.Background(), e, digest)
	require.NoError(t, err)
	require.True(t, receipt.Received)
	require.False(t, receipt.SettlementAccepted)
	require.NoError(t, d.db.Model(&settlementTaskModel{}).Count(&count).Error)
	require.Zero(t, count)
}
func TestExecutionEvidenceDeadlineCannotBeExtendedByFinal(t *testing.T) {
	d, i := executionFixture(t)
	i.LeaseUntil = time.Now().Add(-2 * time.Hour).Unix()
	i.EvidenceDeadline = time.Now().Add(-time.Hour).Unix()
	raw, _ := jsonx.Marshal(i)
	require.NoError(t, d.db.Model(&executionIntentModel{}).Where("id = ?", i.Key()).Updates(map[string]any{"payload": string(raw), "lease_until": i.LeaseUntil, "evidence_deadline": i.EvidenceDeadline}).Error)
	e := executionFinal(t, i)
	_, digest, _ := e.Encode()
	r, err := (&reservationRepo{data: d}).AcceptExecution(context.Background(), e, digest)
	require.NoError(t, err)
	require.True(t, r.Received)
	require.False(t, r.SettlementAccepted)
	require.Equal(t, "evidence_deadline_elapsed", r.Status)
	var count int64
	require.NoError(t, d.db.Model(&settlementTaskModel{}).Count(&count).Error)
	require.Zero(t, count)
}

func TestNativeV2ReservationFreezesMoneyAndExpiresAtomically(t *testing.T) {
	for _, fixture := range []struct{ Dialect, Status string }{{"sqlite", "verified"}, {"sqlite", "missing"}, {"sqlite", "invalid"}, {"sqlite", "unsupported"}, {"mysql", "verified"}, {"mysql", "missing"}, {"mysql", "invalid"}, {"mysql", "unsupported"}, {"postgres", "verified"}, {"postgres", "missing"}, {"postgres", "invalid"}, {"postgres", "unsupported"}} {
		status := fixture.Status
		t.Run(fixture.Dialect+"/"+status, func(t *testing.T) {
			t.Setenv("BILLING_REQUEST_SNAPSHOT_V2", "true")
			t.Setenv("BILLING_EXECUTION_DELIVERY_V1", "true")
			db := testutil.RoutingContextDB(t, fixture.Dialect)
			require.True(t, db.Migrator().HasTable(&executionIntentModel{}))
			require.True(t, db.Migrator().HasTable(&executionEventModel{}))
			require.True(t, db.Migrator().HasTable(&settlementTaskModel{}))
			uc, _, d, _, _ := snapshotBillingFixture(t, db, false, 1, 1)
			ctx := requesttrace.WithAttempt(context.Background(), requesttrace.Attempt{RootRequestID: "root", Number: 1, SourceKind: "channel", UpstreamModelID: "Exact"})
			ctx, err := uc.PrepareExecution(ctx, "native-money", "1", 0)
			require.NoError(t, err)
			reservation, err := uc.ReserveQuota(ctx, "7001", "native-money", 100, "m", "1", 0, requestContext())
			require.NoError(t, err)
			require.NotNil(t, reservation.RequestSnapshot.Execution)
			var intents int64
			require.NoError(t, db.Model(&executionIntentModel{}).Count(&intents).Error)
			require.EqualValues(t, 1, intents)
			_, _, err = uc.CommitQuotaWithUsage(ctx, reservation.ReservationID, 100, true, biz.LedgerUsage{PromptTokens: 100})
			require.ErrorIs(t, err, biz.ErrSettlementReceiptInvalid, "legacy caller cannot bypass the frozen v2 policy")
			envelope := &biz.UsageEnvelopeData{ParseStatus: status, DecisionReason: "fixture"}
			if status == "verified" {
				envelope.Canonical = &biz.CanonicalBuckets{}
			}
			cost, _, err := uc.CommitQuotaWithUsage(ctx, reservation.ReservationID, 0, true, biz.LedgerUsage{UsageContractVersion: 2, Envelope: envelope})
			require.NoError(t, err)
			require.Zero(t, cost)
			stored, err := d.reservationRepo.GetReservation(ctx, reservation.ReservationID)
			require.NoError(t, err)
			if status == "verified" {
				require.Equal(t, biz.ReservationStatusCommitted, stored.Status)
				replay, _, err := uc.CommitQuotaWithUsage(ctx, reservation.ReservationID, 0, true, biz.LedgerUsage{UsageContractVersion: 2, Envelope: envelope})
				require.NoError(t, err)
				require.Zero(t, replay, "known zero must survive worker restart and financial replay")
			} else {
				require.Equal(t, biz.ReservationStatusReleased, stored.Status)
			}
			account, err := d.accountRepo.GetAccountSnapshot(ctx, "7001")
			require.NoError(t, err)
			require.EqualValues(t, 1000000, account.Balance)
			require.Zero(t, account.FrozenAmount)
		})
	}
}
func TestExecutionLeaseAndDeadlineExpiryGuard(t *testing.T) {
	t.Setenv("BILLING_REQUEST_SNAPSHOT_V2", "true")
	t.Setenv("BILLING_EXECUTION_DELIVERY_V1", "true")
	db := testutil.RoutingContextDB(t, "sqlite")
	require.True(t, db.Migrator().HasTable(&executionIntentModel{}))
	require.True(t, db.Migrator().HasTable(&executionEventModel{}))
	require.True(t, db.Migrator().HasTable(&settlementTaskModel{}))
	uc, _, d, _, _ := snapshotBillingFixture(t, db, false, 1, 1)
	ctx := requesttrace.WithAttempt(context.Background(), requesttrace.Attempt{RootRequestID: "root", Number: 1, SourceKind: "channel", UpstreamModelID: "Exact"})
	ctx, err := uc.PrepareExecution(ctx, "lease", "1", 0)
	require.NoError(t, err)
	i := biz.ExecutionIntent(ctx)
	i.LeaseUntil = time.Now().Add(-time.Hour).Unix()
	i.EvidenceDeadline = time.Now().Add(time.Hour).Unix()
	reservation, err := uc.ReserveQuota(ctx, "7001", "lease", 100, "m", "1", 0, requestContext())
	require.NoError(t, err)
	require.NoError(t, db.Model(&reservationModel{}).Where("reservation_id = ?", reservation.ReservationID).Update("expired_at", time.Now().Add(-time.Minute)).Error)
	require.ErrorIs(t, uc.ReleaseReservation(ctx, reservation.ReservationID, "expired fixture", biz.ReservationStatusExpired), biz.ErrReservationExpiryDeferred)
	// A ready task arriving after candidate selection must still obey the maximum deadline.
	task := &biz.SettleTask{ReservationID: reservation.ReservationID, ActualTokens: 10, Success: true, Timestamp: time.Now()}
	require.NoError(t, NewSettlementTaskRepo(d).SaveTask(ctx, task))
	require.Error(t, NewTxRunner(d).RunInTx(ctx, func(ctx context.Context, tx subscriptionbiz.Tx) error {
		won, err := (&reservationRepo{data: d}).CASExpiredReservationStatus(ctx, tx, reservation.ReservationID, time.Unix(i.EvidenceDeadline+1, 0))
		require.NoError(t, err)
		require.True(t, won)
		return fmt.Errorf("rollback fixture")
	}))
}

func TestNativeV2PositivePricingAndSupplierTariffStayFrozen(t *testing.T) {
	t.Setenv("BILLING_REQUEST_SNAPSHOT_V2", "true")
	t.Setenv("BILLING_EXECUTION_DELIVERY_V1", "true")
	db := testutil.RoutingContextDB(t, "sqlite")
	uc, prices, d, _, _ := snapshotBillingFixture(t, db, false, 1, 1)
	prices.set(biz.PricingConfig{GroupRatios: map[string]float64{"default": 0.5}, ModelRatios: map[string]float64{"m": 2}, CompletionRatios: map[string]float64{"m": 3}, UpstreamPrices: map[string]biz.ModelPrice{"channel:1:Exact": {InputPrice: 0.001, OutputPrice: 0.002}}})
	ctx := requesttrace.WithAttempt(context.Background(), requesttrace.Attempt{RootRequestID: "root", Number: 1, SourceKind: "channel", UpstreamModelID: "Exact"})
	ctx, err := uc.PrepareExecution(ctx, "positive", "1", 0)
	require.NoError(t, err)
	res, err := uc.ReserveQuota(ctx, "7001", "positive", 100, "m", "1", 0, requestContext())
	require.NoError(t, err)
	record := &evidence.Record{Observation: evidence.Parse([]byte(`{"usage":{"prompt_tokens":4,"completion_tokens":1,"total_tokens":5}}`), "openai_chat"), Complete: true, HTTPStatus: 200}
	full, err := uc.StoredExecutionIntent(ctx, res.ReservationID)
	require.NoError(t, err)
	require.Empty(t, res.RequestSnapshot.Execution.Tariff, "customer price snapshot must never expose the supplier tariff")
	supplier := biz.SupplierEstimate(full, record)
	require.NotNil(t, supplier)
	require.Positive(t, *supplier)
	prices.set(biz.PricingConfig{GroupRatios: map[string]float64{"default": 99}, ModelRatios: map[string]float64{"m": 99}, CompletionRatios: map[string]float64{"m": 99}, UpstreamPrices: map[string]biz.ModelPrice{"channel:1:Exact": {InputPrice: 99, OutputPrice: 99}}})
	usage := biz.LedgerUsage{SourceKind: "channel", UpstreamModelID: "Exact", PromptTokens: 4, CompletionTokens: 1, UsageContractVersion: 2, Envelope: &biz.UsageEnvelopeData{ParseStatus: "verified", Canonical: &biz.CanonicalBuckets{UncachedInputTokens: 4, OutputTokens: 1}}}
	cost, _, err := uc.CommitQuotaWithUsage(ctx, res.ReservationID, 5, true, usage)
	require.NoError(t, err)
	require.EqualValues(t, 7, cost)
	saved, err := d.reservationRepo.GetReservation(ctx, res.ReservationID)
	require.NoError(t, err)
	full, err = uc.StoredExecutionIntent(ctx, saved.ReservationID)
	require.NoError(t, err)
	require.Equal(t, *supplier, *biz.SupplierEstimate(full, record))
}

func TestNativeFinalRejectsProjectionAndReservationOverride(t *testing.T) {
	d, i := executionFixture(t)
	e := executionFinal(t, i)
	var p billingdomain.SettlementPayload
	require.NoError(t, jsonx.Unmarshal(e.Final, &p))
	var usage biz.LedgerUsage
	require.NoError(t, jsonx.Unmarshal(p.Usage, &usage))
	usage.Envelope.Canonical.OutputTokens = 10
	p.ActualTokens = 10
	p.Usage, _ = jsonx.Marshal(usage)
	e.Final, _ = jsonx.Marshal(p)
	_, digest, _ := e.Encode()
	_, err := (&reservationRepo{data: d}).AcceptExecution(context.Background(), e, digest)
	require.ErrorIs(t, err, biz.ErrSettlementReceiptInvalid)
	e = executionFinal(t, i)
	require.NoError(t, jsonx.Unmarshal(e.Final, &p))
	p.SubscriptionAccountID = 999
	e.Final, _ = jsonx.Marshal(p)
	_, digest, _ = e.Encode()
	_, err = (&reservationRepo{data: d}).AcceptExecution(context.Background(), e, digest)
	require.ErrorIs(t, err, biz.ErrSettlementReceiptInvalid)
	var count int64
	require.NoError(t, d.db.Model(&settlementTaskModel{}).Count(&count).Error)
	require.Zero(t, count)
}

package data

import (
	"context"
	billingdomain "micro-one-api/domain/billing"
	"micro-one-api/domain/requesttrace"
	"micro-one-api/pkg/jsonx"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
	"micro-one-api/app/billing/internal/biz"
	dbtest "micro-one-api/platform/database/testutil"
)

// Reusing a pricing snapshot is the common path for tiered requests. The
// duplicate must not abort PostgreSQL's surrounding wallet transaction.
func TestPricingSnapshotReuseKeepsTransactionUsable(t *testing.T) {
	for _, driver := range []string{"sqlite", "mysql", "postgres"} {
		t.Run(driver, func(t *testing.T) {
			db := dbtest.RoutingContextDB(t, driver)
			repo := &pricingSnapshotRepo{data: &Data{db: db}}
			first := &biz.PricingSnapshot{ConfigHash: "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa", ModelName: "gpt-5.1", SnapshotVersion: 2, ServiceTier: "max", ServiceTierPolicy: "actual_or_max_v1", ServiceTierConfigHash: "cccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccc", GroupRatio: 1, CacheCreationMode: "charge"}
			require.NoError(t, repo.ClaimPricingSnapshot(context.Background(), first))
			require.NoError(t, db.Transaction(func(tx *gorm.DB) error {
				duplicate := *first
				if err := repo.ClaimPricingSnapshotInTx(context.Background(), &gormTx{db: tx}, &duplicate); err != nil {
					return err
				}
				next := *first
				next.ID = 0
				next.ConfigHash = "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"
				return repo.ClaimPricingSnapshotInTx(context.Background(), &gormTx{db: tx}, &next)
			}))
			var count int64
			require.NoError(t, db.Model(&pricingSnapshotModel{}).Count(&count).Error)
			require.EqualValues(t, 2, count)
			got, err := repo.GetPricingSnapshotByHash(context.Background(), first.ConfigHash)
			require.NoError(t, err)
			require.Equal(t, "max", got.ServiceTier)
			require.EqualValues(t, 2, got.SnapshotVersion)
		})
	}
}

// The task is persisted before the worker starts, as after a Billing restart.
// Recovery must use the reserve-time tariff and immutable actual-tier metadata.
func TestServiceTierDurableAsyncRecoveryUsesFrozenPricing(t *testing.T) {
	t.Setenv("BILLING_REQUEST_SNAPSHOT_V2", "true")
	db := dbtest.RoutingContextDB(t, "sqlite")
	uc, prices, d, _, _ := snapshotBillingFixture(t, db, false, 1, 1)
	uc.SetPricingSnapshotRepo(NewPricingSnapshotRepo(d))
	pointer := func(v float64) *float64 { return &v }
	config := billingdomain.ServiceTierConfig{Version: 1, Enabled: true, SourceKind: "channel", SourceID: 11, Model: "gpt-5.1", Prices: map[string]biz.ModelPrice{
		"default":  {InputPrice: .0001, OutputPrice: .0002, CacheReadPrice: pointer(.00001)},
		"priority": {InputPrice: .0002, OutputPrice: .0004, CacheReadPrice: pointer(.00002)},
		"flex":     {InputPrice: .00005, OutputPrice: .0001, CacheReadPrice: pointer(.000005)},
	}}
	prices.set(biz.PricingConfig{ServiceTier: config, GroupRatios: map[string]float64{"default": .5}})
	ctx := requesttrace.WithAttempt(context.Background(), requesttrace.Attempt{RootRequestID: "async-tier-root", Number: 1, SourceKind: "channel", UpstreamModelID: config.Model})
	ctx = billingdomain.WithServiceTierRequest(ctx, billingdomain.ServiceTierRequest{RequestedServiceTier: "priority", Version: 1, Protocol: "openai_chat", InputTokenBound: 1000, OutputTokenBound: 100})
	reserved, err := uc.ReserveQuota(ctx, "7001", "async-tier-attempt", 1000, config.Model, "11", 0, requestContext())
	require.NoError(t, err)
	originalHash, err := reserved.RequestSnapshot.Digest()
	require.NoError(t, err)
	task := &biz.SettleTask{RequestID: reserved.ReservationID, ReservationID: reserved.ReservationID, Model: config.Model, ActualTokens: 150, Success: true, Timestamp: time.Now(), Usage: biz.LedgerUsage{ActualServiceTier: "flex", ServiceTierStatus: "reported", UsageContractVersion: 1, Envelope: &biz.UsageEnvelopeData{ParseStatus: "verified", Semantics: biz.UsageSemanticsOpenAISubset, Canonical: &biz.CanonicalBuckets{UncachedInputTokens: 100, OutputTokens: 50}}}}
	store := NewSettlementTaskRepo(d)
	require.NoError(t, store.SaveTask(ctx, task))
	// Changing live prices, the account group, and disabling admission cannot
	// change an already persisted final settlement after a restart.
	prices.set(biz.PricingConfig{GroupRatios: map[string]float64{"default": 100, "other": 100}, ModelPrices: map[string]biz.ModelPrice{config.Model: {InputPrice: 9, OutputPrice: 9}}})
	require.NoError(t, db.Table("users").Where("id = ?", 7001).Update("group", "changed").Error)
	worker := biz.NewAsyncBillingUsecase(uc, nil, 10, 1, time.Millisecond)
	worker.SetTaskStore(store)
	defer func() { require.NoError(t, worker.Close()) }()
	require.Eventually(t, func() bool {
		var row settlementTaskModel
		return db.Where("reservation_id = ?", reserved.ReservationID).First(&row).Error == nil && row.Status == "completed"
	}, 4*time.Second, 20*time.Millisecond)
	persisted, err := d.reservationRepo.GetReservation(context.Background(), reserved.ReservationID)
	require.NoError(t, err)
	require.Equal(t, biz.ReservationStatusCommitted, persisted.Status)
	require.EqualValues(t, 50, persisted.ActualCost)
	afterHash, err := persisted.RequestSnapshot.Digest()
	require.NoError(t, err)
	require.Equal(t, originalHash, afterHash)
	var row ledgerModel
	require.NoError(t, db.Where("reference_id = ?", reserved.ReservationID).First(&row).Error)
	require.Equal(t, "priority", row.RequestedServiceTier)
	require.Equal(t, "flex", row.ActualServiceTier)
	require.Equal(t, "reported", row.ServiceTierStatus)
	require.Equal(t, "flex", row.BilledServiceTier)
	require.Equal(t, billingdomain.ServiceTierPolicy, row.ServiceTierPolicy)
	require.Equal(t, config.Digest(), row.ServiceTierConfigHash)
	var snapshot pricingSnapshotModel
	require.NoError(t, db.Where("config_hash = ?", row.PricingConfigHash).First(&snapshot).Error)
	require.EqualValues(t, 2, snapshot.SnapshotVersion)
	require.Equal(t, "flex", snapshot.ServiceTier)
	// Exact duplicate intake is harmless; changing the actual tier under the
	// same final event is a durable payload conflict, even after completion.
	require.NoError(t, worker.Settle(ctx, task))
	changed := *task
	changed.Usage.ActualServiceTier = "priority"
	require.ErrorIs(t, worker.Settle(ctx, &changed), biz.ErrSettlementPayloadConflict)
	var taskRow settlementTaskModel
	require.NoError(t, db.Where("reservation_id = ?", reserved.ReservationID).First(&taskRow).Error)
	var retained biz.SettleTask
	require.NoError(t, jsonx.Unmarshal([]byte(taskRow.Payload), &retained))
	require.Equal(t, "flex", retained.Usage.ActualServiceTier)
	require.Equal(t, "reported", retained.Usage.ServiceTierStatus)
	var count int64
	require.NoError(t, db.Model(&ledgerModel{}).Where("reference_id = ?", reserved.ReservationID).Count(&count).Error)
	require.EqualValues(t, 1, count)
	account, err := d.accountRepo.GetAccountSnapshot(ctx, "7001")
	require.NoError(t, err)
	require.EqualValues(t, 999950, account.Balance)
	require.Zero(t, account.FrozenAmount)
	require.EqualValues(t, 50, account.UsedAmount)
	require.EqualValues(t, 1, account.RequestCount)
}

package data

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"micro-one-api/app/billing/internal/biz"
	billingdomain "micro-one-api/domain/billing"
	"micro-one-api/domain/requesttrace"
	dbtest "micro-one-api/platform/database/testutil"
)

// Exercise migration-produced tables through the actual repo conversions, so
// immutable threshold/rule evidence survives DB round-trips on every dialect.
func TestLongContextEvidenceStorageRoundTrip(t *testing.T) {
	for _, driver := range []string{"sqlite", "mysql", "postgres"} {
		t.Run(driver, func(t *testing.T) {
			db := dbtest.RoutingContextDB(t, driver)
			d := &Data{db: db}
			ctx := context.Background()
			snapshot := &biz.PricingSnapshot{
				ConfigHash: strings.Repeat("a", 64), ModelName: "deepseek-flash", SnapshotVersion: 3,
				ServiceTier: "default", ServiceTierPolicy: "actual_or_max_v1", ServiceTierConfigHash: strings.Repeat("b", 64),
				LongContextPolicy: "whole_request_input_gt_v1", LongContextThreshold: 128000, LongContextTier: "long",
				PricingTimeBand: "peak", PricingTimezone: "Asia/Shanghai",
				InputPrice: .000002, OutputPrice: .000004, GroupRatio: 1, CacheCreationMode: "charge",
			}
			pricingRepo := NewPricingSnapshotRepo(d)
			require.NoError(t, pricingRepo.ClaimPricingSnapshot(ctx, snapshot))
			require.NoError(t, pricingRepo.ClaimPricingSnapshot(ctx, snapshot))
			got, err := pricingRepo.GetPricingSnapshotByHash(ctx, snapshot.ConfigHash)
			require.NoError(t, err)
			require.Equal(t, snapshot.LongContextPolicy, got.LongContextPolicy)
			require.Equal(t, snapshot.LongContextThreshold, got.LongContextThreshold)
			require.Equal(t, snapshot.LongContextTier, got.LongContextTier)
			require.Equal(t, snapshot.PricingTimeBand, got.PricingTimeBand)
			require.Equal(t, snapshot.PricingTimezone, got.PricingTimezone)
			require.EqualValues(t, 3, got.SnapshotVersion)
			ledger := &biz.Ledger{
				UserID: "7001", Type: biz.LedgerTypeConsume, Amount: -10, CreatedAt: time.Now(), ReferenceID: "long-context-round-trip",
				LongContextPolicy: snapshot.LongContextPolicy, LongContextThreshold: snapshot.LongContextThreshold,
				LongContextTier: "long", LongContextInputTokens: 128001, PricingConfigHash: snapshot.ConfigHash,
				ServiceTierConfigHash: snapshot.ServiceTierConfigHash,
				PricingTimeBand:       "peak", PricingTimezone: "Asia/Shanghai", PricingTimeUnix: 1793232000,
			}
			repo := NewLedgerRepo(d)
			require.NoError(t, repo.CreateLedger(ctx, ledger))
			rows, total, err := repo.ListLedgers(ctx, "7001", 1, 20)
			require.NoError(t, err)
			require.EqualValues(t, 1, total)
			require.Len(t, rows, 1)
			require.Equal(t, ledger.LongContextPolicy, rows[0].LongContextPolicy)
			require.Equal(t, ledger.LongContextThreshold, rows[0].LongContextThreshold)
			require.Equal(t, ledger.LongContextTier, rows[0].LongContextTier)
			require.Equal(t, ledger.LongContextInputTokens, rows[0].LongContextInputTokens)
			require.Equal(t, ledger.PricingTimeBand, rows[0].PricingTimeBand)
			require.Equal(t, ledger.PricingTimezone, rows[0].PricingTimezone)
			require.Equal(t, ledger.PricingTimeUnix, rows[0].PricingTimeUnix)
		})
	}
}

// Save the final task before starting a fresh worker/usecase. Recovery must use
// the reserve-time time band and long-context tariff after calendar expiry,
// live price changes and an entirely new Billing clock.
func TestLongContextTimePricingDurableRecoveryUsesFrozenClock(t *testing.T) {
	for _, driver := range []string{"sqlite", "mysql", "postgres"} {
		t.Run(driver, func(t *testing.T) {
			t.Setenv("BILLING_REQUEST_SNAPSHOT_V2", "true")
			db := dbtest.RoutingContextDB(t, driver)
			uc, livePricing, d, _, _ := snapshotBillingFixture(t, db, false, 1, 1)
			uc.SetPricingSnapshotRepo(NewPricingSnapshotRepo(d))
			ptr := func(v float64) *float64 { return &v }
			prices := func(scale float64) map[string]billingdomain.ModelPrice {
				result := map[string]billingdomain.ModelPrice{}
				for _, tier := range []string{"default", "priority", "flex"} {
					result[tier] = billingdomain.ModelPrice{InputPrice: .001 * scale, OutputPrice: .002 * scale, CacheReadPrice: ptr(.001 * scale), CacheCreation5mPrice: ptr(.001 * scale), CacheCreation1hPrice: ptr(.001 * scale)}
				}
				return result
			}
			zone, err := time.LoadLocation("Asia/Shanghai")
			require.NoError(t, err)
			config := billingdomain.ServiceTierConfig{
				Version: 3, Enabled: true, SourceKind: "channel", SourceID: 11, Model: "deepseek-flash", Prices: prices(1),
				LongContext: &billingdomain.LongContextConfig{ThresholdTokens: 189, Policy: billingdomain.LongContextPolicy, Prices: prices(2)},
				TimePricing: &billingdomain.TimePricingConfig{Timezone: "Asia/Shanghai", CalendarValidThrough: time.Now().In(zone).Format("2006-01-02"), Holidays: []string{}, PeakWindows: []billingdomain.PricingTimeWindow{{Weekdays: []int{1, 2, 3, 4, 5, 6, 7}, StartMinute: 0, EndMinute: 1440}}, PeakPrices: prices(3), PeakLongContextPrices: prices(6)},
			}
			require.NoError(t, config.Validate())
			livePricing.set(biz.PricingConfig{ServiceTier: config, GroupRatios: map[string]float64{"default": .5}})
			ctx := requesttrace.WithAttempt(context.Background(), requesttrace.Attempt{RootRequestID: "async-context-root", Number: 1, SourceKind: "channel", UpstreamModelID: config.Model})
			ctx = billingdomain.WithServiceTierRequest(ctx, billingdomain.ServiceTierRequest{Version: 1, Protocol: "openai_chat", InputTokenBound: 1000, OutputTokenBound: 100})
			reserved, err := uc.ReserveQuota(ctx, "7001", "async-context", 1, config.Model, "11", 0, requestContext())
			require.NoError(t, err)
			frozen := reserved.RequestSnapshot.ServiceTier
			require.Equal(t, "peak", frozen.PricingTimeBand)
			hash, err := reserved.RequestSnapshot.Digest()
			require.NoError(t, err)
			task := &biz.SettleTask{RequestID: reserved.ReservationID, ReservationID: reserved.ReservationID, Model: config.Model, ActualTokens: 240, Success: true, Timestamp: time.Now(), Usage: biz.LedgerUsage{ActualServiceTier: "default", ServiceTierStatus: "reported", UsageContractVersion: 1, Envelope: &biz.UsageEnvelopeData{ParseStatus: "verified", Semantics: biz.UsageSemanticsOpenAISubset, Canonical: &biz.CanonicalBuckets{UncachedInputTokens: 100, CacheReadTokens: 20, CacheCreation5mTokens: 30, CacheCreation1hTokens: 40, OutputTokens: 50}}}}
			tasks := NewSettlementTaskRepo(d)
			require.NoError(t, tasks.SaveTask(ctx, task))
			livePricing.set(biz.PricingConfig{ModelPrices: map[string]biz.ModelPrice{config.Model: {InputPrice: 99, OutputPrice: 99}}})
			// The restarted owner has no live tariff store, and its clock is past
			// the admitted calendar. Neither may replace the persisted evidence.
			options := uc.Options()
			options.Now = func() time.Time { return time.Unix(frozen.PricingTimeUnix, 0).Add(48 * time.Hour) }
			restarted := biz.NewBillingUsecaseWithOptions(options)
			restarted.SetPricingSnapshotRepo(NewPricingSnapshotRepo(d))
			worker := biz.NewAsyncBillingUsecase(restarted, nil, 10, 1, time.Millisecond)
			worker.SetTaskStore(tasks)
			defer func() { require.NoError(t, worker.Close()) }()
			require.Eventually(t, func() bool {
				var row settlementTaskModel
				return db.Where("reservation_id = ?", reserved.ReservationID).First(&row).Error == nil && row.Status == "completed"
			}, 5*time.Second, 20*time.Millisecond)
			persisted, err := d.reservationRepo.GetReservation(ctx, reserved.ReservationID)
			require.NoError(t, err)
			require.Equal(t, biz.ReservationStatusCommitted, persisted.Status)
			require.EqualValues(t, 8700, persisted.ActualCost) // (190*.006 + 50*.012) * .5 * 10000.
			require.LessOrEqual(t, persisted.ActualCost, persisted.Amount)
			after, err := persisted.RequestSnapshot.Digest()
			require.NoError(t, err)
			require.Equal(t, hash, after)
			var row ledgerModel
			require.NoError(t, db.Where("reference_id = ?", reserved.ReservationID).First(&row).Error)
			require.Equal(t, "long", row.LongContextTier)
			require.EqualValues(t, 190, row.LongContextInputTokens)
			require.EqualValues(t, 189, row.LongContextThreshold)
			require.Equal(t, "peak", row.PricingTimeBand)
			require.Equal(t, "Asia/Shanghai", row.PricingTimezone)
			require.Equal(t, frozen.PricingTimeUnix, row.PricingTimeUnix)
			var snapshot pricingSnapshotModel
			require.NoError(t, db.Where("config_hash = ?", row.PricingConfigHash).First(&snapshot).Error)
			require.EqualValues(t, 4, snapshot.SnapshotVersion)
			require.Equal(t, "long", snapshot.LongContextTier)
			require.Equal(t, "peak", snapshot.PricingTimeBand)
			require.NoError(t, worker.Settle(ctx, task))
			var count int64
			require.NoError(t, db.Model(&ledgerModel{}).Where("reference_id = ?", reserved.ReservationID).Count(&count).Error)
			require.EqualValues(t, 1, count)
		})
	}
}

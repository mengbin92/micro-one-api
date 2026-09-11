package data

import (
	"context"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
	"micro-one-api/app/billing/internal/biz"
	"micro-one-api/domain/routing"
	subscriptionbiz "micro-one-api/domain/subscription/biz"
	subscriptiondata "micro-one-api/domain/subscription/data"
	"micro-one-api/platform/database/testutil"
)

type requestPricingStore struct {
	mu     sync.Mutex
	config biz.PricingConfig
}

func (s *requestPricingStore) GetPricingConfig(context.Context) (biz.PricingConfig, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.config, nil
}
func (s *requestPricingStore) set(c biz.PricingConfig) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.config = c
}

type requestGroupReader struct{}

func (requestGroupReader) GetRoutingGroup(_ context.Context, id int64) (*routing.Group, error) {
	return &routing.Group{ID: id, Key: "default", Status: "enabled", Revision: 1}, nil
}

func requestContext() *routing.ResolvedRoutingContext {
	return &routing.ResolvedRoutingContext{Version: 2, UserID: 7001, TokenID: 11, GroupID: 9, GroupKey: "default", TokenMode: "inherit", TokenRevision: 1, UserAccessRevision: 1, GroupRevision: 1, SelectionSource: "user_default"}
}

func snapshotBillingFixture(t *testing.T, db *gorm.DB, withSubscription bool, q float64, limit float64) (*biz.BillingUsecase, *requestPricingStore, *Data, *subscriptiondata.Repository, *subscriptionbiz.UserSubscription) {
	t.Helper()
	ctx := context.Background()
	require.NoError(t, db.Table("users").Create(map[string]any{"id": 7001, "username": "snapshot-user", "group": "other", "status": 1, "balance": 1000000}).Error)
	d := &Data{db: db}
	d.accountRepo, d.reservationRepo, d.ledgerRepo = NewAccountRepo(d), NewReservationRepo(d), NewLedgerRepo(d)
	store := &requestPricingStore{config: biz.PricingConfig{GroupRatios: map[string]float64{"default": 0.5, "other": 9}, ModelRatios: map[string]float64{"m": 2}, CompletionRatios: map[string]float64{"m": 3}}}
	uc := biz.NewBillingUsecaseWithPricing(d.accountRepo, d.reservationRepo, d.ledgerRepo, nil, biz.PricingConfig{PricingStore: store})
	uc.SetTxRunner(NewTxRunner(d))
	uc.SetRoutingGroupReader(requestGroupReader{})
	repo := subscriptiondata.NewRepository(db, nil)
	var sub *subscriptionbiz.UserSubscription
	if withSubscription {
		group := &subscriptionbiz.SubscriptionGroup{Name: "frozen-policy", DisplayName: "Frozen policy", RateMultiplier: q, DailyLimitUSD: &limit, WeeklyLimitUSD: &limit, MonthlyLimitUSD: &limit, Status: 1}
		require.NoError(t, repo.CreateGroup(ctx, group))
		now := time.Now().Unix()
		sub = &subscriptionbiz.UserSubscription{UserID: 7001, GroupID: group.ID, SubscriptionName: "Frozen subscription", Status: subscriptionbiz.SubscriptionStatusActive, StartsAt: now, ExpiresAt: now + 86400*60, DailyWindowStart: now, WeeklyWindowStart: now, MonthlyWindowStart: now, CreatedAt: now}
		require.NoError(t, repo.CreateSubscription(ctx, sub))
		uc.SetSubscriptionPrimatives(subscriptionbiz.NewSubscriptionUsecase(repo, repo))
	}
	return uc, store, d, repo, sub
}

func TestRequestSnapshotFrozenPricing(t *testing.T) {
	for _, dialect := range []string{"sqlite", "mysql", "postgres"} {
		for _, mode := range []string{"ratio", "model_price"} {
			for _, async := range []bool{false, true} {
				t.Run(dialect+"/"+mode+map[bool]string{false: "/sync", true: "/async"}[async], func(t *testing.T) {
					t.Setenv("BILLING_REQUEST_SNAPSHOT_V2", "true")
					db := testutil.RoutingContextDB(t, dialect)
					uc, prices, d, _, _ := snapshotBillingFixture(t, db, false, 1, 1)
					want := int64(2500)
					if mode == "model_price" {
						prices.set(biz.PricingConfig{GroupRatios: map[string]float64{"default": 0.5}, ModelPrices: map[string]biz.ModelPrice{"m": {InputPrice: 0.0001, OutputPrice: 0.0002}}})
						want = 1000
					}
					ctx := context.Background()
					r, err := uc.ReserveQuota(ctx, "7001", "price-request", 1000, "m", "1", 0, requestContext())
					require.NoError(t, err)
					require.NotNil(t, r.RequestSnapshot)
					require.Equal(t, "default", r.RequestSnapshot.GroupKey)
					// The account group was already different; a subsequent group and
					// model-price change must not affect this accepted request.
					require.NoError(t, db.Table("users").Where("id = ?", 7001).Update("group", "changed").Error)
					prices.set(biz.PricingConfig{GroupRatios: map[string]float64{"default": 8, "changed": 7}, ModelPrices: map[string]biz.ModelPrice{"m": {InputPrice: 1, OutputPrice: 2}}})
					replay, err := uc.ReserveQuota(ctx, "7001", "price-request", 999, "m", "1", 0, requestContext())
					require.NoError(t, err)
					require.Equal(t, r.ReservationID, replay.ReservationID)
					changed := *requestContext()
					changed.UserAccessRevision++
					_, err = uc.ReserveQuota(ctx, "7001", "price-request", 1000, "m", "1", 0, &changed)
					require.ErrorIs(t, err, biz.ErrRoutingContextConflict)
					_, err = uc.ReserveQuota(ctx, "7001", "price-request", 1000, "different", "1", 0, requestContext())
					require.ErrorIs(t, err, biz.ErrRoutingContextConflict)
					usage := biz.LedgerUsage{PromptTokens: 1000, CompletionTokens: 500}
					if async {
						worker := biz.NewAsyncBillingUsecase(uc, nil, 10, 1, time.Millisecond)
						worker.Settle(ctx, &biz.SettleTask{ReservationID: r.ReservationID, ActualTokens: 1500, Success: true, Usage: usage, Timestamp: time.Now()})
						require.NoError(t, worker.Close())
					} else {
						cost, _, err := uc.CommitQuotaWithUsage(ctx, r.ReservationID, 1500, true, usage)
						require.NoError(t, err)
						require.Equal(t, want, cost)
					}
					stored, err := d.reservationRepo.GetReservation(ctx, r.ReservationID)
					require.NoError(t, err)
					require.Equal(t, biz.ReservationStatusCommitted, stored.Status)
					require.Equal(t, want, stored.ActualCost)
					cost, _, err := uc.CommitQuotaWithUsage(ctx, r.ReservationID, 9000, true, usage)
					require.NoError(t, err)
					require.Equal(t, want, cost)
					account, err := d.accountRepo.GetAccountSnapshot(ctx, "7001")
					require.NoError(t, err)
					require.Equal(t, int64(1000000)-want, account.Balance)
					require.Zero(t, account.FrozenAmount)
					require.Equal(t, want, account.UsedAmount)
					require.EqualValues(t, 1, account.RequestCount)
					var count int64
					require.NoError(t, db.Table("billing_ledgers").Where("reference_id = ?", r.ReservationID).Count(&count).Error)
					require.EqualValues(t, 1, count)
					// Corrupt evidence cannot silently fall back to current prices.
					require.NoError(t, db.Table("billing_reservations").Where("reservation_id = ?", r.ReservationID).Update("request_snapshot", "{}").Error)
					_, err = d.reservationRepo.GetReservation(ctx, r.ReservationID)
					require.ErrorIs(t, err, biz.ErrRequestSnapshotInvalid)
				})
			}
		}
	}
}

func TestRequestSnapshotFrozenSubscription(t *testing.T) {
	for _, dialect := range []string{"sqlite", "mysql", "postgres"} {
		t.Run(dialect, func(t *testing.T) {
			t.Setenv("BILLING_REQUEST_SNAPSHOT_V2", "true")
			db := testutil.RoutingContextDB(t, dialect)
			uc, _, d, repo, sub := snapshotBillingFixture(t, db, true, 2, 0.5)
			ctx := context.Background()
			first, err := uc.ReserveQuota(ctx, "7001", "first", 1000, "m", "1", 0, requestContext())
			require.NoError(t, err)
			require.InDelta(t, 0.1, first.SubscriptionAmountUSD, 1e-12)
			require.InDelta(t, 0.2, *first.SubscriptionAccountingUSD, 1e-12)
			group, err := repo.GetGroupByID(ctx, sub.GroupID)
			require.NoError(t, err)
			group.RateMultiplier = 4
			require.NoError(t, repo.UpdateGroup(ctx, group))
			second, err := uc.ReserveQuota(ctx, "7001", "second", 1000, "m", "1", 0, requestContext())
			require.NoError(t, err)
			require.InDelta(t, 0.075, second.SubscriptionAmountUSD, 1e-12)
			require.EqualValues(t, 250, second.BalanceAmount)
			require.NoError(t, uc.ReleaseQuota(ctx, second.ReservationID, "test release"))
			cost, _, err := uc.CommitQuotaWithUsage(ctx, first.ReservationID, 1000, true, biz.LedgerUsage{PromptTokens: 1000})
			require.NoError(t, err)
			require.EqualValues(t, 1000, cost)
			current, err := repo.GetSubscriptionByID(ctx, sub.ID)
			require.NoError(t, err)
			require.InDelta(t, 0.2, current.DailyUsageUSD, 1e-12)
			var charge struct{ AccountingUSD float64 }
			require.NoError(t, db.Table("subscription_window_charges").Where("reservation_id = ?", first.ReservationID).Take(&charge).Error)
			require.InDelta(t, 0.2, charge.AccountingUSD, 1e-12)
			account, err := d.accountRepo.GetAccountSnapshot(ctx, "7001")
			require.NoError(t, err)
			require.EqualValues(t, 1000000, account.Balance)
			require.Zero(t, account.FrozenAmount)
		})
	}
}

func TestRequestSnapshotOriginalSubscriptionWindows(t *testing.T) {
	for _, driver := range []string{"sqlite", "mysql", "postgres"} {
		for _, change := range []string{"day", "week", "month", "revoke_and_replace", "replace_policy", "expire"} {
			t.Run(driver+"/"+change, func(t *testing.T) {
				t.Setenv("BILLING_REQUEST_SNAPSHOT_V2", "true")
				db := testutil.RoutingContextDB(t, driver)
				_, _, d, repo, sub := snapshotBillingFixture(t, db, true, 2, 10)
				now := time.Unix(sub.StartsAt, 0)
				uc := biz.NewBillingUsecaseWithOptions(biz.BillingOptions{AccountRepo: d.accountRepo, ReservationRepo: d.reservationRepo, LedgerRepo: d.ledgerRepo, TxRunner: NewTxRunner(d), SubscriptionUsecase: subscriptionbiz.NewSubscriptionUsecase(repo, repo), AllowOverdraft: true, Now: func() time.Time { return now }})
				uc.SetRoutingGroupReader(requestGroupReader{})
				ctx := context.Background()
				r, err := uc.ReserveQuota(ctx, "7001", "window-request", 1000, "m", "1", 0, requestContext())
				require.NoError(t, err)
				wantDaily, wantWeekly, wantMonthly := 0.2, 0.2, 0.2
				var replacement *subscriptionbiz.UserSubscription
				switch change {
				case "day":
					now = now.Add(25 * time.Hour)
					wantDaily = 0
				case "week":
					now = now.Add(8 * 24 * time.Hour)
					wantDaily, wantWeekly = 0, 0
				case "month":
					now = now.Add(31 * 24 * time.Hour)
					wantDaily, wantWeekly, wantMonthly = 0, 0, 0
				case "revoke_and_replace":
					sub.Status = subscriptionbiz.SubscriptionStatusRevoked
					require.NoError(t, repo.UpdateSubscriptionFields(ctx, sub, []subscriptionbiz.SubscriptionField{subscriptionbiz.SubscriptionFieldStatus}))
					replacement = &subscriptionbiz.UserSubscription{UserID: 7001, GroupID: sub.GroupID, SubscriptionName: "replacement", Status: subscriptionbiz.SubscriptionStatusActive, StartsAt: sub.StartsAt, ExpiresAt: sub.ExpiresAt}
					require.NoError(t, repo.CreateSubscription(ctx, replacement))
				case "replace_policy":
					policy := &subscriptionbiz.SubscriptionGroup{Name: "replacement-policy", DisplayName: "replacement", RateMultiplier: 8, Status: 1}
					require.NoError(t, repo.CreateGroup(ctx, policy))
					sub.GroupID = policy.ID
					require.NoError(t, repo.UpdateSubscriptionFields(ctx, sub, []subscriptionbiz.SubscriptionField{subscriptionbiz.SubscriptionFieldGroupID}))
					wantDaily, wantWeekly, wantMonthly = 0, 0, 0
				case "expire":
					sub.ExpiresAt = time.Now().Unix() - 1
					require.NoError(t, repo.UpdateSubscriptionFields(ctx, sub, []subscriptionbiz.SubscriptionField{subscriptionbiz.SubscriptionFieldExpiresAt}))
				}
				cost, _, err := uc.CommitQuotaWithUsage(ctx, r.ReservationID, 2000, true, biz.LedgerUsage{PromptTokens: 2000})
				require.NoError(t, err)
				require.EqualValues(t, 2000, cost)
				stored, err := repo.GetSubscriptionByID(ctx, sub.ID)
				require.NoError(t, err)
				require.InDelta(t, wantDaily, stored.DailyUsageUSD, 1e-12)
				require.InDelta(t, wantWeekly, stored.WeeklyUsageUSD, 1e-12)
				require.InDelta(t, wantMonthly, stored.MonthlyUsageUSD, 1e-12)
				var charge struct {
					SubscriptionID, DailyWindowStart int64
					AccountingUSD                    float64
				}
				require.NoError(t, db.Table("subscription_window_charges").Where("reservation_id = ?", r.ReservationID).Take(&charge).Error)
				require.Equal(t, r.SubscriptionID, charge.SubscriptionID)
				require.Equal(t, r.SubscriptionDailyWindowStart, charge.DailyWindowStart)
				require.InDelta(t, 0.2, charge.AccountingUSD, 1e-12)
				if replacement != nil {
					fresh, err := repo.GetSubscriptionByID(ctx, replacement.ID)
					require.NoError(t, err)
					require.Zero(t, fresh.DailyUsageUSD)
					require.Zero(t, fresh.WeeklyUsageUSD)
					require.Zero(t, fresh.MonthlyUsageUSD)
				}
				account, err := d.accountRepo.GetAccountSnapshot(ctx, "7001")
				require.NoError(t, err)
				require.EqualValues(t, 999000, account.Balance)
				require.Zero(t, account.FrozenAmount)
			})
		}
	}
}

func TestRequestSnapshotCommitRollback(t *testing.T) {
	for _, driver := range []string{"sqlite", "mysql", "postgres"} {
		t.Run(driver, func(t *testing.T) {
			t.Setenv("BILLING_REQUEST_SNAPSHOT_V2", "true")
			db := testutil.RoutingContextDB(t, driver)
			uc, _, d, repo, sub := snapshotBillingFixture(t, db, true, 2, 0.1)
			ctx := context.Background()
			r, err := uc.ReserveQuota(ctx, "7001", "rollback", 1000, "m", "1", 0, requestContext())
			require.NoError(t, err)
			require.EqualValues(t, 500, r.BalanceAmount)
			require.NoError(t, db.Callback().Create().Before("gorm:create").Register("fail_snapshot_ledger", func(tx *gorm.DB) {
				if tx.Statement.Table == "billing_ledgers" {
					tx.AddError(fmt.Errorf("injected ledger failure"))
				}
			}))
			_, _, err = uc.CommitQuotaWithUsage(ctx, r.ReservationID, 1000, true, biz.LedgerUsage{PromptTokens: 1000})
			require.Error(t, err)
			require.NoError(t, db.Callback().Create().Remove("fail_snapshot_ledger"))
			stored, err := d.reservationRepo.GetReservation(ctx, r.ReservationID)
			require.NoError(t, err)
			require.Equal(t, biz.ReservationStatusReserved, stored.Status)
			current, err := repo.GetSubscriptionByID(ctx, sub.ID)
			require.NoError(t, err)
			require.Zero(t, current.DailyUsageUSD)
			var count int64
			require.NoError(t, db.Table("subscription_window_charges").Count(&count).Error)
			require.Zero(t, count)
			cost, _, err := uc.CommitQuotaWithUsage(ctx, r.ReservationID, 1000, true, biz.LedgerUsage{PromptTokens: 1000})
			require.NoError(t, err)
			require.EqualValues(t, 1000, cost)
			account, err := d.accountRepo.GetAccountSnapshot(ctx, "7001")
			require.NoError(t, err)
			require.EqualValues(t, 999500, account.Balance)
			require.Zero(t, account.FrozenAmount)
			current, err = repo.GetSubscriptionByID(ctx, sub.ID)
			require.NoError(t, err)
			require.InDelta(t, 0.1, current.DailyUsageUSD, 1e-12)
		})
	}
}

type fixedRequestGroupReader struct{}

func (fixedRequestGroupReader) GetRoutingGroup(_ context.Context, id int64) (*routing.Group, error) {
	key := "default"
	if id == 10 {
		key = "vip"
	}
	return &routing.Group{ID: id, Key: key, Status: "enabled", Revision: 1}, nil
}
func TestFixedKeysFreezeSeparateBillingGroups(t *testing.T) {
	for _, dialect := range []string{"sqlite", "mysql", "postgres"} {
		for _, async := range []bool{false, true} {
			t.Run(fmt.Sprintf("%s/async=%v", dialect, async), func(t *testing.T) {
				t.Setenv("BILLING_REQUEST_SNAPSHOT_V2", "true")
				db := testutil.RoutingContextDB(t, dialect)
				uc, prices, d, _, _ := snapshotBillingFixture(t, db, false, 1, 1)
				uc.SetRoutingGroupReader(fixedRequestGroupReader{})
				prices.set(biz.PricingConfig{GroupRatios: map[string]float64{"default": .5, "vip": 2, "other": 9}, ModelRatios: map[string]float64{"m": 2}, CompletionRatios: map[string]float64{"m": 3}})
				ctx := context.Background()
				var reservations []*biz.Reservation
				for i, key := range []string{"default", "vip"} {
					c := requestContext()
					c.TokenMode = "fixed"
					c.SelectionSource = "token_fixed"
					c.TokenID += int64(i)
					c.GroupID += int64(i)
					c.GroupKey = key
					r, err := uc.ReserveQuota(ctx, "7001", fmt.Sprintf("fixed-%d", i), 1000, "m", fmt.Sprint(i+1), 0, c)
					require.NoError(t, err)
					require.Equal(t, key, r.RequestSnapshot.GroupKey)
					require.Equal(t, c.Digest(), r.RequestSnapshot.Routing.Digest())
					reservations = append(reservations, r)
				}
				require.EqualValues(t, 1000, reservations[0].BalanceAmount)
				require.EqualValues(t, 4000, reservations[1].BalanceAmount)
				require.NoError(t, db.Table("users").Where("id = ?", 7001).Update("group", "changed").Error)
				prices.set(biz.PricingConfig{GroupRatios: map[string]float64{"default": 9, "vip": 9}, ModelRatios: map[string]float64{"m": 99}})
				usage := biz.LedgerUsage{PromptTokens: 1000, CompletionTokens: 500}
				for i, r := range reservations {
					if async {
						worker := biz.NewAsyncBillingUsecase(uc, nil, 10, 1, time.Millisecond)
						worker.Settle(ctx, &biz.SettleTask{ReservationID: r.ReservationID, ActualTokens: 1500, Success: true, Usage: usage, Timestamp: time.Now()})
						require.NoError(t, worker.Close())
					} else {
						_, _, err := uc.CommitQuotaWithUsage(ctx, r.ReservationID, 1500, true, usage)
						require.NoError(t, err)
					}
					row, err := d.reservationRepo.GetReservation(ctx, r.ReservationID)
					require.NoError(t, err)
					require.EqualValues(t, []int64{2500, 10000}[i], row.ActualCost)
				}
				ledgers, _, err := uc.ListLedgers(ctx, "7001", 1, 20)
				require.NoError(t, err)
				require.Len(t, ledgers, 2)
				snapshots, err := uc.LedgerRequestSnapshots(ctx, ledgers)
				require.NoError(t, err)
				for i, r := range reservations {
					snap := snapshots[r.ReservationID]
					require.NotNil(t, snap)
					require.EqualValues(t, 9+i, snap.Routing.GroupID)
					require.EqualValues(t, 11+i, snap.Routing.TokenID)
					require.Equal(t, []string{"default", "vip"}[i], snap.GroupKey)
				}
			})
		}
	}
}

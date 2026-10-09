package data

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
	"micro-one-api/app/billing/internal/biz"
	subscriptionbiz "micro-one-api/domain/subscription/biz"
	"micro-one-api/platform/database/testutil"
)

func TestConcurrentPaymentNotifyAndRefund(t *testing.T) {
	for _, dialect := range []string{"sqlite", "mysql", "postgres"} {
		t.Run(dialect, func(t *testing.T) {
			db := testutil.RoutingContextDB(t, dialect)
			require.NoError(t, db.AutoMigrate(&PaymentOrder{}))
			if dialect == "sqlite" {
				sqlDB, err := db.DB()
				require.NoError(t, err)
				sqlDB.SetMaxOpenConns(1)
			}
			ctx := context.Background()
			require.NoError(t, db.Table("users").Create(map[string]any{"id": 42, "username": "payment-race", "status": 1, "balance": 0}).Error)
			repo := NewPaymentRepo(&Data{db: db})
			_, err := repo.CreateOrder(ctx, &biz.PaymentOrder{TradeNo: "race-order", UserID: "42", Status: biz.PaymentOrderStatusPending, AssetType: biz.PaymentAssetTypeBalance, AssetIssueStatus: biz.PaymentAssetIssueStatusPending, AssetAmount: 100, MoneyCents: 100})
			require.NoError(t, err)
			for _, refund := range []bool{false, true} {
				var wins atomic.Int32
				var wg sync.WaitGroup
				errs := make(chan error, 8)
				start := make(chan struct{})
				for i := 0; i < 8; i++ {
					wg.Add(1)
					go func() {
						defer wg.Done()
						<-start
						move := func(tx subscriptionbiz.Tx, amount int64) error {
							return txDB(tx).Table("users").Where("id = ?", 42).UpdateColumn("balance", gorm.Expr("balance + ?", amount)).Error
						}
						var changed bool
						var err error
						if refund {
							_, changed, err = repo.MarkOrderRefunded(ctx, "race-order", "test", func(_ context.Context, _ *biz.PaymentOrder, tx subscriptionbiz.Tx) error { return move(tx, -100) })
						} else {
							_, changed, err = repo.MarkOrderPaid(ctx, "race-order", "provider", func(_ *biz.PaymentOrder, tx subscriptionbiz.Tx) error { return move(tx, 100) })
						}
						if changed {
							wins.Add(1)
						}
						errs <- err
					}()
				}
				close(start)
				wg.Wait()
				close(errs)
				for err := range errs {
					require.NoError(t, err)
				}
				require.EqualValues(t, 1, wins.Load())
				var balance int64
				require.NoError(t, db.Table("users").Where("id = ?", 42).Pluck("balance", &balance).Error)
				if refund {
					require.Zero(t, balance)
				} else {
					require.EqualValues(t, 100, balance)
				}
			}
		})
	}
}

func TestCommitRacesExpiryRelease(t *testing.T) {
	for _, dialect := range []string{"sqlite", "mysql", "postgres"} {
		t.Run(dialect, func(t *testing.T) {
			t.Setenv("BILLING_REQUEST_SNAPSHOT_V2", "true")
			db := testutil.RoutingContextDB(t, dialect)
			if dialect == "sqlite" {
				sqlDB, err := db.DB()
				require.NoError(t, err)
				sqlDB.SetMaxOpenConns(1)
			}
			uc, _, d, _, _ := snapshotBillingFixture(t, db, false, 1, 1)
			ctx := context.Background()
			r, err := uc.ReserveQuota(ctx, "7001", "expiry-race", 1000, "m", "1", 0, requestContext())
			require.NoError(t, err)
			start := make(chan struct{})
			errs := make(chan error, 2)
			go func() {
				<-start
				_, _, err := uc.CommitQuotaWithUsage(ctx, r.ReservationID, 1000, true, biz.LedgerUsage{PromptTokens: 1000})
				errs <- err
			}()
			go func() {
				<-start
				errs <- uc.ReleaseReservation(ctx, r.ReservationID, "expiry scan", biz.ReservationStatusExpired)
			}()
			close(start)
			for i := 0; i < 2; i++ {
				err := <-errs
				if err != nil {
					require.True(t, errors.Is(err, biz.ErrReservationReleased) || errors.Is(err, biz.ErrReservationCommitted), "%v", err)
				}
			}
			stored, err := d.reservationRepo.GetReservation(ctx, r.ReservationID)
			require.NoError(t, err)
			require.Contains(t, []string{biz.ReservationStatusCommitted, biz.ReservationStatusExpired}, stored.Status)
			account, err := d.accountRepo.GetAccountSnapshot(ctx, "7001")
			require.NoError(t, err)
			require.Zero(t, account.FrozenAmount)
			if stored.Status == biz.ReservationStatusCommitted {
				require.Equal(t, int64(1000000)-stored.ActualCost, account.Balance)
			} else {
				require.EqualValues(t, 1000000, account.Balance)
			}
			var count int64
			require.NoError(t, db.Table("billing_ledgers").Where("reference_id = ?", r.ReservationID).Count(&count).Error)
			require.EqualValues(t, 1, count)
		})
	}
}

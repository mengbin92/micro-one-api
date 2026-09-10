package data

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"micro-one-api/app/billing/internal/biz"
	subscriptionbiz "micro-one-api/domain/subscription/biz"
	subscriptiondata "micro-one-api/domain/subscription/data"
	"micro-one-api/platform/database/migrate"
	"micro-one-api/platform/database/xdb"

	"github.com/stretchr/testify/require"
)

// Exercise the production single-connection pool and real migrations, rather
// than AutoMigrate schemas or mocks that can hide nested pool acquisitions.
func TestSQLiteSettlementSingleConnection(t *testing.T) {
	for _, subscription := range []bool{false, true} {
		name := "wallet"
		if subscription {
			name = "subscription"
		}
		t.Run(name, func(t *testing.T) {
			db, err := xdb.Open(xdb.DatabaseConfig{Driver: "sqlite3", DSN: filepath.Join(t.TempDir(), "lite.db")})
			require.NoError(t, err)
			sqlDB, err := db.DB()
			require.NoError(t, err)
			t.Cleanup(func() { _ = sqlDB.Close() })
			require.Equal(t, 1, sqlDB.Stats().MaxOpenConnections)
			_, err = migrate.NewWithDriver(sqlDB, "../../../../migrations/sqlite", "sqlite3").Apply(context.Background())
			require.NoError(t, err)
			require.NoError(t, db.Exec(`INSERT INTO users (id, username, balance, status) VALUES (1, 'lite', 1000000, 1)`).Error)
			require.NoError(t, db.Exec(`INSERT INTO system_options (option_key, option_value) VALUES ('ModelRatio', '{"gpt-3.5-turbo":2}')`).Error)
			d := &Data{db: db}
			subRepo := subscriptiondata.NewRepository(db, nil)
			if subscription {
				group := &subscriptionbiz.SubscriptionGroup{Name: "lite", RateMultiplier: 1, Status: 1}
				require.NoError(t, subRepo.CreateGroup(context.Background(), group))
				require.NoError(t, subRepo.CreateSubscription(context.Background(), &subscriptionbiz.UserSubscription{
					UserID: 1, GroupID: group.ID, Status: "active", ExpiresAt: time.Now().Add(time.Hour).Unix(),
				}))
			}
			uc := biz.NewBillingUsecaseWithPricing(NewAccountRepo(d), NewReservationRepo(d), NewLedgerRepo(d), NewRedeemRepo(d),
				biz.PricingConfig{PricingStore: NewPricingConfigRepo(d)})
			uc.SetTxRunner(NewTxRunner(d))
			uc.SetSubscriptionPrimatives(subscriptionbiz.NewSubscriptionUsecase(subRepo, subRepo))
			ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
			defer cancel()
			reservation, err := uc.ReserveQuota(ctx, "1", "lite-request", 100, "gpt-3.5-turbo", "1", 0)
			require.NoError(t, err)
			cost, _, err := uc.CommitQuotaWithUsage(ctx, reservation.ReservationID, 10, true,
				biz.LedgerUsage{PromptTokens: 10})
			require.NoError(t, err)
			require.Equal(t, int64(20), cost, "dynamic ModelRatio must be used, not timeout fallback pricing")
			retryCost, _, err := uc.CommitQuota(ctx, reservation.ReservationID, 10, true)
			require.NoError(t, err)
			require.Equal(t, cost, retryCost)
			require.Equal(t, int64(1), ledgerCount(t, db))
			snapshot, err := uc.GetAccountSnapshot(ctx, "1")
			require.NoError(t, err)
			require.Zero(t, snapshot.FrozenAmount)
			if subscription {
				require.Equal(t, int64(1000000), snapshot.Balance)
			} else {
				require.Equal(t, int64(1000000)-cost, snapshot.Balance)
			}
		})
	}
}

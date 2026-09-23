package data

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
	"micro-one-api/app/channel/internal/biz"
	"micro-one-api/platform/database/testutil"
)

func TestQuotaResetDoesNotEraseNewWindowUsage(t *testing.T) {
	repo := setupChannelTestDB(t)
	require.NoError(t, repo.db.AutoMigrate(&subscriptionAccountQuotaResetRunModel{}))
	ctx := context.Background()
	now := time.Now().UTC()
	account := &biz.SubscriptionAccount{Name: "reset-race", Platform: "codex", Status: 1, Group: "default", QuotaResetStrategy: "fixed", QuotaTimezone: "UTC"}
	start := account.FixedQuotaWindowStart(now, "daily")
	account.QuotaDailyWindowStart = start
	account.QuotaDailyUsedUSD = 0.75
	require.NoError(t, repo.CreateSubscriptionAccount(ctx, account))
	// The sweep read yesterday's row before a user charge advanced the window.
	run := &biz.SubscriptionAccountQuotaResetRun{AccountID: account.ID, Scope: "daily", WindowStart: start, Strategy: "fixed", Timezone: "UTC", ResetAt: now}
	require.NoError(t, repo.RecordQuotaResetAndReset(ctx, run))
	got, err := repo.FindSubscriptionAccountByID(ctx, account.ID)
	require.NoError(t, err)
	require.Equal(t, 0.75, got.QuotaDailyUsedUSD)
	require.Equal(t, start, got.QuotaDailyWindowStart)
}

func TestQuotaResetConcurrentAcrossDialects(t *testing.T) {
	for _, dialect := range []string{"sqlite", "mysql", "postgres"} {
		t.Run(dialect, func(t *testing.T) {
			db := testutil.RoutingContextDB(t, dialect)
			repo := &Repository{db: db}
			ctx := context.Background()
			start := time.Now().UTC().Truncate(24 * time.Hour).Unix()
			account := &biz.SubscriptionAccount{Name: "concurrent-reset", Platform: "codex", Status: 1, Group: "default", QuotaDailyWindowStart: start - 86400, QuotaDailyUsedUSD: 3}
			require.NoError(t, repo.CreateSubscriptionAccount(ctx, account))
			if dialect == "sqlite" {
				sqlDB, err := db.DB()
				require.NoError(t, err)
				sqlDB.SetMaxOpenConns(1)
			}
			run := &biz.SubscriptionAccountQuotaResetRun{AccountID: account.ID, Scope: "daily", WindowStart: start, Strategy: "fixed", Timezone: "UTC", ResetAt: time.Now()}
			results := make(chan error, 2)
			var wg sync.WaitGroup
			for range 2 {
				wg.Go(func() { results <- repo.RecordQuotaResetAndReset(ctx, run) })
			}
			wg.Wait()
			close(results)
			success := 0
			for err := range results {
				if err == nil {
					success++
				} else {
					require.ErrorIs(t, err, biz.ErrQuotaResetRunDuplicate)
				}
			}
			require.Equal(t, 1, success)
			var row subscriptionAccountModel
			require.NoError(t, db.First(&row, account.ID).Error)
			require.Zero(t, row.QuotaDailyUsedUSD)
			require.Equal(t, start, row.QuotaDailyWindowStart)
		})
	}
}

func TestManualRecoveryFilterCountsBeforePagination(t *testing.T) {
	repo := setupChannelTestDB(t)
	ctx := context.Background()
	for _, a := range []*biz.SubscriptionAccount{
		{Name: "normal", Platform: "codex", Status: 1},
		{Name: "manual-one", Platform: "codex", Status: 2, Metadata: `{"recovery_policy":"manual","unschedulable_reason":"401","unschedulable_since":1}`},
		{Name: "manual-two", Platform: "codex", Status: 2, Metadata: `{"recovery_policy": "manual", "unschedulable_reason":"403","unschedulable_since":1}`},
		{Name: "unrelated", Platform: "codex", Status: 2, Metadata: `{"note":"manual","recovery_policy":"auto"}`},
	} {
		require.NoError(t, repo.CreateSubscriptionAccount(ctx, a))
	}
	items, total, err := repo.ListSubscriptionAccountsByRecovery(ctx, 2, 1, "", "", 0, "", "manual")
	require.NoError(t, err)
	require.EqualValues(t, 2, total)
	require.Len(t, items, 1)
	require.Equal(t, "manual-two", items[0].Name)
}

func TestQuotaResetRollbackAndConcurrentRetry(t *testing.T) {
	repo := setupChannelTestDB(t)
	require.NoError(t, repo.db.AutoMigrate(&subscriptionAccountQuotaResetRunModel{}))
	require.NoError(t, repo.db.Exec("CREATE UNIQUE INDEX test_reset_window ON subscription_account_quota_reset_runs(subscription_account_id, scope, window_start)").Error)
	db, err := repo.db.DB()
	require.NoError(t, err)
	db.SetMaxOpenConns(1)
	ctx := context.Background()
	now := time.Now().UTC()
	account := &biz.SubscriptionAccount{Name: "reset-retry", Platform: "codex", Status: 1, Group: "default", QuotaResetStrategy: "fixed", QuotaTimezone: "UTC", QuotaDailyUsedUSD: 3}
	start := account.FixedQuotaWindowStart(now, "daily")
	account.QuotaDailyWindowStart = start - 86400
	require.NoError(t, repo.CreateSubscriptionAccount(ctx, account))
	run := &biz.SubscriptionAccountQuotaResetRun{AccountID: account.ID, Scope: "daily", WindowStart: start, Strategy: "fixed", Timezone: "UTC", ResetAt: now}
	require.NoError(t, repo.db.Callback().Update().Before("gorm:update").Register("test_reset_failure", func(tx *gorm.DB) {
		if tx.Statement.Table == "subscription_accounts" {
			tx.AddError(errors.New("injected reset failure"))
		}
	}))
	require.ErrorContains(t, repo.RecordQuotaResetAndReset(ctx, run), "injected reset failure")
	require.NoError(t, repo.db.Callback().Update().Remove("test_reset_failure"))
	var count int64
	require.NoError(t, repo.db.Model(&subscriptionAccountQuotaResetRunModel{}).Count(&count).Error)
	require.Zero(t, count)
	results := make(chan error, 2)
	var wg sync.WaitGroup
	for range 2 {
		wg.Go(func() { results <- repo.RecordQuotaResetAndReset(ctx, run) })
	}
	wg.Wait()
	close(results)
	success, duplicate := 0, 0
	for err := range results {
		if err == nil {
			success++
		} else {
			require.ErrorIs(t, err, biz.ErrQuotaResetRunDuplicate)
			duplicate++
		}
	}
	require.Equal(t, 1, success)
	require.Equal(t, 1, duplicate)
	got, err := repo.FindSubscriptionAccountByID(ctx, account.ID)
	require.NoError(t, err)
	require.Zero(t, got.QuotaDailyUsedUSD)
	require.Equal(t, start, got.QuotaDailyWindowStart)
}

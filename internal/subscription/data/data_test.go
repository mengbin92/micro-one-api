package data

import (
	"context"
	"sync"
	"testing"

	"micro-one-api/internal/subscription/biz"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
)

func setupSubscriptionTestDB(t *testing.T) *Repository {
	t.Helper()
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	require.NoError(t, err)

	require.NoError(t, db.Exec(`
		CREATE TABLE subscription_groups (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			name TEXT NOT NULL UNIQUE,
			display_name TEXT NOT NULL DEFAULT '',
			platform TEXT NOT NULL,
			subscription_type TEXT NOT NULL DEFAULT 'standard',
			daily_limit_usd REAL DEFAULT NULL,
			weekly_limit_usd REAL DEFAULT NULL,
			monthly_limit_usd REAL DEFAULT NULL,
			rate_multiplier REAL NOT NULL DEFAULT 1.0,
			status INTEGER NOT NULL DEFAULT 1,
			price_quota INTEGER NOT NULL DEFAULT 0,
			duration_days INTEGER NOT NULL DEFAULT 0,
			created_at INTEGER NOT NULL DEFAULT 0,
			updated_at INTEGER NOT NULL DEFAULT 0
		)
	`).Error)

	require.NoError(t, db.Exec(`
		CREATE TABLE subscription_plans (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			group_id INTEGER NOT NULL,
			name TEXT NOT NULL,
			description TEXT NOT NULL DEFAULT '',
			price_quota INTEGER NOT NULL DEFAULT 0,
			original_price INTEGER DEFAULT NULL,
			validity_days INTEGER NOT NULL DEFAULT 30,
			validity_unit TEXT NOT NULL DEFAULT 'day',
			features TEXT NOT NULL DEFAULT '',
			product_name TEXT NOT NULL DEFAULT '',
			for_sale INTEGER NOT NULL DEFAULT 1,
			sort_order INTEGER NOT NULL DEFAULT 0,
			created_at INTEGER NOT NULL DEFAULT 0,
			updated_at INTEGER NOT NULL DEFAULT 0
		)
	`).Error)

	require.NoError(t, db.Exec(`
		CREATE TABLE user_subscriptions (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			user_id INTEGER NOT NULL,
			group_id INTEGER NOT NULL,
			subscription_name TEXT NOT NULL DEFAULT '',
			status TEXT NOT NULL DEFAULT 'active',
			starts_at INTEGER NOT NULL DEFAULT 0,
			expires_at INTEGER NOT NULL DEFAULT 0,
			daily_usage_usd REAL NOT NULL DEFAULT 0,
			weekly_usage_usd REAL NOT NULL DEFAULT 0,
			monthly_usage_usd REAL NOT NULL DEFAULT 0,
			daily_window_start INTEGER NOT NULL DEFAULT 0,
			weekly_window_start INTEGER NOT NULL DEFAULT 0,
			monthly_window_start INTEGER NOT NULL DEFAULT 0,
			metadata TEXT,
			created_at INTEGER NOT NULL DEFAULT 0,
			updated_at INTEGER NOT NULL DEFAULT 0
		)
	`).Error)

	return &Repository{db: db}
}

func TestSubscriptionRepository_PlanCRUD(t *testing.T) {
	repo := setupSubscriptionTestDB(t)
	ctx := context.Background()

	group := &biz.SubscriptionGroup{Name: "pro", Platform: "openai", Status: biz.SubscriptionGroupStatusEnabled}
	require.NoError(t, repo.CreateGroup(ctx, group))

	plan := &biz.SubscriptionPlan{
		GroupID:      group.ID,
		Name:         "Monthly Pro",
		PriceQuota:   100,
		ValidityDays: 30,
		ValidityUnit: "day",
		ForSale:      true,
		SortOrder:    10,
	}
	require.NoError(t, repo.CreatePlan(ctx, plan))
	require.NotZero(t, plan.ID)

	got, err := repo.GetPlanByID(ctx, plan.ID)
	require.NoError(t, err)
	assert.Equal(t, "Monthly Pro", got.Name)
	require.NotNil(t, got.Group)
	assert.Equal(t, group.ID, got.Group.ID)

	plan.Name = "Monthly Pro Plus"
	plan.ForSale = false
	require.NoError(t, repo.UpdatePlan(ctx, plan))

	all, err := repo.ListPlans(ctx)
	require.NoError(t, err)
	require.Len(t, all, 1)
	assert.Equal(t, "Monthly Pro Plus", all[0].Name)

	forSale, err := repo.ListPlansForSale(ctx)
	require.NoError(t, err)
	assert.Empty(t, forSale)

	require.NoError(t, repo.DeletePlan(ctx, plan.ID))
	_, err = repo.GetPlanByID(ctx, plan.ID)
	assert.ErrorIs(t, err, biz.ErrSubscriptionPlanNotFound)
}

func TestSubscriptionRepository_GroupCRUD(t *testing.T) {
	repo := setupSubscriptionTestDB(t)
	ctx := context.Background()

	group := &biz.SubscriptionGroup{
		Name:        "pro",
		DisplayName: "Pro",
		Platform:    "openai",
		Status:      biz.SubscriptionGroupStatusEnabled,
	}
	require.NoError(t, repo.CreateGroup(ctx, group))
	require.NotZero(t, group.ID)

	got, err := repo.GetGroupByID(ctx, group.ID)
	require.NoError(t, err)
	assert.Equal(t, "pro", got.Name)

	group.DisplayName = "Pro Plus"
	require.NoError(t, repo.UpdateGroup(ctx, group))

	got, err = repo.GetGroupByName(ctx, "pro")
	require.NoError(t, err)
	assert.Equal(t, "Pro Plus", got.DisplayName)

	list, err := repo.ListGroups(ctx)
	require.NoError(t, err)
	require.Len(t, list, 1)

	require.NoError(t, repo.DeleteGroup(ctx, group.ID))
	_, err = repo.GetGroupByID(ctx, group.ID)
	assert.ErrorIs(t, err, biz.ErrSubscriptionGroupNotFound)
}

func TestSubscriptionRepository_SubscriptionCRUD(t *testing.T) {
	repo := setupSubscriptionTestDB(t)
	ctx := context.Background()

	group := &biz.SubscriptionGroup{Name: "pro", Platform: "openai", Status: biz.SubscriptionGroupStatusEnabled}
	require.NoError(t, repo.CreateGroup(ctx, group))

	sub := &biz.UserSubscription{
		UserID:             1001,
		GroupID:            group.ID,
		SubscriptionName:   "alice-pro",
		Status:             biz.SubscriptionStatusActive,
		StartsAt:           10,
		ExpiresAt:          20,
		DailyWindowStart:   10,
		WeeklyWindowStart:  10,
		MonthlyWindowStart: 10,
	}
	require.NoError(t, repo.CreateSubscription(ctx, sub))
	require.NotZero(t, sub.ID)

	got, err := repo.GetSubscriptionByID(ctx, sub.ID)
	require.NoError(t, err)
	assert.Equal(t, int64(1001), got.UserID)

	list, err := repo.ListSubscriptionsByUser(ctx, 1001)
	require.NoError(t, err)
	require.Len(t, list, 1)

	all, err := repo.ListAllSubscriptions(ctx)
	require.NoError(t, err)
	require.Len(t, all, 1)
	assert.Equal(t, sub.ID, all[0].ID)

	active, err := repo.GetActiveSubscriptionByUser(ctx, 1001)
	require.NoError(t, err)
	assert.Equal(t, sub.ID, active.ID)

	sub.Status = biz.SubscriptionStatusRevoked
	require.NoError(t, repo.UpdateSubscription(ctx, sub))
	got, err = repo.GetSubscriptionByID(ctx, sub.ID)
	require.NoError(t, err)
	assert.Equal(t, biz.SubscriptionStatusRevoked, got.Status)

	require.NoError(t, repo.DeleteSubscription(ctx, sub.ID))
	_, err = repo.GetSubscriptionByID(ctx, sub.ID)
	assert.ErrorIs(t, err, biz.ErrSubscriptionNotFound)
}

// TestSubscriptionRepository_AddUsageConcurrent verifies AddUsage does not lose
// increments under concurrency (regression for the read-modify-write race).
func TestSubscriptionRepository_AddUsageConcurrent(t *testing.T) {
	repo := NewMemoryRepositoryForTest()
	ctx := context.Background()

	sub := &biz.UserSubscription{
		UserID:             2002,
		GroupID:            1,
		Status:             biz.SubscriptionStatusActive,
		StartsAt:           1,
		ExpiresAt:          1 << 62, // far future so windows never roll during the test
		DailyWindowStart:   1,
		WeeklyWindowStart:  1,
		MonthlyWindowStart: 1,
	}
	require.NoError(t, repo.CreateSubscription(ctx, sub))

	const goroutines = 50
	const perGoroutine = 20
	var wg sync.WaitGroup
	wg.Add(goroutines)
	for i := 0; i < goroutines; i++ {
		go func() {
			defer wg.Done()
			for j := 0; j < perGoroutine; j++ {
				if err := repo.AddUsage(ctx, 2002, 0.01, 100); err != nil {
					t.Errorf("AddUsage() error = %v", err)
					return
				}
			}
		}()
	}
	wg.Wait()

	got, err := repo.GetActiveSubscriptionByUser(ctx, 2002)
	require.NoError(t, err)
	want := 0.01 * float64(goroutines*perGoroutine)
	assert.InDelta(t, want, got.DailyUsageUSD, 1e-9)
	assert.InDelta(t, want, got.WeeklyUsageUSD, 1e-9)
	assert.InDelta(t, want, got.MonthlyUsageUSD, 1e-9)
}

package data

import (
	"context"
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

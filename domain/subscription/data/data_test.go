package data

import (
	"context"
	"sync"
	"testing"
	"time"

	"micro-one-api/domain/subscription/biz"

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
			renewal_strategy TEXT NOT NULL DEFAULT '',
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
		ExpiresAt:          1 << 62, // far future so the domain-C1 expires_at>now guard does not filter it out
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

// TestSubscriptionRepository_ExpiredActiveFiltered (domain-C1) verifies that a
// subscription whose status is still 'active' but whose expires_at has already
// passed is NOT returned by the active-subscription read paths. This is the
// defence-in-depth guard that keeps quota correct even before the hourly
// SubscriptionExpiryChecker has flipped the status to expired.
func TestSubscriptionRepository_ExpiredActiveFiltered(t *testing.T) {
	repo := setupSubscriptionTestDB(t)
	ctx := context.Background()

	group := &biz.SubscriptionGroup{Name: "pro-expired", Platform: "openai", Status: biz.SubscriptionGroupStatusEnabled}
	require.NoError(t, repo.CreateGroup(ctx, group))

	// status=active but already expired in the past.
	expired := &biz.UserSubscription{
		UserID:           7777,
		GroupID:          group.ID,
		SubscriptionName: "stale",
		Status:           biz.SubscriptionStatusActive,
		StartsAt:         10,
		ExpiresAt:        1, // already expired
	}
	require.NoError(t, repo.CreateSubscription(ctx, expired))

	// The row exists (by ID) but must not be considered active.
	_, err := repo.GetActiveSubscriptionByUser(ctx, 7777)
	assert.ErrorIs(t, err, biz.ErrSubscriptionNotFound)

	// The in-tx locked read must apply the same guard so the payment assigner
	// cannot grant onto a stale row either.
	_, err = repo.GetActiveSubscriptionByUserInTx(ctx, &gormTx{db: repo.db}, 7777)
	assert.ErrorIs(t, err, biz.ErrSubscriptionNotFound)
}

// TestSubscriptionRepository_UpdateFieldsDoesNotClobberUsage (domain-H1)
// verifies that a selective update (e.g. the expiry checker flipping status,
// or an Extend changing expires_at) does NOT overwrite concurrent AddUsage
// increments on the usage/window columns. The previous full-row
// updateSubscriptionWithTx wrote daily/weekly/monthly_usage_usd from a
// potentially stale read snapshot, silently rolling back billed usage.
func TestSubscriptionRepository_UpdateFieldsDoesNotClobberUsage(t *testing.T) {
	repo := setupSubscriptionTestDB(t)
	ctx := context.Background()

	group := &biz.SubscriptionGroup{Name: "pro-h1", Platform: "openai", Status: biz.SubscriptionGroupStatusEnabled}
	require.NoError(t, repo.CreateGroup(ctx, group))

	sub := &biz.UserSubscription{
		UserID:           8888,
		GroupID:          group.ID,
		SubscriptionName: "h1-pro",
		Status:           biz.SubscriptionStatusActive,
		StartsAt:         10,
		ExpiresAt:        1 << 62,
	}
	require.NoError(t, repo.CreateSubscription(ctx, sub))

	// Simulate relay usage being billed.
	require.NoError(t, repo.AddUsage(ctx, 8888, 2.5, 100))

	// Capture the live usage that a concurrent AddUsage would produce.
	before, err := repo.GetSubscriptionByID(ctx, sub.ID)
	require.NoError(t, err)
	require.Equal(t, 2.5, before.DailyUsageUSD)

	// Now a stale snapshot (e.g. read before AddUsage committed) tries to flip
	// the status to expired via the SELECTIVE write path. Its usage fields are
	// still zero (stale read) — the narrow write must NOT propagate them.
	stale := *before
	stale.DailyUsageUSD = 0
	stale.WeeklyUsageUSD = 0
	stale.MonthlyUsageUSD = 0
	stale.Status = biz.SubscriptionStatusExpired
	require.NoError(t, repo.UpdateSubscriptionFields(ctx, &stale, []biz.SubscriptionField{biz.SubscriptionFieldStatus}))

	got, err := repo.GetSubscriptionByID(ctx, sub.ID)
	require.NoError(t, err)
	// Status was written...
	assert.Equal(t, biz.SubscriptionStatusExpired, got.Status)
	// ...but the usage increments are preserved (the bug would zero them).
	assert.Equal(t, 2.5, got.DailyUsageUSD, "narrow status update must not clobber daily usage")
	assert.Equal(t, 2.5, got.WeeklyUsageUSD, "narrow status update must not clobber weekly usage")
	assert.Equal(t, 2.5, got.MonthlyUsageUSD, "narrow status update must not clobber monthly usage")
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

// TestSubscriptionRepository_RenewalStrategyRoundTrip (M2) verifies the
// renewal_strategy column persists through create and selective update, and
// that the expiry guard still filters an expired row regardless of strategy.
func TestSubscriptionRepository_RenewalStrategyRoundTrip(t *testing.T) {
	repo := setupSubscriptionTestDB(t)
	ctx := context.Background()

	group := &biz.SubscriptionGroup{Name: "pro", Platform: "openai", Status: biz.SubscriptionGroupStatusEnabled}
	require.NoError(t, repo.CreateGroup(ctx, group))

	sub := &biz.UserSubscription{
		UserID:           1002,
		GroupID:          group.ID,
		SubscriptionName: "alice-pro",
		Status:           biz.SubscriptionStatusActive,
		StartsAt:         10,
		ExpiresAt:        1 << 62, // far future so the domain-C1 guard keeps it active
		RenewalStrategy:  biz.RenewalStrategyNew,
	}
	require.NoError(t, repo.CreateSubscription(ctx, sub))
	require.NotZero(t, sub.ID)

	got, err := repo.GetSubscriptionByID(ctx, sub.ID)
	require.NoError(t, err)
	assert.Equal(t, biz.RenewalStrategyNew, got.RenewalStrategy, "create must persist renewal_strategy")

	// Selective update flips it to extend without touching other columns.
	got.RenewalStrategy = biz.RenewalStrategyExtend
	require.NoError(t, repo.UpdateSubscriptionFields(ctx, got, []biz.SubscriptionField{biz.SubscriptionFieldRenewalStrategy}))
	active, err := repo.GetActiveSubscriptionByUser(ctx, 1002)
	require.NoError(t, err)
	assert.Equal(t, biz.RenewalStrategyExtend, active.RenewalStrategy, "selective update must persist renewal_strategy")
	assert.Equal(t, int64(10), active.StartsAt, "other columns must be untouched by the selective update")

	// An expired active row is filtered out by the domain-C1 guard regardless
	// of its renewal strategy.
	got.ExpiresAt = time.Now().Unix() - 100
	require.NoError(t, repo.UpdateSubscriptionFields(ctx, got, []biz.SubscriptionField{biz.SubscriptionFieldExpiresAt}))
	_, err = repo.GetActiveSubscriptionByUser(ctx, 1002)
	assert.ErrorIs(t, err, biz.ErrSubscriptionNotFound, "expired row must not be active")
}

// setupSubscriptionTestDBWithUniqueIndex is like setupSubscriptionTestDB but
// also creates the H10 partial unique index
// (uniq_user_subs_active_user_id) that enforces "at most one active
// subscription per user". Use this variant when a test needs to verify the
// database-level constraint itself, not just the application-layer mapping.
func setupSubscriptionTestDBWithUniqueIndex(t *testing.T) *Repository {
	t.Helper()
	repo := setupSubscriptionTestDB(t)
	// Mirror migrations/sqlite/001_enforce_single_active_subscription.sql.
	require.NoError(t, repo.db.Exec(`CREATE UNIQUE INDEX IF NOT EXISTS uniq_user_subs_active_user_id ON user_subscriptions (user_id) WHERE status = 'active'`).Error)
	return repo
}

// TestSubscriptionRepository_H10UniqueIndexRejectsSecondActive verifies the
// H10 partial unique index prevents two concurrent active subscriptions for
// the same user at the DB level, and that the violation is mapped to the
// sentinel ErrSubscriptionAlreadyAssigned (review H10 regression).
func TestSubscriptionRepository_H10UniqueIndexRejectsSecondActive(t *testing.T) {
	repo := setupSubscriptionTestDBWithUniqueIndex(t)
	ctx := context.Background()

	group := &biz.SubscriptionGroup{Name: "pro", Platform: "openai", Status: biz.SubscriptionGroupStatusEnabled}
	require.NoError(t, repo.CreateGroup(ctx, group))

	first := &biz.UserSubscription{
		UserID:    5001,
		GroupID:   group.ID,
		Status:    biz.SubscriptionStatusActive,
		StartsAt:  10,
		ExpiresAt: 1 << 62,
	}
	require.NoError(t, repo.CreateSubscription(ctx, first))
	require.NotZero(t, first.ID)

	// A second active subscription for the same user must be rejected by the
	// DB unique index and mapped to the sentinel error.
	second := &biz.UserSubscription{
		UserID:    5001,
		GroupID:   group.ID,
		Status:    biz.SubscriptionStatusActive,
		StartsAt:  20,
		ExpiresAt: 1 << 62,
	}
	err := repo.CreateSubscription(ctx, second)
	assert.ErrorIs(t, err, biz.ErrSubscriptionAlreadyAssigned,
		"concurrent active create must hit the unique index and map to ErrSubscriptionAlreadyAssigned")
}

// TestSubscriptionRepository_H10UniqueIndexAllowsMultipleNonActive verifies
// the partial index (WHERE status='active') does not block multiple
// expired/revoked rows for the same user — only concurrent actives are
// forbidden.
func TestSubscriptionRepository_H10UniqueIndexAllowsMultipleNonActive(t *testing.T) {
	repo := setupSubscriptionTestDBWithUniqueIndex(t)
	ctx := context.Background()

	group := &biz.SubscriptionGroup{Name: "pro2", Platform: "openai", Status: biz.SubscriptionGroupStatusEnabled}
	require.NoError(t, repo.CreateGroup(ctx, group))

	for _, status := range []biz.SubscriptionStatus{biz.SubscriptionStatusExpired, biz.SubscriptionStatusRevoked} {
		s := &biz.UserSubscription{
			UserID:    6001,
			GroupID:   group.ID,
			Status:    status,
			StartsAt:  10,
			ExpiresAt: 100,
		}
		require.NoError(t, repo.CreateSubscription(ctx, s), "non-active status=%s must not violate the partial index", status)
	}

	// An active subscription is still allowed when only non-active rows exist.
	active := &biz.UserSubscription{
		UserID:    6001,
		GroupID:   group.ID,
		Status:    biz.SubscriptionStatusActive,
		StartsAt:  200,
		ExpiresAt: 1 << 62,
	}
	require.NoError(t, repo.CreateSubscription(ctx, active), "active create after only non-active rows must succeed")
}

// TestSubscriptionRepository_H10UniqueIndexDifferentUsers verifies the index
// scopes by user: two different users can each have one active subscription.
func TestSubscriptionRepository_H10UniqueIndexDifferentUsers(t *testing.T) {
	repo := setupSubscriptionTestDBWithUniqueIndex(t)
	ctx := context.Background()

	group := &biz.SubscriptionGroup{Name: "pro3", Platform: "openai", Status: biz.SubscriptionGroupStatusEnabled}
	require.NoError(t, repo.CreateGroup(ctx, group))

	for _, uid := range []int64{7001, 7002, 7003} {
		s := &biz.UserSubscription{
			UserID:    uid,
			GroupID:   group.ID,
			Status:    biz.SubscriptionStatusActive,
			StartsAt:  10,
			ExpiresAt: 1 << 62,
		}
		require.NoError(t, repo.CreateSubscription(ctx, s), "user %d should get an active subscription without conflict", uid)
	}
}

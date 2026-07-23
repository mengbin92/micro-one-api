package data

import (
	"context"
	"math"
	"sync"
	"testing"
	"time"

	"micro-one-api/app/channel/internal/biz"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
)

// setupChannelTestDB creates an in-memory sqlite DB matching the
// `channels` and `abilities` schemas relevant to repo behaviour.
// Only the columns the repo reads/writes are modelled here.
func setupChannelTestDB(t *testing.T) *Repository {
	t.Helper()
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	require.NoError(t, err)
	sqlDB, err := db.DB()
	require.NoError(t, err)
	sqlDB.SetMaxOpenConns(1)

	require.NoError(t, db.Exec(`
		CREATE TABLE channels (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			type INTEGER DEFAULT 0,
			`+"`key`"+` TEXT,
			status INTEGER DEFAULT 0,
			name TEXT,
			weight INTEGER DEFAULT 0,
			created_time INTEGER DEFAULT 0,
			test_time INTEGER DEFAULT 0,
			response_time INTEGER DEFAULT 0,
			base_url TEXT,
			balance REAL DEFAULT 0,
			balance_updated_time INTEGER DEFAULT 0,
			balance_refresh_last_error TEXT,
			balance_refresh_last_success_time INTEGER DEFAULT 0,
			consecutive_balance_refresh_failures INTEGER DEFAULT 0,
			health_status TEXT DEFAULT 'healthy',
			health_last_error TEXT,
			health_last_success_time INTEGER DEFAULT 0,
			health_last_failure_time INTEGER DEFAULT 0,
			health_consecutive_failures INTEGER DEFAULT 0,
			circuit_opened_until INTEGER DEFAULT 0,
			models TEXT,
			`+"`group`"+` TEXT DEFAULT 'default',
			used_quota INTEGER DEFAULT 0,
			model_mapping TEXT DEFAULT '',
			priority INTEGER DEFAULT 0,
			config TEXT DEFAULT '',
			system_prompt TEXT
		)
	`).Error)

	require.NoError(t, db.Exec(`
		CREATE TABLE abilities (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			`+"`group`"+` TEXT NOT NULL DEFAULT 'default',
			model TEXT NOT NULL,
			channel_id INTEGER NOT NULL,
			enabled INTEGER DEFAULT 1,
			priority INTEGER DEFAULT 0
		)
	`).Error)

	require.NoError(t, db.Exec(`
		CREATE TABLE subscription_accounts (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			name TEXT NOT NULL DEFAULT '',
			platform TEXT NOT NULL,
			account_type TEXT NOT NULL DEFAULT 'oauth',
			status INTEGER DEFAULT 1,
			`+"`group`"+` TEXT DEFAULT 'default',
			models TEXT,
			priority INTEGER DEFAULT 0,
			base_url TEXT,
			access_token TEXT,
			refresh_token TEXT,
			expires_at INTEGER DEFAULT 0,
			account_id TEXT DEFAULT '',
			fingerprint TEXT,
			metadata TEXT,
			created_at INTEGER DEFAULT 0,
			updated_at INTEGER DEFAULT 0,
			last_used_at INTEGER DEFAULT 0,
			rate_limited_until INTEGER DEFAULT 0,
			quota_used_percent REAL DEFAULT 0,
			quota_reset_at INTEGER DEFAULT 0,
			concurrency INTEGER DEFAULT 1,
			quota_limit_usd REAL DEFAULT 0,
			quota_used_usd REAL DEFAULT 0,
			quota_5h_limit_usd REAL DEFAULT 0,
			quota_5h_used_usd REAL DEFAULT 0,
			quota_5h_window_start INTEGER DEFAULT 0,
			quota_daily_limit_usd REAL DEFAULT 0,
			quota_daily_used_usd REAL DEFAULT 0,
			quota_daily_window_start INTEGER DEFAULT 0,
			quota_weekly_limit_usd REAL DEFAULT 0,
			quota_weekly_used_usd REAL DEFAULT 0,
			quota_weekly_window_start INTEGER DEFAULT 0,
			rate_multiplier REAL DEFAULT 1,
			rpm_limit INTEGER DEFAULT 0,
			session_window_limit_usd REAL DEFAULT 0,
			quota_reset_strategy TEXT DEFAULT 'rolling',
			quota_timezone TEXT DEFAULT 'UTC',
			model_mapping TEXT DEFAULT ''
		)
	`).Error)

	require.NoError(t, db.Exec(`
		CREATE TABLE subscription_account_abilities (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			`+"`group`"+` TEXT NOT NULL DEFAULT 'default',
			model TEXT NOT NULL,
			platform TEXT NOT NULL,
			account_id INTEGER NOT NULL,
			enabled INTEGER DEFAULT 1,
			priority INTEGER DEFAULT 0
		)
	`).Error)

	require.NoError(t, db.Exec(`
		CREATE TABLE subscription_account_quota_events (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			reservation_id TEXT NOT NULL,
			subscription_account_id INTEGER NOT NULL,
			cost_source TEXT NOT NULL DEFAULT 'billing_commit',
			cost_usd REAL NOT NULL DEFAULT 0,
			charged_usd REAL NOT NULL DEFAULT 0,
			rate_multiplier REAL NOT NULL DEFAULT 1,
			occurred_at INTEGER NOT NULL DEFAULT 0,
			created_at INTEGER NOT NULL DEFAULT 0
		)
	`).Error)
	require.NoError(t, db.Exec(`
		CREATE UNIQUE INDEX idx_subscription_account_quota_events_dedupe
		ON subscription_account_quota_events(reservation_id, subscription_account_id, cost_source)
	`).Error)

	require.NoError(t, db.Exec(`
		CREATE TABLE IF NOT EXISTS account_quota_snapshots (
			account_id INTEGER PRIMARY KEY,
			primary_used_percent REAL,
			primary_reset_after_seconds INTEGER,
			primary_window_minutes INTEGER,
			secondary_used_percent REAL,
			secondary_reset_after_seconds INTEGER,
			secondary_window_minutes INTEGER,
			primary_over_secondary_percent REAL,
			updated_at DATETIME,
			snapshot_paused INTEGER DEFAULT 0
		)
	`).Error)

	return &Repository{db: db}
}

// loadAbilities returns all rows in abilities for diagnostic + assertion use.
func loadAbilities(t *testing.T, repo *Repository, channelID int64) []abilityModel {
	t.Helper()
	var rows []abilityModel
	require.NoError(t, repo.db.Where("channel_id = ?", channelID).Order("`group` ASC, model ASC").Find(&rows).Error)
	return rows
}

func loadSubscriptionAbilities(t *testing.T, repo *Repository, accountID int64) []subscriptionAccountAbilityModel {
	t.Helper()
	var rows []subscriptionAccountAbilityModel
	require.NoError(t, repo.db.Where("account_id = ?", accountID).Order("`group` ASC, model ASC").Find(&rows).Error)
	return rows
}

func TestCreateChannel_PopulatesAbilities(t *testing.T) {
	repo := setupChannelTestDB(t)
	ctx := context.Background()

	ch := &biz.Channel{
		Name:     "test-anthropic",
		Type:     2,
		BaseURL:  "https://api.anthropic.com",
		Key:      "sk-test",
		Status:   biz.ChannelStatusEnabled,
		Group:    "default,premium",
		Models:   []string{"claude-opus-4-7", "claude-sonnet-4-6"},
		Priority: 100,
	}

	require.NoError(t, repo.CreateChannel(ctx, ch))
	require.NotZero(t, ch.ID, "channel ID should be populated by INSERT")

	rows := loadAbilities(t, repo, ch.ID)
	assert.Len(t, rows, 4, "expected 2 groups × 2 models = 4 ability rows")

	// Verify row content: enabled mirrors channel.Status; priority mirrors channel.Priority.
	wantPairs := map[string]bool{
		"default:claude-opus-4-7":   false,
		"default:claude-sonnet-4-6": false,
		"premium:claude-opus-4-7":   false,
		"premium:claude-sonnet-4-6": false,
	}
	for _, r := range rows {
		key := r.Group + ":" + r.Model
		_, expected := wantPairs[key]
		require.True(t, expected, "unexpected ability row %s", key)
		wantPairs[key] = true
		assert.True(t, r.Enabled, "enabled should be true for enabled channel")
		require.NotNil(t, r.Priority)
		assert.EqualValues(t, 100, *r.Priority)
		assert.Equal(t, ch.ID, r.ChannelID)
	}
	for k, seen := range wantPairs {
		assert.True(t, seen, "ability row missing: %s", k)
	}
}

func TestCreateChannel_DisabledChannel_AbilitiesDisabled(t *testing.T) {
	repo := setupChannelTestDB(t)
	ctx := context.Background()

	ch := &biz.Channel{
		Name:   "disabled",
		Group:  "default",
		Models: []string{"gpt-4o"},
		Status: 2, // anything other than ChannelStatusEnabled
	}
	require.NoError(t, repo.CreateChannel(ctx, ch))

	rows := loadAbilities(t, repo, ch.ID)
	require.Len(t, rows, 1)
	assert.False(t, rows[0].Enabled, "ability.enabled should be false when channel.status != 1")
}

func TestCreateChannel_SkipsEmptyGroupOrModel(t *testing.T) {
	repo := setupChannelTestDB(t)
	ctx := context.Background()

	ch := &biz.Channel{
		Name:   "with-empties",
		Group:  "default,,premium", // empty group between commas
		Models: []string{"gpt-4o", "", "claude-opus-4-7"},
		Status: biz.ChannelStatusEnabled,
	}
	require.NoError(t, repo.CreateChannel(ctx, ch))

	rows := loadAbilities(t, repo, ch.ID)
	// Expect 2 groups × 2 models = 4 (empty group + empty model skipped)
	assert.Len(t, rows, 4)
	for _, r := range rows {
		assert.NotEmpty(t, r.Group)
		assert.NotEmpty(t, r.Model)
	}
}

func TestUpdateChannel_RewritesAbilities(t *testing.T) {
	repo := setupChannelTestDB(t)
	ctx := context.Background()

	ch := &biz.Channel{
		Name:     "drift",
		Group:    "default",
		Models:   []string{"gpt-3.5-turbo", "gpt-4"},
		Status:   biz.ChannelStatusEnabled,
		Priority: 10,
	}
	require.NoError(t, repo.CreateChannel(ctx, ch))
	require.Len(t, loadAbilities(t, repo, ch.ID), 2)

	// Change models entirely and priority.
	ch.Models = []string{"gpt-4o"}
	ch.Priority = 50
	require.NoError(t, repo.UpdateChannel(ctx, ch))

	rows := loadAbilities(t, repo, ch.ID)
	require.Len(t, rows, 1, "expected old abilities replaced by 1 new row")
	assert.Equal(t, "gpt-4o", rows[0].Model)
	require.NotNil(t, rows[0].Priority)
	assert.EqualValues(t, 50, *rows[0].Priority)
}

func TestDeleteChannel_RemovesAbilities(t *testing.T) {
	repo := setupChannelTestDB(t)
	ctx := context.Background()

	ch := &biz.Channel{
		Name:   "tbd",
		Group:  "default",
		Models: []string{"gpt-4o"},
		Status: biz.ChannelStatusEnabled,
	}
	require.NoError(t, repo.CreateChannel(ctx, ch))
	require.Len(t, loadAbilities(t, repo, ch.ID), 1)

	require.NoError(t, repo.DeleteChannel(ctx, ch.ID))
	assert.Empty(t, loadAbilities(t, repo, ch.ID))

	// channels row also gone
	var count int64
	require.NoError(t, repo.db.Table("channels").Where("id = ?", ch.ID).Count(&count).Error)
	assert.EqualValues(t, 0, count)
}

func TestChangeStatus_UpdatesAbilitiesEnabled(t *testing.T) {
	repo := setupChannelTestDB(t)
	ctx := context.Background()

	ch := &biz.Channel{
		Name:   "toggleable",
		Group:  "default",
		Models: []string{"gpt-4o"},
		Status: biz.ChannelStatusEnabled,
	}
	require.NoError(t, repo.CreateChannel(ctx, ch))
	rows := loadAbilities(t, repo, ch.ID)
	require.True(t, rows[0].Enabled)

	// Disable the channel.
	require.NoError(t, repo.ChangeStatus(ctx, ch.ID, 2))

	rows = loadAbilities(t, repo, ch.ID)
	require.Len(t, rows, 1)
	assert.False(t, rows[0].Enabled, "ability.enabled should be false after disabling channel")

	// Re-enable.
	require.NoError(t, repo.ChangeStatus(ctx, ch.ID, biz.ChannelStatusEnabled))

	rows = loadAbilities(t, repo, ch.ID)
	assert.True(t, rows[0].Enabled, "ability.enabled should be true after re-enabling channel")
}

func TestCreateSubscriptionAccount_PopulatesAbilities(t *testing.T) {
	repo := setupChannelTestDB(t)
	ctx := context.Background()

	account := &biz.SubscriptionAccount{
		Name:        "codex",
		Platform:    "codex",
		AccountType: "oauth",
		Status:      biz.ChannelStatusEnabled,
		Group:       "default,premium",
		Models:      []string{"gpt-5", "gpt-5-codex"},
		Priority:    30,
		AccountID:   "acc_1",
	}

	require.NoError(t, repo.CreateSubscriptionAccount(ctx, account))
	require.NotZero(t, account.ID)

	rows := loadSubscriptionAbilities(t, repo, account.ID)
	assert.Len(t, rows, 4)
	for _, r := range rows {
		assert.True(t, r.Enabled)
		require.NotNil(t, r.Priority)
		assert.EqualValues(t, 30, *r.Priority)
		assert.Equal(t, "codex", r.Platform)
	}
}

func TestSelectSubscriptionAccount_ByPriority(t *testing.T) {
	repo := setupChannelTestDB(t)
	ctx := context.Background()

	acc1 := &biz.SubscriptionAccount{
		Name:      "low",
		Platform:  "codex",
		Status:    biz.ChannelStatusEnabled,
		Group:     "default",
		Models:    []string{"gpt-5"},
		Priority:  1,
		AccountID: "acc_1",
	}
	acc2 := &biz.SubscriptionAccount{
		Name:      "high",
		Platform:  "codex",
		Status:    biz.ChannelStatusEnabled,
		Group:     "default",
		Models:    []string{"gpt-5"},
		Priority:  9,
		AccountID: "acc_2",
	}
	require.NoError(t, repo.CreateSubscriptionAccount(ctx, acc1))
	require.NoError(t, repo.CreateSubscriptionAccount(ctx, acc2))

	got, err := biz.NewChannelUsecase(repo, nil).SelectSubscriptionAccount(ctx, "default", "gpt-5", "codex", false)
	require.NoError(t, err)
	assert.Equal(t, acc2.ID, got.ID)
}

func TestSubscriptionAccountQuotaUsage_RecordAndReset(t *testing.T) {
	repo := setupChannelTestDB(t)
	ctx := context.Background()
	account := &biz.SubscriptionAccount{
		Name:                   "quota-usage",
		Platform:               "codex",
		Status:                 biz.ChannelStatusEnabled,
		Group:                  "default",
		Models:                 []string{"gpt-5"},
		AccountID:              "acc_1",
		RateMultiplier:         2,
		Quota5hUsedUSD:         0.25,
		Quota5hWindowStart:     time.Unix(1000, 0).Unix(),
		QuotaDailyUsedUSD:      0.75,
		QuotaDailyWindowStart:  time.Unix(1000, 0).Unix(),
		QuotaWeeklyWindowStart: time.Unix(1000, 0).Unix(),
	}
	require.NoError(t, repo.CreateSubscriptionAccount(ctx, account))

	require.NoError(t, repo.RecordSubscriptionAccountQuotaUsage(ctx, biz.SubscriptionAccountQuotaUsage{
		AccountID:  account.ID,
		CostUSD:    0.5,
		OccurredAt: time.Unix(1100, 0),
	}))
	stored, err := repo.FindSubscriptionAccountByID(ctx, account.ID)
	require.NoError(t, err)
	assert.InDelta(t, 1.0, stored.QuotaUsedUSD, 0.000001)
	assert.InDelta(t, 1.25, stored.Quota5hUsedUSD, 0.000001)
	assert.EqualValues(t, 1000, stored.Quota5hWindowStart)
	assert.InDelta(t, 1.75, stored.QuotaDailyUsedUSD, 0.000001)
	assert.EqualValues(t, 1000, stored.QuotaDailyWindowStart)
	assert.InDelta(t, 1.0, stored.QuotaWeeklyUsedUSD, 0.000001)

	require.NoError(t, repo.RecordSubscriptionAccountQuotaUsage(ctx, biz.SubscriptionAccountQuotaUsage{
		AccountID:  account.ID,
		CostUSD:    0.25,
		OccurredAt: time.Unix(1000+25*60*60, 0),
	}))
	stored, err = repo.FindSubscriptionAccountByID(ctx, account.ID)
	require.NoError(t, err)
	assert.InDelta(t, 1.5, stored.QuotaUsedUSD, 0.000001)
	assert.InDelta(t, 0.5, stored.Quota5hUsedUSD, 0.000001)
	assert.EqualValues(t, 1000+25*60*60, stored.Quota5hWindowStart)
	assert.InDelta(t, 0.5, stored.QuotaDailyUsedUSD, 0.000001)
	assert.EqualValues(t, 1000+25*60*60, stored.QuotaDailyWindowStart)

	require.NoError(t, repo.ResetSubscriptionAccountQuota(ctx, account.ID, "daily"))
	stored, err = repo.FindSubscriptionAccountByID(ctx, account.ID)
	require.NoError(t, err)
	assert.InDelta(t, 1.5, stored.QuotaUsedUSD, 0.000001)
	assert.InDelta(t, 0.5, stored.Quota5hUsedUSD, 0.000001)
	assert.Zero(t, stored.QuotaDailyUsedUSD)
	assert.Zero(t, stored.QuotaDailyWindowStart)

	require.NoError(t, repo.ResetSubscriptionAccountQuota(ctx, account.ID, "5h"))
	stored, err = repo.FindSubscriptionAccountByID(ctx, account.ID)
	require.NoError(t, err)
	assert.Zero(t, stored.Quota5hUsedUSD)
	assert.Zero(t, stored.Quota5hWindowStart)
}

func TestSubscriptionAccountQuotaUsage_FixedResetStrategyUsesTimezoneBoundaries(t *testing.T) {
	repo := setupChannelTestDB(t)
	ctx := context.Background()
	loc, err := time.LoadLocation("Asia/Shanghai")
	require.NoError(t, err)
	account := &biz.SubscriptionAccount{
		Name:                   "fixed-quota-usage",
		Platform:               "codex",
		Status:                 biz.ChannelStatusEnabled,
		Group:                  "default",
		Models:                 []string{"gpt-5"},
		AccountID:              "acc_1",
		QuotaDailyUsedUSD:      0.75,
		QuotaDailyWindowStart:  time.Date(2026, 7, 7, 23, 0, 0, 0, loc).Unix(),
		QuotaWeeklyUsedUSD:     1.25,
		QuotaWeeklyWindowStart: time.Date(2026, 7, 6, 0, 0, 0, 0, loc).Unix(),
		QuotaResetStrategy:     biz.QuotaResetStrategyFixed,
		QuotaTimezone:          "Asia/Shanghai",
	}
	require.NoError(t, repo.CreateSubscriptionAccount(ctx, account))

	occurredAt := time.Date(2026, 7, 8, 1, 0, 0, 0, loc)
	require.NoError(t, repo.RecordSubscriptionAccountQuotaUsage(ctx, biz.SubscriptionAccountQuotaUsage{
		AccountID:  account.ID,
		CostUSD:    0.5,
		OccurredAt: occurredAt,
	}))
	stored, err := repo.FindSubscriptionAccountByID(ctx, account.ID)
	require.NoError(t, err)
	assert.InDelta(t, 0.5, stored.QuotaDailyUsedUSD, 0.000001)
	assert.EqualValues(t, time.Date(2026, 7, 8, 0, 0, 0, 0, loc).Unix(), stored.QuotaDailyWindowStart)
	assert.InDelta(t, 1.75, stored.QuotaWeeklyUsedUSD, 0.000001)
	assert.EqualValues(t, time.Date(2026, 7, 6, 0, 0, 0, 0, loc).Unix(), stored.QuotaWeeklyWindowStart)
}

func TestSubscriptionAccountQuotaUsage_IdempotentByReservation(t *testing.T) {
	repo := setupChannelTestDB(t)
	ctx := context.Background()
	account := &biz.SubscriptionAccount{
		Name:           "quota-event",
		Platform:       "codex",
		Status:         biz.ChannelStatusEnabled,
		Group:          "default",
		Models:         []string{"gpt-5"},
		AccountID:      "acc_1",
		RateMultiplier: 2,
	}
	require.NoError(t, repo.CreateSubscriptionAccount(ctx, account))

	usage := biz.SubscriptionAccountQuotaUsage{
		AccountID:     account.ID,
		ReservationID: "reservation-1",
		CostSource:    "billing_commit",
		CostUSD:       0.5,
		OccurredAt:    time.Unix(1100, 0),
	}
	require.NoError(t, repo.RecordSubscriptionAccountQuotaUsage(ctx, usage))
	require.NoError(t, repo.RecordSubscriptionAccountQuotaUsage(ctx, usage))

	stored, err := repo.FindSubscriptionAccountByID(ctx, account.ID)
	require.NoError(t, err)
	assert.InDelta(t, 1.0, stored.QuotaUsedUSD, 0.000001)

	var events []subscriptionAccountQuotaEventModel
	require.NoError(t, repo.db.Order("id").Find(&events).Error)
	require.Len(t, events, 1)
	assert.Equal(t, "reservation-1", events[0].ReservationID)
	assert.Equal(t, "billing_commit", events[0].CostSource)
	assert.InDelta(t, 0.5, events[0].CostUSD, 0.000001)
	assert.InDelta(t, 1.0, events[0].ChargedUSD, 0.000001)
	assert.InDelta(t, 2.0, events[0].RateMultiplier, 0.000001)
}

func TestSubscriptionAccountQuotaUsage_ConcurrentReplayOnlyRecordsOnce(t *testing.T) {
	repo := setupChannelTestDB(t)
	ctx := context.Background()
	account := &biz.SubscriptionAccount{
		Name:           "quota-event-concurrent",
		Platform:       "codex",
		Status:         biz.ChannelStatusEnabled,
		Group:          "default",
		Models:         []string{"gpt-5"},
		AccountID:      "acc_1",
		RateMultiplier: 2,
	}
	require.NoError(t, repo.CreateSubscriptionAccount(ctx, account))

	usage := biz.SubscriptionAccountQuotaUsage{
		AccountID:     account.ID,
		ReservationID: "reservation-1",
		CostSource:    "billing_commit",
		CostUSD:       0.5,
		OccurredAt:    time.Unix(1100, 0),
	}
	var wg sync.WaitGroup
	errs := make(chan error, 8)
	for i := 0; i < cap(errs); i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			errs <- repo.RecordSubscriptionAccountQuotaUsage(ctx, usage)
		}()
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		require.NoError(t, err)
	}

	stored, err := repo.FindSubscriptionAccountByID(ctx, account.ID)
	require.NoError(t, err)
	assert.InDelta(t, 1.0, stored.QuotaUsedUSD, 0.000001)

	var count int64
	require.NoError(t, repo.db.Model(&subscriptionAccountQuotaEventModel{}).Count(&count).Error)
	assert.EqualValues(t, 1, count)
}

func TestAggregateSubscriptionAccountQuotaEvents(t *testing.T) {
	repo := setupChannelTestDB(t)
	ctx := context.Background()
	first := &biz.SubscriptionAccount{
		Name:           "quota-event-first",
		Platform:       "codex",
		Status:         biz.ChannelStatusEnabled,
		Group:          "default",
		Models:         []string{"gpt-5"},
		AccountID:      "acc_1",
		RateMultiplier: 2,
	}
	second := &biz.SubscriptionAccount{
		Name:           "quota-event-second",
		Platform:       "codex",
		Status:         biz.ChannelStatusEnabled,
		Group:          "default",
		Models:         []string{"gpt-5"},
		AccountID:      "acc_2",
		RateMultiplier: 3,
	}
	require.NoError(t, repo.CreateSubscriptionAccount(ctx, first))
	require.NoError(t, repo.CreateSubscriptionAccount(ctx, second))

	require.NoError(t, repo.RecordSubscriptionAccountQuotaUsage(ctx, biz.SubscriptionAccountQuotaUsage{
		AccountID:     first.ID,
		ReservationID: "reservation-1",
		CostSource:    "billing_commit",
		CostUSD:       0.5,
		OccurredAt:    time.Unix(1100, 0),
	}))
	require.NoError(t, repo.RecordSubscriptionAccountQuotaUsage(ctx, biz.SubscriptionAccountQuotaUsage{
		AccountID:     first.ID,
		ReservationID: "reservation-2",
		CostSource:    "billing_commit",
		CostUSD:       0.25,
		OccurredAt:    time.Unix(1200, 0),
	}))
	require.NoError(t, repo.RecordSubscriptionAccountQuotaUsage(ctx, biz.SubscriptionAccountQuotaUsage{
		AccountID:     second.ID,
		ReservationID: "reservation-3",
		CostSource:    "billing_commit",
		CostUSD:       0.2,
		OccurredAt:    time.Unix(1300, 0),
	}))

	rows, err := repo.AggregateSubscriptionAccountQuotaEvents(ctx, biz.SubscriptionAccountQuotaEventFilter{Limit: 10})
	require.NoError(t, err)
	require.Len(t, rows, 2)
	assert.Equal(t, first.ID, rows[0].SubscriptionAccountID)
	assert.InDelta(t, 0.75, rows[0].CostUSD, 0.000001)
	assert.InDelta(t, 1.5, rows[0].ChargedUSD, 0.000001)
	assert.InDelta(t, 2.0, rows[0].AverageRateMultiplier, 0.000001)
	assert.EqualValues(t, 2, rows[0].Count)
	assert.EqualValues(t, 1200, rows[0].LastOccurredAt)
	assert.Equal(t, second.ID, rows[1].SubscriptionAccountID)
	assert.InDelta(t, 0.6, rows[1].ChargedUSD, 0.000001)
}

func TestRecordHealth_OpensAndResetsCircuit(t *testing.T) {
	repo := setupChannelTestDB(t)
	ctx := context.Background()
	ch := &biz.Channel{
		Name:   "health-check",
		Group:  "default",
		Models: []string{"gpt-4o"},
		Status: biz.ChannelStatusEnabled,
	}
	require.NoError(t, repo.CreateChannel(ctx, ch))

	failedAt := time.Unix(100, 0)
	updated, err := repo.RecordHealth(ctx, biz.ChannelHealthEvent{
		ChannelID:    ch.ID,
		Success:      false,
		Error:        "status=502",
		ResponseTime: 1500,
		CheckedAt:    failedAt,
	}, 1, 5*time.Minute)
	require.NoError(t, err)
	assert.Equal(t, biz.ChannelHealthUnavailable, updated.HealthStatus)
	assert.Equal(t, "status=502", updated.HealthLastError)
	assert.EqualValues(t, 1, updated.HealthConsecutiveFailures)
	assert.Equal(t, failedAt.Add(5*time.Minute).Unix(), updated.CircuitOpenedUntil)

	stored, err := repo.FindByID(ctx, ch.ID)
	require.NoError(t, err)
	assert.Equal(t, biz.ChannelHealthUnavailable, stored.HealthStatus)
	assert.Equal(t, int64(1500), stored.ResponseTime)

	succeededAt := time.Unix(500, 0)
	updated, err = repo.RecordHealth(ctx, biz.ChannelHealthEvent{
		ChannelID:    ch.ID,
		Success:      true,
		ResponseTime: 120,
		CheckedAt:    succeededAt,
	}, 1, 5*time.Minute)
	require.NoError(t, err)
	assert.Equal(t, biz.ChannelHealthHealthy, updated.HealthStatus)
	assert.Equal(t, "", updated.HealthLastError)
	assert.EqualValues(t, 0, updated.HealthConsecutiveFailures)
	assert.EqualValues(t, 0, updated.CircuitOpenedUntil)
	assert.Equal(t, succeededAt.Unix(), updated.HealthLastSuccessTime)
}

func TestRecordAccountQuotaSnapshot_PersistsAndUpdatesAccount(t *testing.T) {
	repo := setupChannelTestDB(t)
	ctx := context.Background()
	require.NoError(t, repo.db.Exec(`
		CREATE TABLE IF NOT EXISTS account_quota_snapshots (
			account_id INTEGER PRIMARY KEY,
			primary_used_percent REAL,
			primary_reset_after_seconds INTEGER,
			primary_window_minutes INTEGER,
			secondary_used_percent REAL,
			secondary_reset_after_seconds INTEGER,
			secondary_window_minutes INTEGER,
			primary_over_secondary_percent REAL,
			updated_at DATETIME,
			snapshot_paused INTEGER DEFAULT 0
		)
	`).Error)

	account := &biz.SubscriptionAccount{
		Name:      "quota",
		Platform:  "codex",
		Status:    biz.ChannelStatusEnabled,
		Group:     "default",
		Models:    []string{"gpt-5"},
		AccountID: "acc_1",
	}
	require.NoError(t, repo.CreateSubscriptionAccount(ctx, account))

	used := 96.5
	reset := int32(120)
	window := int32(300)
	secondaryUsed := 48.25
	secondaryReset := int32(86400)
	secondaryWindow := int32(10080)
	updatedAt := time.Unix(1000, 0).UTC()
	require.NoError(t, repo.RecordAccountQuotaSnapshot(ctx, &biz.AccountQuotaSnapshot{
		AccountID:                  account.ID,
		PrimaryUsedPercent:         &used,
		PrimaryResetAfterSeconds:   &reset,
		PrimaryWindowMinutes:       &window,
		SecondaryUsedPercent:       &secondaryUsed,
		SecondaryResetAfterSeconds: &secondaryReset,
		SecondaryWindowMinutes:     &secondaryWindow,
		UpdatedAt:                  updatedAt,
	}))

	snapshot, err := repo.GetAccountQuotaSnapshot(ctx, account.ID)
	require.NoError(t, err)
	require.NotNil(t, snapshot.PrimaryUsedPercent)
	assert.Equal(t, used, *snapshot.PrimaryUsedPercent)
	require.NotNil(t, snapshot.PrimaryWindowMinutes)
	assert.EqualValues(t, window, *snapshot.PrimaryWindowMinutes)

	stored, err := repo.FindSubscriptionAccountByID(ctx, account.ID)
	require.NoError(t, err)
	assert.EqualValues(t, used, stored.QuotaUsedPercent)
	assert.Equal(t, updatedAt.Add(time.Duration(secondaryReset)*time.Second).Unix(), stored.QuotaResetAt)

	listed, total, err := repo.ListSubscriptionAccounts(ctx, 1, 20, "", "", 0, "")
	require.NoError(t, err)
	require.EqualValues(t, 1, total)
	require.Len(t, listed, 1)
	require.NotNil(t, listed[0].PrimaryQuotaUsedPercent)
	assert.Equal(t, used, *listed[0].PrimaryQuotaUsedPercent)
	require.NotNil(t, listed[0].PrimaryQuotaWindowMinutes)
	assert.EqualValues(t, window, *listed[0].PrimaryQuotaWindowMinutes)
	require.NotNil(t, listed[0].SecondaryQuotaUsedPercent)
	assert.Equal(t, secondaryUsed, *listed[0].SecondaryQuotaUsedPercent)
	require.NotNil(t, listed[0].SecondaryQuotaWindowMinutes)
	assert.EqualValues(t, secondaryWindow, *listed[0].SecondaryQuotaWindowMinutes)
	assert.Equal(t, updatedAt.Unix(), listed[0].QuotaSnapshotUpdatedAt)

	// Review H1/H2 fix: a "quota exhausted" reason derives the "quota"
	// recovery policy, which is transient. The account must NOT be disabled
	// (status must stay enabled) so the recovery sweeper can clear the
	// unschedulable markers once the local window resets. Only authorization
	// errors (manual policy) disable the account.
	require.NoError(t, repo.AutoPauseAccount(ctx, account.ID, "local quota exhausted"))
	stored, err = repo.FindSubscriptionAccountByID(ctx, account.ID)
	require.NoError(t, err)
	assert.EqualValues(t, biz.ChannelStatusEnabled, stored.Status)
	assert.Equal(t, "local quota exhausted", stored.LastError)
	assert.Contains(t, stored.Metadata, `"recovery_policy":"quota"`)
}

func TestGetAccountQuotaSnapshotMemorySkipsOverflowResetAfter(t *testing.T) {
	repo := &Repository{
		subAccounts: map[int64]*biz.SubscriptionAccount{
			1: {
				ID:           1,
				Status:       biz.ChannelStatusEnabled,
				QuotaResetAt: time.Now().Unix() + int64(math.MaxInt32) + 1,
			},
		},
	}

	snapshot, err := repo.GetAccountQuotaSnapshot(context.Background(), 1)
	require.NoError(t, err)
	if snapshot.PrimaryResetAfterSeconds != nil {
		t.Fatalf("reset after should be absent for overflow value, got %d", *snapshot.PrimaryResetAfterSeconds)
	}
}

// TestAutoPauseAccount_CodexKeepsEnabledWithCodexPolicy (review §6 regression
// for H1/H2): a codex snapshot exhaustion reason must keep the account ENABLED
// and stamp the "codex" recovery policy (not manual/disabled), so the recovery
// sweeper's codex branch can clear markers once the snapshot resets.
func TestAutoPauseAccount_CodexKeepsEnabledWithCodexPolicy(t *testing.T) {
	repo := setupChannelTestDB(t)
	ctx := context.Background()
	account := &biz.SubscriptionAccount{
		Name:      "codex-acct",
		Platform:  "codex",
		Status:    biz.ChannelStatusEnabled,
		Group:     "default",
		Models:    []string{"gpt-5"},
		AccountID: "acc_codex",
	}
	require.NoError(t, repo.CreateSubscriptionAccount(ctx, account))

	require.NoError(t, repo.AutoPauseAccount(ctx, account.ID, "codex quota exhausted"))
	stored, err := repo.FindSubscriptionAccountByID(ctx, account.ID)
	require.NoError(t, err)
	// Must NOT be disabled — the sweeper only scans enabled accounts.
	assert.EqualValues(t, biz.ChannelStatusEnabled, stored.Status)
	assert.Equal(t, "codex quota exhausted", stored.LastError)
	assert.Contains(t, stored.Metadata, `"recovery_policy":"codex"`)
}

// TestAutoPauseAccount_AuthorizationErrorDisablesWithManualPolicy (review H1/H2
// guard): a 401/403 authorization error must still disable the account and
// stamp manual policy (never auto-recover), preserving the existing behavior
// for auth errors.
func TestAutoPauseAccount_AuthorizationErrorDisablesWithManualPolicy(t *testing.T) {
	repo := setupChannelTestDB(t)
	ctx := context.Background()
	account := &biz.SubscriptionAccount{
		Name:      "auth-acct",
		Platform:  "codex",
		Status:    biz.ChannelStatusEnabled,
		Group:     "default",
		Models:    []string{"gpt-5"},
		AccountID: "acc_auth",
	}
	require.NoError(t, repo.CreateSubscriptionAccount(ctx, account))

	require.NoError(t, repo.AutoPauseAccount(ctx, account.ID, "upstream 401 unauthorized"))
	stored, err := repo.FindSubscriptionAccountByID(ctx, account.ID)
	require.NoError(t, err)
	assert.EqualValues(t, biz.ChannelStatusDisabled, stored.Status)
	assert.Contains(t, stored.Metadata, `"recovery_policy":"manual"`)
}

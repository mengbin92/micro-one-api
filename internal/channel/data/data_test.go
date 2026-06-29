package data

import (
	"context"
	"testing"
	"time"

	"micro-one-api/internal/channel/biz"

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
			concurrency INTEGER DEFAULT 1
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

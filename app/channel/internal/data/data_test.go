package data

import (
	"context"
	"encoding/base64"
	"math"
	"sync"
	"testing"
	"time"

	"micro-one-api/app/channel/internal/biz"
	"micro-one-api/domain/authorization"
	authztest "micro-one-api/domain/authorization/testutil"
	appcrypto "micro-one-api/platform/security/crypto"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
)

func TestNewRepositoryFromEnvRequiresDSNUnlessMemoryModeEnabled(t *testing.T) {
	t.Setenv("CHANNEL_SQL_DSN", "")
	t.Setenv("SQL_DSN", "")
	t.Setenv("CHANNEL_MEMORY_MODE", "")

	_, err := NewRepositoryFromEnv("", "")
	require.ErrorContains(t, err, "CHANNEL_MEMORY_MODE=true")

	t.Setenv("CHANNEL_MEMORY_MODE", "true")
	repo, err := NewRepositoryFromEnv("", "")
	require.NoError(t, err)
	require.Nil(t, repo.db)
}

func TestEncryptionKeyFromEnvRequiresAESKeyLength(t *testing.T) {
	t.Setenv("CHANNEL_ENCRYPTION_KEY", "short")
	_, err := encryptionKeyFromEnv()
	require.ErrorContains(t, err, "16, 24, or 32 bytes")

	t.Setenv("CHANNEL_ENCRYPTION_KEY", "01234567890123456789012345678901")
	key, err := encryptionKeyFromEnv()
	require.NoError(t, err)
	require.Len(t, key, 32)
}

func TestEncryptKeyNeverFallsBackToPlaintext(t *testing.T) {
	_, err := (&Repository{}).encryptKey("provider-secret")
	require.ErrorContains(t, err, "encryption key is not configured")

	repo := &Repository{encKey: []byte("01234567890123456789012345678901")}
	encrypted, err := repo.encryptKey("provider-secret")
	require.NoError(t, err)
	require.NotEqual(t, "provider-secret", encrypted)
	plain, err := repo.decryptKey(encrypted)
	require.NoError(t, err)
	require.Equal(t, "provider-secret", plain)
}

func TestCreateChannelRejectsMissingEncryptionKey(t *testing.T) {
	repo := setupChannelTestDB(t)
	repo.encKey = nil

	err := repo.CreateChannel(context.Background(), &biz.Channel{
		Name:   "unprotected",
		Key:    "provider-secret",
		Models: []string{"gpt-4o"},
	})
	require.ErrorContains(t, err, "encryption key is not configured")

	var count int64
	require.NoError(t, repo.db.Model(&channelModel{}).Count(&count).Error)
	assert.Zero(t, count)
}

func TestRecordUsageOnceIsIdempotent(t *testing.T) {
	repo := setupChannelTestDB(t)
	require.NoError(t, repo.db.Exec("INSERT INTO channels (id, name, used_quota) VALUES (1, 'primary', 10)").Error)

	require.NoError(t, repo.RecordUsageOnce(context.Background(), "res-1", 1, 25))
	require.NoError(t, repo.RecordUsageOnce(context.Background(), "res-1", 1, 25))

	var used int64
	require.NoError(t, repo.db.Table("channels").Select("used_quota").Where("id = 1").Scan(&used).Error)
	assert.Equal(t, int64(35), used)
	var claims int64
	require.NoError(t, repo.db.Table("channel_usage_events").Count(&claims).Error)
	assert.Equal(t, int64(1), claims)
}

func TestMigrateCredentialsDryRunAndApplyIsIdempotent(t *testing.T) {
	repo := setupChannelTestDB(t)
	repo.encKey = []byte("01234567890123456789012345678901")
	encrypted, err := appcrypto.Encrypt("already-protected", repo.encKey)
	require.NoError(t, err)
	indeterminate := base64.StdEncoding.EncodeToString(make([]byte, 28))
	require.NoError(t, repo.db.Create(&channelCredentialRow{ID: 1, Key: "plain-channel"}).Error)
	require.NoError(t, repo.db.Create(&channelCredentialRow{ID: 2, Key: encrypted}).Error)
	require.NoError(t, repo.db.Create(&channelCredentialRow{ID: 3, Key: indeterminate}).Error)
	require.NoError(t, repo.db.Exec("INSERT INTO subscription_accounts (id, name, platform, access_token, refresh_token) VALUES (?, ?, ?, ?, ?)", 1, "one", "codex", "plain-access", encrypted).Error)

	report, err := repo.MigrateCredentials(context.Background(), true)
	require.NoError(t, err)
	assert.Equal(t, 3, report.Channels.Scanned)
	assert.Equal(t, 1, report.Channels.Encrypted)
	assert.Equal(t, 1, report.Channels.SuspectedPlaintext)
	assert.Equal(t, 1, report.Channels.Indeterminate)
	assert.Equal(t, 2, report.SubscriptionAccounts.Scanned)
	assert.Equal(t, 1, report.SubscriptionAccounts.Encrypted)
	assert.Equal(t, 1, report.SubscriptionAccounts.SuspectedPlaintext)
	assert.Zero(t, report.Channels.Rewritten)
	assert.Len(t, report.SuspectedPlaintext, 2)
	assert.Len(t, report.Indeterminate, 1)

	report, err = repo.MigrateCredentials(context.Background(), false)
	require.NoError(t, err)
	assert.Equal(t, 1, report.Channels.Rewritten)
	assert.Equal(t, 1, report.SubscriptionAccounts.Rewritten)

	report, err = repo.MigrateCredentials(context.Background(), true)
	require.NoError(t, err)
	assert.Zero(t, report.Channels.SuspectedPlaintext)
	assert.Zero(t, report.SubscriptionAccounts.SuspectedPlaintext)
	assert.Equal(t, 2, report.Channels.Encrypted)
	assert.Equal(t, 2, report.SubscriptionAccounts.Encrypted)
	assert.Equal(t, 1, report.Channels.Indeterminate)
}

// setupChannelTestDB creates an in-memory sqlite DB matching the
// `channels` and `candidates` schemas relevant to repo behaviour.
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
			authorization_revision INTEGER NOT NULL DEFAULT 1,
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
			system_prompt TEXT,
			restrict_models INTEGER NOT NULL DEFAULT 1
		)
	`).Error)

	require.NoError(t, db.Exec(`
		CREATE TABLE channel_usage_events (
			reservation_id TEXT PRIMARY KEY,
			channel_id INTEGER NOT NULL,
			quota INTEGER NOT NULL,
			created_at INTEGER NOT NULL DEFAULT 0
		)
	`).Error)

	require.NoError(t, db.Exec(`
		CREATE TABLE subscription_accounts (
			credential_revision INTEGER NOT NULL DEFAULT 1,
			credential_refresh_pending INTEGER NOT NULL DEFAULT 0,
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			name TEXT NOT NULL DEFAULT '',
			platform TEXT NOT NULL,
			account_type TEXT NOT NULL DEFAULT 'oauth',
			status INTEGER DEFAULT 1,
			`+"`group`"+` TEXT DEFAULT 'default',
			models TEXT,
			priority INTEGER DEFAULT 0,
			weight INTEGER NOT NULL DEFAULT 0,
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

	require.NoError(t, db.Exec(`
		CREATE TABLE IF NOT EXISTS model_routings (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			group_name TEXT NOT NULL DEFAULT 'default',
			model TEXT NOT NULL,
			platform TEXT NOT NULL DEFAULT '',
			subscription_account_id INTEGER NOT NULL,
			enabled INTEGER DEFAULT 1,
			priority INTEGER NOT NULL DEFAULT 0,
			created_at INTEGER NOT NULL DEFAULT 0,
			updated_at INTEGER NOT NULL DEFAULT 0,
			revision INTEGER NOT NULL DEFAULT 1
		)
	`).Error)
	require.NoError(t, db.Exec(`
		CREATE UNIQUE INDEX IF NOT EXISTS idx_model_routings_group_model_account
		ON model_routings(group_name, model, platform, subscription_account_id)
	`).Error)
	require.NoError(t, db.Exec(`
		CREATE TABLE IF NOT EXISTS usage_semantic_source_blocks (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			source_kind TEXT NOT NULL,
			source_id INTEGER NOT NULL,
			upstream_model_id TEXT NOT NULL,
			adapter_protocol TEXT NOT NULL,
			status TEXT NOT NULL DEFAULT '',
			reason TEXT NOT NULL DEFAULT '',
			window_started_at DATETIME,
			consecutive_ambiguous INTEGER NOT NULL DEFAULT 0,
			blocked_until DATETIME,
			last_verified_at DATETIME,
			created_at DATETIME NOT NULL,
			updated_at DATETIME NOT NULL,
			revision INTEGER NOT NULL DEFAULT 1,
			UNIQUE(source_kind, source_id, upstream_model_id, adapter_protocol)
		)
	`).Error)

	// Model registry + channel mappings (used by the registry list path).
	require.NoError(t, db.Exec(`
		CREATE TABLE models (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			authorization_revision INTEGER NOT NULL DEFAULT 1,
			model_id TEXT NOT NULL,
			display_name TEXT NOT NULL DEFAULT '',
			description TEXT,
			provider TEXT NOT NULL DEFAULT '',
			model_type TEXT NOT NULL DEFAULT 'chat',
			context_window INTEGER NOT NULL DEFAULT 0,
			pricing_input REAL NOT NULL DEFAULT 0,
			pricing_cache_read REAL NOT NULL DEFAULT 0,
			pricing_output REAL NOT NULL DEFAULT 0,
			status INTEGER NOT NULL DEFAULT 1,
			is_public INTEGER NOT NULL DEFAULT 1,
			capabilities TEXT DEFAULT '',
			input_modalities TEXT DEFAULT '',
			output_modalities TEXT DEFAULT '',
			tags TEXT DEFAULT '',
			category TEXT NOT NULL DEFAULT '',
			tier TEXT NOT NULL DEFAULT '',
			metadata TEXT,
			created_at INTEGER NOT NULL DEFAULT 0,
			updated_at INTEGER NOT NULL DEFAULT 0
		)
	`).Error)
	require.NoError(t, db.Exec(`
		CREATE UNIQUE INDEX IF NOT EXISTS idx_models_model_id_ci
		ON models(LOWER(model_id))
	`).Error)
	require.NoError(t, db.Exec(`
		CREATE TABLE model_channel_mapping (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			channel_id INTEGER NOT NULL,
			model_id INTEGER NOT NULL,
			enabled INTEGER DEFAULT 1,
			priority INTEGER DEFAULT 0,
			config TEXT DEFAULT '',
			upstream_model_id TEXT DEFAULT '',
			created_at INTEGER NOT NULL DEFAULT 0,
			updated_at INTEGER NOT NULL DEFAULT 0
		)
	`).Error)
	require.NoError(t, db.Exec(`
		CREATE UNIQUE INDEX IF NOT EXISTS idx_mcm_channel_model
		ON model_channel_mapping(channel_id, model_id)
	`).Error)

	// IAM-scoped writes resolve authoritative routing groups and append write
	// audits; both tables are part of the repository's write contract.
	require.NoError(t, db.AutoMigrate(&routingGroupModel{}))
	require.NoError(t, db.Exec(`
		CREATE TABLE resource_write_audits (
			event_id TEXT PRIMARY KEY,
			actor_user_id INTEGER NOT NULL,
			operation TEXT NOT NULL,
			resource_id TEXT NOT NULL,
			decision_versions TEXT NOT NULL,
			reason TEXT NOT NULL,
			occurred_at INTEGER NOT NULL,
			result TEXT NOT NULL
		)
	`).Error)

	require.NoError(t, db.AutoMigrate(&modelSubscriptionMappingModel{}))
	return &Repository{db: db, encKey: []byte("01234567890123456789012345678901")}
}

type testRegistryChannelRoutingCandidateRow struct {
	ChannelID       int64  `gorm:"column:channel_id"`
	Model           string `gorm:"column:model"`
	Enabled         bool   `gorm:"column:enabled"`
	Priority        int32  `gorm:"column:priority"`
	UpstreamModelID string `gorm:"column:upstream_model_id"`
}

type testRegistrySubscriptionRoutingCandidateRow struct {
	AccountID       int64  `gorm:"column:account_id"`
	Model           string `gorm:"column:model"`
	GroupName       string `gorm:"column:group_name"`
	Enabled         bool   `gorm:"column:enabled"`
	Priority        int32  `gorm:"column:priority"`
	UpstreamModelID string `gorm:"column:upstream_model_id"`
}

func loadAbilities(t *testing.T, repo *Repository, channelID int64) []testRegistryChannelRoutingCandidateRow {
	t.Helper()
	var rows []testRegistryChannelRoutingCandidateRow
	require.NoError(t, repo.db.Table("model_channel_mapping AS mcm").
		Select("mcm.channel_id, m.model_id AS model, mcm.enabled, mcm.priority, mcm.upstream_model_id").
		Joins("JOIN models AS m ON m.id = mcm.model_id").
		Where("mcm.channel_id = ?", channelID).
		Order("m.model_id ASC").Scan(&rows).Error)
	return rows
}

func loadSubscriptionAbilities(t *testing.T, repo *Repository, accountID int64) []testRegistrySubscriptionRoutingCandidateRow {
	t.Helper()
	var rows []testRegistrySubscriptionRoutingCandidateRow
	require.NoError(t, repo.db.Table("model_subscription_mapping AS msm").
		Select("msm.subscription_account_id AS account_id, m.model_id AS model, msm.group_name, msm.enabled, msm.priority, msm.upstream_model_id").
		Joins("JOIN models AS m ON m.id = msm.model_id").
		Where("msm.subscription_account_id = ?", accountID).
		Order("msm.group_name ASC, m.model_id ASC").Scan(&rows).Error)
	return rows
}

func TestCreateChannel_PopulatesRegistryMappings(t *testing.T) {
	repo := setupChannelTestDB(t)
	ctx := context.Background()

	ch := &biz.Channel{
		Name: "test-anthropic", Type: 2, BaseURL: "https://api.anthropic.com", Key: "sk-test",
		Status: biz.ChannelStatusEnabled, Group: "default,premium",
		Models: []string{"claude-opus-4-7", "claude-sonnet-4-6"}, Priority: 100,
	}
	require.NoError(t, repo.CreateChannel(ctx, ch))
	require.NotZero(t, ch.ID)

	rows := loadAbilities(t, repo, ch.ID)
	require.Len(t, rows, 2)
	for _, row := range rows {
		assert.True(t, row.Enabled)
		assert.EqualValues(t, 100, row.Priority)
		assert.Equal(t, ch.ID, row.ChannelID)
		assert.Equal(t, row.Model, row.UpstreamModelID)
	}
}

func TestCreateChannel_DisabledChannel_NotRoutable(t *testing.T) {
	repo := setupChannelTestDB(t)
	ctx := context.Background()
	ch := &biz.Channel{Name: "disabled", Group: "default", Models: []string{"gpt-4o"}, Status: 2}
	require.NoError(t, repo.CreateChannel(ctx, ch))
	_, err := repo.ListChannelCandidates(ctx, "default", "gpt-4o")
	require.ErrorIs(t, err, biz.ErrChannelNotFound)
}

func TestCreateChannel_SkipsEmptyGroupOrModel(t *testing.T) {
	repo := setupChannelTestDB(t)
	ctx := context.Background()
	ch := &biz.Channel{
		Name: "with-empties", Group: "default,,premium",
		Models: []string{"gpt-4o", "", "claude-opus-4-7"}, Status: biz.ChannelStatusEnabled,
	}
	require.NoError(t, repo.CreateChannel(ctx, ch))
	rows := loadAbilities(t, repo, ch.ID)
	require.Len(t, rows, 2)
	for _, row := range rows {
		assert.NotEmpty(t, row.Model)
	}
}

func TestUpdateChannel_RewritesRegistryMappings(t *testing.T) {
	repo := setupChannelTestDB(t)
	ctx := context.Background()
	ch := &biz.Channel{
		Name: "drift", Group: "default", Models: []string{"gpt-3.5-turbo", "gpt-4"},
		Status: biz.ChannelStatusEnabled, Priority: 10,
	}
	require.NoError(t, repo.CreateChannel(ctx, ch))
	require.Len(t, loadAbilities(t, repo, ch.ID), 2)

	ch.Models = []string{"gpt-4o"}
	ch.Priority = 50
	require.NoError(t, repo.UpdateChannel(ctx, ch))
	rows := loadAbilities(t, repo, ch.ID)
	require.Len(t, rows, 1)
	assert.Equal(t, "gpt-4o", rows[0].Model)
	assert.EqualValues(t, 50, rows[0].Priority)
}

func TestUpdateChannel_PersistsModelMapping(t *testing.T) {
	repo := setupChannelTestDB(t)
	ctx := context.Background()
	ch := &biz.Channel{Name: "drift", Group: "default", Models: []string{"gpt-4o"}, Status: biz.ChannelStatusEnabled, Priority: 10}
	require.NoError(t, repo.CreateChannel(ctx, ch))
	ch.ModelMapping = `{"gpt-4o":"gpt-4o-mini"}`
	require.NoError(t, repo.UpdateChannel(ctx, ch))
	got, err := repo.FindByID(ctx, ch.ID)
	require.NoError(t, err)
	assert.Equal(t, `{"gpt-4o":"gpt-4o-mini"}`, got.ModelMapping)
}

func TestUpdateSubscriptionAccount_PersistsModelMapping(t *testing.T) {
	repo := setupChannelTestDB(t)
	ctx := context.Background()
	account := &biz.SubscriptionAccount{
		Name: "codex", Platform: "codex", AccountType: "oauth", Status: biz.ChannelStatusEnabled,
		Group: "default", Models: []string{"gpt-5"}, Priority: 30, AccountID: "acc_1",
	}
	require.NoError(t, repo.CreateSubscriptionAccount(ctx, account))
	account.ModelMapping = `{"gpt-5":"gpt-5-codex"}`
	require.NoError(t, repo.UpdateSubscriptionAccount(ctx, account))
	got, err := repo.FindSubscriptionAccountByID(ctx, account.ID)
	require.NoError(t, err)
	assert.Equal(t, `{"gpt-5":"gpt-5-codex"}`, got.ModelMapping)
}

func TestDeleteChannel_RemovesRegistryMappings(t *testing.T) {
	repo := setupChannelTestDB(t)
	ctx := context.Background()
	ch := &biz.Channel{Name: "tbd", Group: "default", Models: []string{"gpt-4o"}, Status: biz.ChannelStatusEnabled}
	require.NoError(t, repo.CreateChannel(ctx, ch))
	require.Len(t, loadAbilities(t, repo, ch.ID), 1)
	require.NoError(t, repo.DeleteChannel(ctx, ch.ID))
	assert.Empty(t, loadAbilities(t, repo, ch.ID))
}

func TestChangeStatus_RemovesFromRouting(t *testing.T) {
	repo := setupChannelTestDB(t)
	ctx := context.Background()
	ch := &biz.Channel{Name: "toggleable", Group: "default", Models: []string{"gpt-4o"}, Status: biz.ChannelStatusEnabled}
	require.NoError(t, repo.CreateChannel(ctx, ch))
	_, err := repo.ListChannelCandidates(ctx, "default", "gpt-4o")
	require.NoError(t, err)
	require.NoError(t, repo.ChangeStatus(ctx, ch.ID, 2))
	_, err = repo.ListChannelCandidates(ctx, "default", "gpt-4o")
	require.ErrorIs(t, err, biz.ErrChannelNotFound)
	require.NoError(t, repo.ChangeStatus(ctx, ch.ID, biz.ChannelStatusEnabled))
	_, err = repo.ListChannelCandidates(ctx, "default", "gpt-4o")
	require.NoError(t, err)
}

func TestCreateSubscriptionAccount_PopulatesRegistryMappings(t *testing.T) {
	repo := setupChannelTestDB(t)
	ctx := context.Background()
	account := &biz.SubscriptionAccount{
		Name: "codex", Platform: "codex", AccountType: "oauth", Status: biz.ChannelStatusEnabled,
		Group: "default,premium", Models: []string{"gpt-5", "gpt-5-codex"}, Priority: 30, AccountID: "acc_1",
	}
	require.NoError(t, repo.CreateSubscriptionAccount(ctx, account))
	require.NotZero(t, account.ID)
	rows := loadSubscriptionAbilities(t, repo, account.ID)
	require.Len(t, rows, 4)
	for _, row := range rows {
		assert.True(t, row.Enabled)
		assert.EqualValues(t, 30, row.Priority)
		assert.NotEmpty(t, row.GroupName)
	}
}

func TestSubscriptionModelMappingManualEditStopsAutoRewrite(t *testing.T) {
	repo := setupChannelTestDB(t)
	ctx := context.Background()
	account := &biz.SubscriptionAccount{
		Name: "codex", Platform: "codex", AccountType: "oauth", Status: biz.ChannelStatusEnabled,
		Group: "default", Models: []string{"gpt-5"}, Priority: 10, AccountID: "acc_1",
	}
	require.NoError(t, repo.CreateSubscriptionAccount(ctx, account))
	model, err := repo.GetModelByID(ctx, "gpt-5")
	require.NoError(t, err)
	require.NoError(t, repo.UpsertSubscriptionMapping(ctx, &biz.ModelSubscriptionMapping{
		SubscriptionAccountID: account.ID, ModelPK: model.ID, GroupName: "default",
		Enabled: true, EnabledHasValue: true, Priority: 99, UpstreamModelID: "custom-upstream",
	}))

	account.Name = "codex-renamed"
	require.NoError(t, repo.UpdateSubscriptionAccount(ctx, account))
	mappings, err := repo.ListSubscriptionMappings(ctx, account.ID)
	require.NoError(t, err)
	require.Len(t, mappings, 1)
	assert.EqualValues(t, 99, mappings[0].Priority)
	assert.Equal(t, "custom-upstream", mappings[0].UpstreamModelID)
	assert.Empty(t, mappings[0].Config, "manual edit must promote the auto mapping")
}

func TestSubscriptionAutoMappingRequiresMappingPermission(t *testing.T) {
	repo := setupChannelTestDB(t)
	require.NoError(t, repo.db.Exec(`
		INSERT INTO routing_groups (id, key, status) VALUES (10, 'default', 'enabled')
	`).Error)
	newAccount := func() *biz.SubscriptionAccount {
		return &biz.SubscriptionAccount{
			Name: "guarded", Platform: "codex", AccountType: "oauth", Status: biz.ChannelStatusEnabled,
			Group: "default", Models: []string{"gpt-5"}, AccountID: "acc_guard",
		}
	}

	// An IAM caller can hold account.create while its mapping grant only covers
	// an unrelated resource. The auto-projection must still be denied and the
	// entire account creation rolled back.
	denied := authorization.WithQueryScope(authztest.Context(), "channel.account.create", authztest.All())
	denied = authorization.WithQueryScope(denied, "channel.model_mapping.create", authztest.Resources(9999))
	require.ErrorIs(t, repo.CreateSubscriptionAccount(denied, newAccount()), authorization.ErrDenied)
	var accounts, mappings int64
	require.NoError(t, repo.db.Table("subscription_accounts").Count(&accounts).Error)
	require.NoError(t, repo.db.Table("model_subscription_mapping").Count(&mappings).Error)
	require.Zero(t, accounts, "denied mapping write must roll back the account")
	require.Zero(t, mappings, "denied mapping write must roll back the projection")

	allowed := authorization.WithQueryScope(authztest.Context(), "channel.account.create", authztest.All())
	allowed = authorization.WithQueryScope(allowed, "channel.model_mapping.create", authztest.All())
	account := newAccount()
	require.NoError(t, repo.CreateSubscriptionAccount(allowed, account))
	rows := loadSubscriptionAbilities(t, repo, account.ID)
	require.Len(t, rows, 1)
	assert.True(t, rows[0].Enabled)
	var audits int64
	require.NoError(t, repo.db.Table("resource_write_audits").
		Where("operation = ? AND result = ?", "channel.model_mapping.create", "success").Count(&audits).Error)
	require.EqualValues(t, 1, audits, "mapping grant must be audited")
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
		wg.Go(func() {
			errs <- repo.RecordSubscriptionAccountQuotaUsage(ctx, usage)
		})
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
	updated, err = repo.RecordHealth(ctx, biz.ChannelHealthEvent{ChannelID: ch.ID, Success: true, CheckedAt: succeededAt}, 1, 5*time.Minute)
	require.NoError(t, err)
	assert.Equal(t, biz.ChannelHealthUnavailable, updated.HealthStatus, "unfenced probe success cannot authorize recovery")
	updated, err = repo.RecordHealth(ctx, biz.ChannelHealthEvent{
		ConfigurationRevision: ch.UpstreamRevision(),
		ChannelID:             ch.ID,
		Success:               true,
		ResponseTime:          120,
		CheckedAt:             succeededAt,
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

// ---------------------------------------------------------------------------
// P1 (#4) — wildcard model matching in candidates queries.
// ---------------------------------------------------------------------------

func TestListChannelCandidates_WildcardPattern(t *testing.T) {
	repo := setupChannelTestDB(t)
	ctx := context.Background()
	require.NoError(t, repo.CreateChannel(ctx, &biz.Channel{
		ID: 1, Type: 1, Name: "wildcard-chan", Status: biz.ChannelStatusEnabled,
		Group: "default", Models: []string{"claude-*"}, Priority: 5,
	}))
	_, err := repo.ListChannelCandidates(ctx, "default", "claude-sonnet-4")
	require.ErrorIs(t, err, biz.ErrChannelNotFound)
}

func TestListSubscriptionCandidates_WildcardPattern(t *testing.T) {
	repo := setupChannelTestDB(t)
	ctx := context.Background()

	acc := &biz.SubscriptionAccount{
		Name: "wildcard-acc", Platform: "codex", Status: biz.ChannelStatusEnabled,
		Group: "default", Models: []string{"claude-*"}, Priority: 5, AccountID: "acc_wc",
	}
	require.NoError(t, repo.CreateSubscriptionAccount(ctx, acc))

	_, err := repo.ListSubscriptionCandidates(ctx, "default", "claude-sonnet-4", "codex")
	require.ErrorIs(t, err, biz.ErrSubscriptionAccountNotFound)
}

func TestListAvailableModels_ExcludesWildcardPatterns(t *testing.T) {
	repo := setupChannelTestDB(t)
	ctx := context.Background()

	// Channel advertising both a concrete model and a wildcard pattern.
	require.NoError(t, repo.CreateChannel(ctx, &biz.Channel{
		ID: 1, Type: 1, Name: "mixed", Status: biz.ChannelStatusEnabled,
		Group: "default", Models: []string{"gpt-4o", "claude-*"}, Priority: 5,
	}))

	models, err := repo.ListAvailableModels(ctx, "default")
	require.NoError(t, err)
	// "gpt-4o" is advertised; "claude-*" is a routing rule and must NOT appear.
	assert.Contains(t, models, "gpt-4o")
	for _, m := range models {
		if m == "claude-*" {
			t.Fatalf("wildcard pattern claude-* must not be advertised in /v1/models: %v", models)
		}
	}
}

func TestListChannelCandidates_RejectsUnregisteredWildcard(t *testing.T) {
	repo := setupChannelTestDB(t)
	ctx := context.Background()
	require.NoError(t, repo.CreateChannel(ctx, &biz.Channel{
		ID: 1, Type: 1, Name: "wildcard-only", Status: biz.ChannelStatusEnabled,
		Group: "default", Models: []string{"*"}, Priority: 1,
	}))
	_, err := repo.ListChannelCandidates(ctx, "default", "any-random-model")
	require.ErrorIs(t, err, biz.ErrChannelNotFound)
}

func TestSelectChannel_RejectsUnregisteredModel(t *testing.T) {
	repo := setupChannelTestDB(t)
	ctx := context.Background()
	require.NoError(t, repo.CreateChannel(ctx, &biz.Channel{
		ID: 1, Type: 1, Name: "explicit-only", Status: biz.ChannelStatusEnabled,
		Group: "default", Priority: 1,
	}))
	uc := biz.NewChannelUsecase(repo, nil)
	_, err := uc.SelectChannel(ctx, "default", "unregistered-model", false)
	require.ErrorIs(t, err, biz.ErrChannelNotFound)
}

func TestSelectChannel_NoCatchAllReturnsNotFound_DB(t *testing.T) {
	repo := setupChannelTestDB(t)
	ctx := context.Background()

	// A restricted channel with an ability for a DIFFERENT model only.
	require.NoError(t, repo.CreateChannel(ctx, &biz.Channel{
		ID: 1, Type: 1, Name: "restricted", Status: biz.ChannelStatusEnabled,
		Group: "default", Models: []string{"gpt-4o"}, Priority: 5,
	}))

	uc := biz.NewChannelUsecase(repo, nil)
	_, err := uc.SelectChannel(ctx, "default", "unregistered-model", false)
	assert.Equal(t, biz.ErrChannelNotFound, err)
}

// TestListModelRoutings_EmptyPlatformMatchesConcretePlatform proves the 🔴#3
// fix: a routing row with an empty platform ("any platform") must match when
// the relay infers a concrete platform (e.g. codex). Pre-fix the equality
// filter platform = ? never matched an empty row.
func TestListModelRoutings_EmptyPlatformMatchesConcretePlatform(t *testing.T) {
	repo := setupChannelTestDB(t)
	ctx := context.Background()

	// Empty-platform routing row (the UI-recommended default).
	require.NoError(t, repo.UpsertModelRouting(ctx, &biz.ModelRouting{
		GroupName: "default", Model: "gpt-5", Platform: "",
		SubscriptionAccountID: 7, Enabled: true, Priority: 5,
	}))

	rows, err := repo.ListModelRoutings(ctx, "default", "gpt-5", "codex")
	require.NoError(t, err)
	require.Len(t, rows, 1, "empty-platform routing must match concrete platform codex")
	assert.Equal(t, int64(7), rows[0].SubscriptionAccountID)
}

// TestListChannelCandidates_ExactBeatsWildcardConsistency (🟡#4):
// when a channel lists BOTH "gpt-4o" (exact) and "gpt-*" (wildcard), the
// candidates query must return ONLY the exact tier — not both — so DB and
// memory paths agree and the selector does not double-weight the channel.
func TestListChannelCandidates_ExactBeatsWildcardConsistency(t *testing.T) {
	repo := setupChannelTestDB(t)
	ctx := context.Background()
	require.NoError(t, repo.CreateChannel(ctx, &biz.Channel{
		ID: 1, Type: 1, Name: "both", Status: biz.ChannelStatusEnabled,
		Group: "default", Models: []string{"gpt-4o", "gpt-*"}, Priority: 5,
	}))
	candidates, err := repo.ListChannelCandidates(ctx, "default", "gpt-4o")
	require.NoError(t, err)
	// Exactly one ability row — the exact "gpt-4o", not the wildcard too.
	require.Len(t, candidates, 1, "exact tier must shadow wildcard tier (no double-count)")
	assert.Equal(t, int64(1), candidates[0].ChannelID)

	_, err = repo.ListChannelCandidates(ctx, "default", "gpt-5")
	require.ErrorIs(t, err, biz.ErrChannelNotFound)
}

// TestListChannelCandidates_ExactBeatsWildcardConsistency_Memory:
// same contract on the in-memory repo so DB and memory behave identically.
func TestListChannelCandidates_ExactBeatsWildcardConsistency_Memory(t *testing.T) {
	repo := newMemoryRepository()
	ctx := context.Background()
	require.NoError(t, repo.CreateChannel(ctx, &biz.Channel{
		ID: 1, Type: 1, Name: "both", Status: biz.ChannelStatusEnabled,
		Group: "default", Models: []string{"gpt-4o", "gpt-*"}, Priority: 5,
	}))
	candidates, err := repo.ListChannelCandidates(ctx, "default", "gpt-4o")
	require.NoError(t, err)
	require.Len(t, candidates, 1, "memory path: exact tier must shadow wildcard (no double-count)")
	assert.Equal(t, int64(1), candidates[0].ChannelID)

	_, err = repo.ListChannelCandidates(ctx, "default", "gpt-5")
	require.ErrorIs(t, err, biz.ErrChannelNotFound)
}

// TestSyncChannelModelMappings_CreateAutoRegistersModels verifies that creating
// a channel auto-creates canonical public models and owned mappings, so the
// registry-backed /v1/models path sees the edit immediately.
func TestSyncChannelModelMappings_CreateAutoRegistersModels(t *testing.T) {
	repo := setupChannelTestDB(t)
	ctx := context.Background()

	ch := &biz.Channel{
		Name: "kimi-test", Type: 1, Status: biz.ChannelStatusEnabled,
		Group: "default", Models: []string{"Kimi-K3", "claude-sonnet-4-6"}, Priority: 50,
	}
	require.NoError(t, repo.CreateChannel(ctx, ch))

	var modelRows []modelModel
	require.NoError(t, repo.db.Order("model_id ASC").Find(&modelRows).Error)
	require.Len(t, modelRows, 2)
	assert.Equal(t, "claude-sonnet-4-6", modelRows[0].ModelID)
	assert.Equal(t, "kimi-k3", modelRows[1].ModelID)
	assert.Equal(t, biz.ModelStatusEnabled, int(modelRows[1].Status))
	assert.True(t, modelRows[1].IsPublic)

	var mappings []modelChannelMappingModel
	require.NoError(t, repo.db.Where("channel_id = ?", ch.ID).Order("model_id ASC").Find(&mappings).Error)
	require.Len(t, mappings, 2)
	for _, mapping := range mappings {
		assert.True(t, mapping.Enabled)
		assert.EqualValues(t, 50, mapping.Priority)
		assert.Equal(t, autoChannelModelMappingConfig, mapping.Config)
		assert.NotEmpty(t, mapping.UpstreamModelID)
	}

	got, err := repo.ListAvailableModels(ctx, "default")
	require.NoError(t, err)
	assert.Contains(t, got, "kimi-k3")
	assert.Contains(t, got, "claude-sonnet-4-6")
}

func TestSyncChannelModelMappings_UnrelatedUpdatePreservesManualMappings(t *testing.T) {
	repo := setupChannelTestDB(t)
	ctx := context.Background()
	ch := &biz.Channel{
		Name: "managed", Status: biz.ChannelStatusEnabled,
		Group: "default", Models: []string{"gpt-4o"}, Priority: 10,
	}
	require.NoError(t, repo.CreateChannel(ctx, ch))

	var primary modelModel
	require.NoError(t, repo.db.Where("model_id = ?", "gpt-4o").First(&primary).Error)
	require.NoError(t, repo.UpsertChannelMapping(ctx, &biz.ModelChannelMapping{
		ChannelID: ch.ID, ModelPK: primary.ID,
		Enabled: false, EnabledHasValue: true, Priority: 99,
		UpstreamModelID: "gpt-4o-upstream",
	}))
	extra := &biz.Model{
		ModelID: "claude-opus-4-7", DisplayName: "Claude", Provider: "anthropic",
		ModelType: "chat", Status: biz.ModelStatusEnabled, IsPublic: true,
	}
	require.NoError(t, repo.CreateModel(ctx, extra))
	require.NoError(t, repo.UpsertChannelMapping(ctx, &biz.ModelChannelMapping{
		ChannelID: ch.ID, ModelPK: extra.ID,
		Enabled: true, EnabledHasValue: true, Priority: 77,
		Config: `{"source":"admin"}`, UpstreamModelID: "claude-upstream",
	}))

	ch.Balance = 1 // representative non-model UpdateChannel mutation
	require.NoError(t, repo.UpdateChannel(ctx, ch))

	mappings, err := repo.ListChannelMappings(ctx, ch.ID)
	require.NoError(t, err)
	require.Len(t, mappings, 2)
	byModel := make(map[int64]*biz.ModelChannelMapping, len(mappings))
	for _, mapping := range mappings {
		byModel[mapping.ModelPK] = mapping
	}
	assert.False(t, byModel[primary.ID].Enabled)
	assert.EqualValues(t, 99, byModel[primary.ID].Priority)
	assert.Empty(t, byModel[primary.ID].Config, "empty config is still an explicitly managed mapping")
	assert.Equal(t, "gpt-4o-upstream", byModel[primary.ID].UpstreamModelID)
	assert.EqualValues(t, 77, byModel[extra.ID].Priority)
	assert.Equal(t, `{"source":"admin"}`, byModel[extra.ID].Config)
	assert.Equal(t, "claude-upstream", byModel[extra.ID].UpstreamModelID)
}

func TestSyncChannelModelMappings_ReenableRestoresVisibility(t *testing.T) {
	repo := setupChannelTestDB(t)
	ctx := context.Background()
	ch := &biz.Channel{
		Name: "disabled", Status: biz.ChannelStatusDisabled,
		Group: "default", Models: []string{"gpt-4o"},
	}
	require.NoError(t, repo.CreateChannel(ctx, ch))

	var mapping modelChannelMappingModel
	require.NoError(t, repo.db.Where("channel_id = ?", ch.ID).First(&mapping).Error)
	assert.True(t, mapping.Enabled, "mapping availability must not duplicate channel status")
	got, err := repo.ListAvailableModels(ctx, "default")
	require.NoError(t, err)
	assert.NotContains(t, got, "gpt-4o")

	require.NoError(t, repo.ChangeStatus(ctx, ch.ID, biz.ChannelStatusEnabled))
	got, err = repo.ListAvailableModels(ctx, "default")
	require.NoError(t, err)
	assert.Contains(t, got, "gpt-4o")
}

func TestSyncChannelModelMappings_PreservesConfiguredUpstreamSpelling(t *testing.T) {
	repo := setupChannelTestDB(t)
	ctx := context.Background()
	ch := &biz.Channel{
		Name: "mapped", Status: biz.ChannelStatusEnabled, Group: "default",
		Models: []string{"Kimi-K3"}, ModelMapping: `{"kimi-k3":"Kimi-For-Coding"}`,
	}
	require.NoError(t, repo.CreateChannel(ctx, ch))

	candidates, err := repo.ListChannelCandidates(ctx, "default", "kimi-k3")
	require.NoError(t, err)
	require.Len(t, candidates, 1)
	assert.Equal(t, "kimi-k3", candidates[0].Model)
	assert.Equal(t, "Kimi-K3", candidates[0].UpstreamModelID,
		"registry keeps the configured upstream spelling; channel.model_mapping remains authoritative")
}

func TestSyncChannelModelMappings_UpdateRemovesOnlyOwnedMappings(t *testing.T) {
	repo := setupChannelTestDB(t)
	ctx := context.Background()
	ch := &biz.Channel{
		Name: "edit-channel", Type: 1, Status: biz.ChannelStatusEnabled,
		Group: "default", Models: []string{"gpt-4o", "glm-4.6"}, Priority: 10,
	}
	require.NoError(t, repo.CreateChannel(ctx, ch))

	ch.Models = []string{"gpt-4o", "claude-opus-4-7"}
	require.NoError(t, repo.UpdateChannel(ctx, ch))

	var mappings []modelChannelMappingModel
	require.NoError(t, repo.db.Where("channel_id = ?", ch.ID).Find(&mappings).Error)
	require.Len(t, mappings, 2)
	pks := make([]int64, 0, len(mappings))
	for _, mapping := range mappings {
		pks = append(pks, mapping.ModelPK)
		assert.Equal(t, autoChannelModelMappingConfig, mapping.Config)
	}
	var surviving []modelModel
	require.NoError(t, repo.db.Where("id IN ?", pks).Find(&surviving).Error)
	gotIDs := make(map[string]bool, len(surviving))
	for _, model := range surviving {
		gotIDs[model.ModelID] = true
	}
	assert.True(t, gotIDs["gpt-4o"])
	assert.True(t, gotIDs["claude-opus-4-7"])
	assert.False(t, gotIDs["glm-4.6"])
}

func TestSyncChannelModelMappings_EmptyModelsRemovesManagedMappings(t *testing.T) {
	repo := setupChannelTestDB(t)
	ctx := context.Background()
	ch := &biz.Channel{
		Name: "empty-models", Type: 1, Status: biz.ChannelStatusEnabled,
		Group: "default", Models: []string{"gpt-4o", "claude-sonnet-4-6"},
	}
	require.NoError(t, repo.CreateChannel(ctx, ch))

	ch.Models = nil
	require.NoError(t, repo.UpdateChannel(ctx, ch))

	var count int64
	require.NoError(t, repo.db.Model(&modelChannelMappingModel{}).Where("channel_id = ?", ch.ID).Count(&count).Error)
	assert.Zero(t, count)
}

// Wildcards are routing rules, not public registry model IDs.
func TestSyncChannelModelMappings_SkipsWildcards(t *testing.T) {
	repo := setupChannelTestDB(t)
	ctx := context.Background()
	ch := &biz.Channel{
		Name: "wildcard-channel", Type: 1, Status: biz.ChannelStatusEnabled,
		Group: "default", Models: []string{"claude-*", "gpt-4o"},
	}
	require.NoError(t, repo.CreateChannel(ctx, ch))

	var modelRows []modelModel
	require.NoError(t, repo.db.Find(&modelRows).Error)
	require.Len(t, modelRows, 1)
	assert.Equal(t, "gpt-4o", modelRows[0].ModelID)
}

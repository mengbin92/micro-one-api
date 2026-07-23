package data

import (
	"context"
	"testing"

	"micro-one-api/app/channel/internal/biz"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
)

// setupModelTestDB creates an in-memory sqlite DB with the model management
// tables, mirroring migration 062.
func setupModelTestDB(t *testing.T) *Repository {
	t.Helper()
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	require.NoError(t, err)
	sqlDB, err := db.DB()
	require.NoError(t, err)
	sqlDB.SetMaxOpenConns(1)

	require.NoError(t, db.Exec(`
		CREATE TABLE models (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			model_id TEXT NOT NULL UNIQUE,
			display_name TEXT NOT NULL,
			description TEXT,
			provider TEXT NOT NULL DEFAULT '',
			model_type TEXT NOT NULL DEFAULT 'chat',
			context_window INTEGER NOT NULL DEFAULT 0,
			pricing_input REAL NOT NULL DEFAULT 0,
			pricing_output REAL NOT NULL DEFAULT 0,
			status INTEGER NOT NULL DEFAULT 1,
			is_public INTEGER NOT NULL DEFAULT 1,
			capabilities TEXT DEFAULT '[]',
			tags TEXT DEFAULT '[]',
			category TEXT NOT NULL DEFAULT '',
			tier TEXT NOT NULL DEFAULT '',
			metadata TEXT,
			created_at INTEGER NOT NULL DEFAULT 0,
			updated_at INTEGER NOT NULL DEFAULT 0
		)
	`).Error)

	require.NoError(t, db.Exec(`
		CREATE TABLE model_aliases (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			model_id INTEGER NOT NULL REFERENCES models(id) ON DELETE CASCADE,
			alias TEXT NOT NULL UNIQUE,
			is_primary INTEGER NOT NULL DEFAULT 0,
			created_at INTEGER NOT NULL DEFAULT 0
		)
	`).Error)

	require.NoError(t, db.Exec(`
		CREATE TABLE model_channel_mapping (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			channel_id INTEGER NOT NULL,
			model_id INTEGER NOT NULL REFERENCES models(id) ON DELETE CASCADE,
			enabled INTEGER NOT NULL DEFAULT 1,
			priority INTEGER NOT NULL DEFAULT 0,
			config TEXT DEFAULT '',
			created_at INTEGER NOT NULL DEFAULT 0,
			updated_at INTEGER NOT NULL DEFAULT 0
		)
	`).Error)

	require.NoError(t, db.Exec(`
		CREATE TABLE model_subscription_mapping (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			subscription_account_id INTEGER NOT NULL,
			model_id INTEGER NOT NULL REFERENCES models(id) ON DELETE CASCADE,
			group_name TEXT NOT NULL DEFAULT 'default',
			enabled INTEGER NOT NULL DEFAULT 1,
			priority INTEGER NOT NULL DEFAULT 0,
			created_at INTEGER NOT NULL DEFAULT 0,
			updated_at INTEGER NOT NULL DEFAULT 0
		)
	`).Error)

	require.NoError(t, db.Exec(`
		CREATE TABLE model_usage_stats (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			model_id INTEGER NOT NULL REFERENCES models(id) ON DELETE CASCADE,
			date TEXT NOT NULL,
			request_count INTEGER NOT NULL DEFAULT 0,
			token_count INTEGER NOT NULL DEFAULT 0,
			error_count INTEGER NOT NULL DEFAULT 0,
			avg_latency INTEGER NOT NULL DEFAULT 0
		)
	`).Error)
	require.NoError(t, db.Exec(`CREATE UNIQUE INDEX IF NOT EXISTS idx_mus_model_date ON model_usage_stats(model_id, date)`).Error)

	return &Repository{db: db}
}

func TestRepository_CreateAndGetModel(t *testing.T) {
	repo := setupModelTestDB(t)
	ctx := context.Background()

	m := &biz.Model{
		ModelID:       "gpt-4o",
		DisplayName:   "GPT-4o",
		Provider:      "openai",
		ModelType:     "chat",
		ContextWindow: 128000,
		PricingInput:  0.005,
		PricingOutput: 0.015,
		Capabilities:  []string{"vision", "function_calling"},
		Tags:          []string{"fast"},
	}
	require.NoError(t, repo.CreateModel(ctx, m))
	assert.NotZero(t, m.ID)

	got, err := repo.GetModel(ctx, m.ID)
	require.NoError(t, err)
	assert.Equal(t, "gpt-4o", got.ModelID)
	assert.Equal(t, "GPT-4o", got.DisplayName)
	assert.Equal(t, []string{"vision", "function_calling"}, got.Capabilities)
	assert.Equal(t, []string{"fast"}, got.Tags)

	byID, err := repo.GetModelByID(ctx, "gpt-4o")
	require.NoError(t, err)
	assert.Equal(t, got.ID, byID.ID)
}

func TestRepository_CreateDuplicateModel(t *testing.T) {
	repo := setupModelTestDB(t)
	ctx := context.Background()
	_ = repo.CreateModel(ctx, &biz.Model{ModelID: "dup", DisplayName: "D"})
	err := repo.CreateModel(ctx, &biz.Model{ModelID: "dup", DisplayName: "D2"})
	assert.ErrorIs(t, err, biz.ErrModelIDExists)
}

func TestRepository_UpdateModel(t *testing.T) {
	repo := setupModelTestDB(t)
	ctx := context.Background()
	m := &biz.Model{ModelID: "u", DisplayName: "U"}
	require.NoError(t, repo.CreateModel(ctx, m))

	m.DisplayName = "Updated"
	m.Description = "desc"
	m.Capabilities = []string{"streaming"}
	require.NoError(t, repo.UpdateModel(ctx, m))

	got, _ := repo.GetModel(ctx, m.ID)
	assert.Equal(t, "Updated", got.DisplayName)
	assert.Equal(t, "desc", got.Description)
	assert.Equal(t, []string{"streaming"}, got.Capabilities)
}

func TestRepository_UpdateModelNotFound(t *testing.T) {
	repo := setupModelTestDB(t)
	err := repo.UpdateModel(context.Background(), &biz.Model{ID: 999, DisplayName: "x"})
	assert.ErrorIs(t, err, biz.ErrModelNotFound)
}

func TestRepository_DeleteModel(t *testing.T) {
	repo := setupModelTestDB(t)
	ctx := context.Background()
	m := &biz.Model{ModelID: "d", DisplayName: "D"}
	require.NoError(t, repo.CreateModel(ctx, m))

	require.NoError(t, repo.DeleteModel(ctx, m.ID))
	_, err := repo.GetModel(ctx, m.ID)
	assert.ErrorIs(t, err, biz.ErrModelNotFound)
}

func TestRepository_DeleteModelNotFound(t *testing.T) {
	repo := setupModelTestDB(t)
	err := repo.DeleteModel(context.Background(), 999)
	assert.ErrorIs(t, err, biz.ErrModelNotFound)
}

func TestRepository_ChangeModelStatus(t *testing.T) {
	repo := setupModelTestDB(t)
	ctx := context.Background()
	m := &biz.Model{ModelID: "s", DisplayName: "S", Status: biz.ModelStatusEnabled}
	require.NoError(t, repo.CreateModel(ctx, m))

	require.NoError(t, repo.ChangeModelStatus(ctx, m.ID, biz.ModelStatusDisabled))
	got, _ := repo.GetModel(ctx, m.ID)
	assert.Equal(t, int32(biz.ModelStatusDisabled), got.Status)
}

func TestRepository_BatchChangeStatus(t *testing.T) {
	repo := setupModelTestDB(t)
	ctx := context.Background()
	var pks []int64
	for i := 0; i < 3; i++ {
		m := &biz.Model{ModelID: "b" + string(rune('A'+i)), DisplayName: "B"}
		require.NoError(t, repo.CreateModel(ctx, m))
		pks = append(pks, m.ID)
	}
	affected, err := repo.BatchChangeStatus(ctx, pks, biz.ModelStatusDisabled)
	require.NoError(t, err)
	assert.Equal(t, int32(3), affected)
}

func TestRepository_BatchDelete(t *testing.T) {
	repo := setupModelTestDB(t)
	ctx := context.Background()
	var pks []int64
	for i := 0; i < 2; i++ {
		m := &biz.Model{ModelID: "bd" + string(rune('A'+i)), DisplayName: "BD"}
		require.NoError(t, repo.CreateModel(ctx, m))
		pks = append(pks, m.ID)
	}
	affected, err := repo.BatchDelete(ctx, pks)
	require.NoError(t, err)
	assert.Equal(t, int32(2), affected)
}

func TestRepository_ListModelsFiltering(t *testing.T) {
	repo := setupModelTestDB(t)
	ctx := context.Background()
	require.NoError(t, repo.CreateModel(ctx, &biz.Model{ModelID: "openai-1", DisplayName: "O1", Provider: "openai", Status: biz.ModelStatusEnabled}))
	require.NoError(t, repo.CreateModel(ctx, &biz.Model{ModelID: "anthropic-1", DisplayName: "A1", Provider: "anthropic", Status: biz.ModelStatusEnabled}))
	require.NoError(t, repo.CreateModel(ctx, &biz.Model{ModelID: "openai-2", DisplayName: "O2", Provider: "openai", Status: biz.ModelStatusDisabled}))

	models, total, err := repo.ListModels(ctx, 1, 10, biz.ListModelsFilter{Provider: "openai"})
	require.NoError(t, err)
	assert.Equal(t, int64(2), total)
	assert.Len(t, models, 2)

	models, total, err = repo.ListModels(ctx, 1, 10, biz.ListModelsFilter{Provider: "openai", Status: biz.ModelStatusEnabled})
	require.NoError(t, err)
	assert.Equal(t, int64(1), total)
	assert.Len(t, models, 1)
	assert.Equal(t, "openai-1", models[0].ModelID)

	models, total, err = repo.ListModels(ctx, 1, 10, biz.ListModelsFilter{Keyword: "anthropic"})
	require.NoError(t, err)
	assert.Equal(t, int64(1), total)
	assert.Equal(t, "anthropic-1", models[0].ModelID)
}

func TestRepository_AliasCRUD(t *testing.T) {
	repo := setupModelTestDB(t)
	ctx := context.Background()
	m := &biz.Model{ModelID: "alias", DisplayName: "A"}
	require.NoError(t, repo.CreateModel(ctx, m))

	alias := &biz.ModelAlias{ModelPK: m.ID, Alias: "a", IsPrimary: true}
	require.NoError(t, repo.CreateModelAlias(ctx, alias))
	assert.NotZero(t, alias.ID)

	aliases, err := repo.ListModelAliases(ctx, m.ID)
	require.NoError(t, err)
	assert.Len(t, aliases, 1)
	assert.Equal(t, "a", aliases[0].Alias)

	require.NoError(t, repo.DeleteModelAlias(ctx, alias.ID))
	aliases, _ = repo.ListModelAliases(ctx, m.ID)
	assert.Empty(t, aliases)
}

func TestRepository_AliasDuplicate(t *testing.T) {
	repo := setupModelTestDB(t)
	ctx := context.Background()
	m := &biz.Model{ModelID: "ad", DisplayName: "AD"}
	require.NoError(t, repo.CreateModel(ctx, m))
	_ = repo.CreateModelAlias(ctx, &biz.ModelAlias{ModelPK: m.ID, Alias: "dup"})
	err := repo.CreateModelAlias(ctx, &biz.ModelAlias{ModelPK: m.ID, Alias: "dup"})
	assert.ErrorIs(t, err, biz.ErrAliasExists)
}

func TestRepository_ChannelMappingUpsert(t *testing.T) {
	repo := setupModelTestDB(t)
	ctx := context.Background()
	m := &biz.Model{ModelID: "cm", DisplayName: "CM"}
	require.NoError(t, repo.CreateModel(ctx, m))

	require.NoError(t, repo.UpsertChannelMapping(ctx, &biz.ModelChannelMapping{
		ChannelID: 10, ModelPK: m.ID, Enabled: true, Priority: 5,
	}))
	// upsert again — should update
	require.NoError(t, repo.UpsertChannelMapping(ctx, &biz.ModelChannelMapping{
		ChannelID: 10, ModelPK: m.ID, Enabled: false, Priority: 9,
	}))
	mappings, err := repo.ListChannelMappings(ctx, 10)
	require.NoError(t, err)
	assert.Len(t, mappings, 1)
	assert.False(t, mappings[0].Enabled)
	assert.Equal(t, int32(9), mappings[0].Priority)

	require.NoError(t, repo.DeleteChannelMapping(ctx, 10, m.ID))
	mappings, _ = repo.ListChannelMappings(ctx, 10)
	assert.Empty(t, mappings)
}

func TestRepository_SubscriptionMappingUpsert(t *testing.T) {
	repo := setupModelTestDB(t)
	ctx := context.Background()
	m := &biz.Model{ModelID: "sm", DisplayName: "SM"}
	require.NoError(t, repo.CreateModel(ctx, m))

	require.NoError(t, repo.UpsertSubscriptionMapping(ctx, &biz.ModelSubscriptionMapping{
		SubscriptionAccountID: 20, ModelPK: m.ID, GroupName: "default", Enabled: true,
	}))
	require.NoError(t, repo.UpsertSubscriptionMapping(ctx, &biz.ModelSubscriptionMapping{
		SubscriptionAccountID: 20, ModelPK: m.ID, GroupName: "default", Enabled: false,
	}))
	mappings, err := repo.ListSubscriptionMappings(ctx, 20)
	require.NoError(t, err)
	assert.Len(t, mappings, 1)
	assert.False(t, mappings[0].Enabled)

	require.NoError(t, repo.DeleteSubscriptionMapping(ctx, 20, m.ID, "default"))
}

func TestRepository_DeleteMappingNotFound(t *testing.T) {
	repo := setupModelTestDB(t)
	err := repo.DeleteChannelMapping(context.Background(), 1, 2)
	assert.ErrorIs(t, err, biz.ErrMappingNotFound)
}

func TestRepository_MemoryFallback(t *testing.T) {
	repo := newMemoryRepository()
	ctx := context.Background()

	m := &biz.Model{ModelID: "mem", DisplayName: "Mem", Status: biz.ModelStatusEnabled}
	require.NoError(t, repo.CreateModel(ctx, m))
	assert.NotZero(t, m.ID)

	got, err := repo.GetModel(ctx, m.ID)
	require.NoError(t, err)
	assert.Equal(t, "mem", got.ModelID)

	models, total, err := repo.ListModels(ctx, 1, 10, biz.ListModelsFilter{})
	require.NoError(t, err)
	assert.Equal(t, int64(1), total)
	assert.Len(t, models, 1)

	require.NoError(t, repo.ChangeModelStatus(ctx, m.ID, biz.ModelStatusDisabled))
	got, _ = repo.GetModel(ctx, m.ID)
	assert.Equal(t, int32(biz.ModelStatusDisabled), got.Status)

	require.NoError(t, repo.DeleteModel(ctx, m.ID))
	_, err = repo.GetModel(ctx, m.ID)
	assert.ErrorIs(t, err, biz.ErrModelNotFound)
}

// TestRepository_ListAvailableModelsDualRead verifies that ListAvailableModels
// returns models from both the legacy channel/subscription abilities AND the
// new model registry tables (Sprint 3 dual-read).
func TestRepository_ListAvailableModelsDualRead(t *testing.T) {
	repo := newMemoryRepository()
	ctx := context.Background()

	// Create a legacy channel with models.
	ch := &biz.Channel{
		ID:      1,
		Name:    "test-channel",
		Status:  biz.ChannelStatusEnabled,
		Group:   "default",
		Models:  []string{"gpt-4o", "gpt-4o-mini"},
		Priority: 0,
	}
	require.NoError(t, repo.CreateChannel(ctx, ch))

	// Create a model in the registry and a channel mapping.
	model := &biz.Model{ModelID: "claude-3-5-sonnet", DisplayName: "Claude 3.5 Sonnet", Status: biz.ModelStatusEnabled}
	require.NoError(t, repo.CreateModel(ctx, model))

	require.NoError(t, repo.UpsertChannelMapping(ctx, &biz.ModelChannelMapping{
		ChannelID: ch.ID,
		ModelPK:   model.ID,
		Enabled:   true,
	}))

	// ListAvailableModels should return both legacy and registry models.
	models, err := repo.ListAvailableModels(ctx, "default")
	require.NoError(t, err)

	seen := make(map[string]bool)
	for _, m := range models {
		seen[m] = true
	}
	assert.True(t, seen["gpt-4o"], "legacy model gpt-4o should be present")
	assert.True(t, seen["gpt-4o-mini"], "legacy model gpt-4o-mini should be present")
	assert.True(t, seen["claude-3-5-sonnet"], "registry model claude-3-5-sonnet should be present")
}

// TestRepository_ListAvailableModelsRegistryDisabledModel verifies that a
// disabled model in the registry is NOT returned by ListAvailableModels.
func TestRepository_ListAvailableModelsRegistryDisabledModel(t *testing.T) {
	repo := newMemoryRepository()
	ctx := context.Background()

	ch := &biz.Channel{
		ID:     1,
		Name:   "test-channel",
		Status: biz.ChannelStatusEnabled,
		Group:  "default",
		Models: []string{},
	}
	require.NoError(t, repo.CreateChannel(ctx, ch))

	// Create a disabled model in the registry.
	model := &biz.Model{ModelID: "disabled-model", DisplayName: "Disabled", Status: biz.ModelStatusDisabled}
	require.NoError(t, repo.CreateModel(ctx, model))

	require.NoError(t, repo.UpsertChannelMapping(ctx, &biz.ModelChannelMapping{
		ChannelID: ch.ID,
		ModelPK:   model.ID,
		Enabled:   true,
	}))

	models, err := repo.ListAvailableModels(ctx, "default")
	require.NoError(t, err)

	for _, m := range models {
		assert.NotEqual(t, "disabled-model", m, "disabled registry model should not be returned")
	}
}

// ── Sprint 4: case-insensitive model name tests ────────────────────────────

func TestRepository_CaseInsensitiveGetModelByID(t *testing.T) {
	repo := setupModelTestDB(t)
	ctx := context.Background()

	// Store with lowercase (as the biz usecase would normalise before calling repo).
	m := &biz.Model{ModelID: "glm-5.2", DisplayName: "GLM 5.2"}
	require.NoError(t, repo.CreateModel(ctx, m))

	// Lookup with different case should find the model (case-insensitive query).
	got, err := repo.GetModelByID(ctx, "GLM-5.2")
	require.NoError(t, err)
	assert.Equal(t, "glm-5.2", got.ModelID)
}

func TestRepository_CaseInsensitiveDuplicateModel(t *testing.T) {
	// DB unique constraint is case-sensitive by default; normalisation happens
	// at the biz layer. Here we verify that the normalised (lowercase) form
	// is rejected by the DB unique constraint.
	repo := setupModelTestDB(t)
	ctx := context.Background()
	_ = repo.CreateModel(ctx, &biz.Model{ModelID: "glm-5.2", DisplayName: "D"})
	err := repo.CreateModel(ctx, &biz.Model{ModelID: "glm-5.2", DisplayName: "D2"})
	assert.ErrorIs(t, err, biz.ErrModelIDExists)
}

func TestRepository_CaseInsensitiveMemoryGetModelByID(t *testing.T) {
	repo := newMemoryRepository()
	ctx := context.Background()

	// Store with lowercase (as the biz usecase would normalise).
	m := &biz.Model{ModelID: "glm-5.2", DisplayName: "GLM 5.2", Status: biz.ModelStatusEnabled}
	require.NoError(t, repo.CreateModel(ctx, m))

	// Lookup with different case should find the model (case-insensitive memory lookup).
	got, err := repo.GetModelByID(ctx, "GLM-5.2")
	require.NoError(t, err)
	assert.Equal(t, "glm-5.2", got.ModelID)
}

func TestRepository_CaseInsensitiveMemoryDuplicateModel(t *testing.T) {
	repo := newMemoryRepository()
	ctx := context.Background()
	_ = repo.CreateModel(ctx, &biz.Model{ModelID: "GLM-5.2", DisplayName: "D", Status: biz.ModelStatusEnabled})
	// Creating with different case should fail.
	err := repo.CreateModel(ctx, &biz.Model{ModelID: "glm-5.2", DisplayName: "D2", Status: biz.ModelStatusEnabled})
	assert.ErrorIs(t, err, biz.ErrModelIDExists)
}

func TestRepository_CaseInsensitiveListAvailableModels(t *testing.T) {
	repo := newMemoryRepository()
	ctx := context.Background()

	// Channel with uppercase model name.
	ch := &biz.Channel{
		ID:     1,
		Name:   "test-channel",
		Status: biz.ChannelStatusEnabled,
		Group:  "default",
		Models: []string{"GLM-5.2"},
	}
	require.NoError(t, repo.CreateChannel(ctx, ch))

	// Another channel with lowercase model name.
	ch2 := &biz.Channel{
		ID:     2,
		Name:   "test-channel-2",
		Status: biz.ChannelStatusEnabled,
		Group:  "default",
		Models: []string{"glm-5.2"},
	}
	require.NoError(t, repo.CreateChannel(ctx, ch2))

	// ListAvailableModels should return only one entry (deduplicated).
	models, err := repo.ListAvailableModels(ctx, "default")
	require.NoError(t, err)

	count := 0
	for _, m := range models {
		if m == "glm-5.2" {
			count++
		}
	}
	assert.Equal(t, 1, count, "GLM-5.2 and glm-5.2 should be deduplicated to a single entry")
}

// ── Sprint 4: Usage statistics tests ───────────────────────────────────────

func TestRepository_RecordAndListModelUsageStats(t *testing.T) {
	repo := setupModelTestDB(t)
	ctx := context.Background()

	m := &biz.Model{ModelID: "usage-model", DisplayName: "UM"}
	require.NoError(t, repo.CreateModel(ctx, m))

	// Record first usage.
	require.NoError(t, repo.RecordModelUsage(ctx, m.ID, &biz.ModelUsageStat{
		ModelPK:      m.ID,
		Date:         "2026-07-23",
		RequestCount: 1,
		TokenCount:   100,
		AvgLatency:   50,
	}))

	// Record second usage for the same day — should accumulate.
	require.NoError(t, repo.RecordModelUsage(ctx, m.ID, &biz.ModelUsageStat{
		ModelPK:      m.ID,
		Date:         "2026-07-23",
		RequestCount: 1,
		TokenCount:   200,
		AvgLatency:   60,
	}))

	stats, total, err := repo.ListModelUsageStats(ctx, m.ID, "", "", 1, 10)
	require.NoError(t, err)
	assert.Equal(t, int64(1), total)
	assert.Len(t, stats, 1)
	assert.Equal(t, int32(2), stats[0].RequestCount)
	assert.Equal(t, int64(300), stats[0].TokenCount)
	assert.Equal(t, int32(60), stats[0].AvgLatency)
}

func TestRepository_ModelUsageStatsMemory(t *testing.T) {
	repo := newMemoryRepository()
	ctx := context.Background()

	m := &biz.Model{ModelID: "mem-usage", DisplayName: "MU", Status: biz.ModelStatusEnabled}
	require.NoError(t, repo.CreateModel(ctx, m))

	require.NoError(t, repo.RecordModelUsage(ctx, m.ID, &biz.ModelUsageStat{
		ModelPK:      m.ID,
		Date:         "2026-07-23",
		RequestCount: 1,
		TokenCount:   100,
	}))

	require.NoError(t, repo.RecordModelUsage(ctx, m.ID, &biz.ModelUsageStat{
		ModelPK:      m.ID,
		Date:         "2026-07-23",
		RequestCount: 1,
		TokenCount:   200,
	}))

	stats, total, err := repo.ListModelUsageStats(ctx, m.ID, "", "", 1, 10)
	require.NoError(t, err)
	assert.Equal(t, int64(1), total)
	assert.Len(t, stats, 1)
	assert.Equal(t, int32(2), stats[0].RequestCount)
	assert.Equal(t, int64(300), stats[0].TokenCount)
}

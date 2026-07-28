package data

import (
	"context"
	"testing"

	"micro-one-api/app/channel/internal/biz"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// canonicalMergeFixture seeds a DB-backed repo with a case-only duplicate
// group ("GLM-5.2" + "glm-5.2") and returns the PKs so tests can refer to
// them. The fixture intentionally stores the canonical spelling on the SECOND
// row so the survivor default (primary-spelling member) is not just row 1.
func canonicalMergeFixture(t *testing.T, r *Repository) (upperPK, lowerPK int64) {
	t.Helper()
	ctx := context.Background()
	// Insert two rows that collide after NormalizeModelID. SQLite's UNIQUE is
	// case-sensitive (BINARY collation) so both rows insert.
	require.NoError(t, r.CreateModel(ctx, &biz.Model{ModelID: "GLM-5.2", DisplayName: "GLM", Provider: "zhipu"}))
	require.NoError(t, r.CreateModel(ctx, &biz.Model{ModelID: "glm-5.2", DisplayName: "glm", Provider: "zhipu"}))
	// NOTE: GetModelByID is case-insensitive, so fetch by row order to tell
	// the two stored spellings apart.
	models, _, err := r.ListModels(ctx, 1, 50, biz.ListModelsFilter{})
	require.NoError(t, err)
	require.Len(t, models, 2)
	for _, m := range models {
		if m.ModelID == "GLM-5.2" {
			upperPK = m.ID
		} else if m.ModelID == "glm-5.2" {
			lowerPK = m.ID
		}
	}
	require.NotZero(t, upperPK, "upper (GLM-5.2) row not seeded")
	require.NotZero(t, lowerPK, "lower (glm-5.2) row not seeded")
	require.NotEqual(t, upperPK, lowerPK)
	return upperPK, lowerPK
}

func TestCanonicalPreflight_DetectsCaseOnlyDuplicate(t *testing.T) {
	r := setupModelTestDB(t)
	ctx := context.Background()
	upperPK, lowerPK := canonicalMergeFixture(t, r)

	// Attach a few dependents to verify the counts.
	require.NoError(t, r.CreateModelAlias(ctx, &biz.ModelAlias{ModelPK: upperPK, Alias: "glm52-upper"}))
	require.NoError(t, r.UpsertChannelMapping(ctx, &biz.ModelChannelMapping{ChannelID: 1, ModelPK: upperPK, EnabledHasValue: true}))
	require.NoError(t, r.UpsertSubscriptionMapping(ctx, &biz.ModelSubscriptionMapping{SubscriptionAccountID: 7, ModelPK: lowerPK, EnabledHasValue: true}))
	require.NoError(t, r.RecordModelUsage(ctx, lowerPK, &biz.ModelUsageStat{Date: "2026-07-01", RequestCount: 5, TokenCount: 100}))

	report, err := r.CanonicalModelPreflight(ctx)
	require.NoError(t, err)
	require.Len(t, report.Groups, 1)
	g := report.Groups[0]
	assert.Equal(t, "glm-5.2", g.CanonicalID)
	require.Len(t, g.Members, 2)

	byPK := map[int64]biz.DuplicateModelRef{}
	for _, m := range g.Members {
		byPK[m.ModelPK] = m
	}
	assert.Equal(t, "GLM-5.2", byPK[upperPK].ModelID)
	assert.False(t, byPK[upperPK].IsPrimary)
	assert.EqualValues(t, 1, byPK[upperPK].Aliases)
	assert.EqualValues(t, 1, byPK[upperPK].ChannelMappings)

	assert.True(t, byPK[lowerPK].IsPrimary)
	assert.EqualValues(t, 1, byPK[lowerPK].SubscriptionMappings)
	assert.EqualValues(t, 1, byPK[lowerPK].UsageStatDays)
	assert.EqualValues(t, 5, byPK[lowerPK].UsageRequestTotal)
}

func TestCanonicalPreflight_CleanRegistryIsEmpty(t *testing.T) {
	r := setupModelTestDB(t)
	ctx := context.Background()
	require.NoError(t, r.CreateModel(ctx, &biz.Model{ModelID: "gpt-4o", DisplayName: "gpt"}))
	require.NoError(t, r.CreateModel(ctx, &biz.Model{ModelID: "claude-3", DisplayName: "claude"}))

	report, err := r.CanonicalModelPreflight(ctx)
	require.NoError(t, err)
	assert.Empty(t, report.Groups)
}

func TestMergeCanonicalModels_RepointsDependentsAndDeletesLoser(t *testing.T) {
	r := setupModelTestDB(t)
	ctx := context.Background()
	upperPK, lowerPK := canonicalMergeFixture(t, r)
	t.Logf("fixture: upperPK=%d lowerPK=%d", upperPK, lowerPK)

	require.NoError(t, r.CreateModelAlias(ctx, &biz.ModelAlias{ModelPK: upperPK, Alias: "glm-alias"}))
	require.NoError(t, r.UpsertChannelMapping(ctx, &biz.ModelChannelMapping{ChannelID: 1, ModelPK: upperPK, EnabledHasValue: true}))
	require.NoError(t, r.UpsertChannelMapping(ctx, &biz.ModelChannelMapping{ChannelID: 2, ModelPK: lowerPK, EnabledHasValue: true}))
	require.NoError(t, r.UpsertSubscriptionMapping(ctx, &biz.ModelSubscriptionMapping{SubscriptionAccountID: 7, ModelPK: lowerPK, EnabledHasValue: true}))
	require.NoError(t, r.RecordModelUsage(ctx, upperPK, &biz.ModelUsageStat{Date: "2026-07-01", RequestCount: 3, TokenCount: 30}))
	require.NoError(t, r.RecordModelUsage(ctx, lowerPK, &biz.ModelUsageStat{Date: "2026-07-01", RequestCount: 5, TokenCount: 50}))
	require.NoError(t, r.RecordModelUsage(ctx, lowerPK, &biz.ModelUsageStat{Date: "2026-07-02", RequestCount: 2, TokenCount: 20}))

	report, err := r.CanonicalModelPreflight(ctx)
	require.NoError(t, err)
	require.Len(t, report.Groups, 1)
	group := report.Groups[0]
	t.Logf("report group: canonical=%s surviving(before set)=%d members=%v", group.CanonicalID, group.SurvivingPK, len(group.Members))
	group.SurvivingPK = lowerPK // keep the canonical-spelling row
	t.Logf("report group: surviving(after set)=%d", group.SurvivingPK)

	res, err := r.MergeCanonicalModels(ctx, group)
	require.NoError(t, err)
	assert.Equal(t, "glm-5.2", res.CanonicalID)
	assert.Equal(t, lowerPK, res.SurvivingPK)
	assert.Equal(t, []int64{upperPK}, res.MergedModelPKs)
	// Only the LOSER's (upperPK) dependents are re-pointed. The survivor's
	// own rows stay put, so the counts reflect the loser's payload: 1 alias,
	// 1 channel mapping, 0 subscription mappings (the sub mapping was seeded
	// on the survivor), and 1 usage-stat row (07-01).
	assert.EqualValues(t, 1, res.AliasesRepointed)
	assert.EqualValues(t, 1, res.ChannelMappingsRepointed)
	assert.EqualValues(t, 0, res.SubscriptionMappingsRepointed)
	assert.EqualValues(t, 1, res.UsageStatsRepointed)

	// Loser is gone.
	_, err = r.GetModel(ctx, upperPK)
	assert.ErrorIs(t, err, biz.ErrModelNotFound)

	// Survivor carries the canonical spelling.
	surv, err := r.GetModel(ctx, lowerPK)
	require.NoError(t, err)
	assert.Equal(t, "glm-5.2", surv.ModelID)

	// Aliases and mappings all point at the survivor now.
	aliases, err := r.ListModelAliases(ctx, lowerPK)
	require.NoError(t, err)
	require.Len(t, aliases, 1)
	assert.Equal(t, "glm-alias", aliases[0].Alias)

	chs, err := r.ListChannelMappings(ctx, 0)
	require.NoError(t, err)
	assert.Len(t, chs, 2)
	for _, c := range chs {
		assert.Equal(t, lowerPK, c.ModelPK, "channel %d not re-pointed to survivor", c.ChannelID)
	}

	subs, err := r.ListSubscriptionMappings(ctx, 0)
	require.NoError(t, err)
	require.Len(t, subs, 1)
	assert.Equal(t, lowerPK, subs[0].ModelPK)

	// Usage stats after merge: 07-01 accumulated (loser 3/30 folded into
	// survivor 5/50 => 8 req, 80 tok), 07-02 stays on the survivor (2/20).
	stats, _, err := r.ListModelUsageStats(ctx, lowerPK, "", "", 1, 50)
	require.NoError(t, err)
	require.Len(t, stats, 2)
	byDate := map[string]*biz.ModelUsageStat{}
	for i := range stats {
		byDate[stats[i].Date] = stats[i]
	}
	assert.EqualValues(t, 8, byDate["2026-07-01"].RequestCount)
	assert.EqualValues(t, 80, byDate["2026-07-01"].TokenCount)
	assert.EqualValues(t, 2, byDate["2026-07-02"].RequestCount)
	assert.EqualValues(t, 20, byDate["2026-07-02"].TokenCount)

	// After merge, preflight is clean.
	clean, err := r.CanonicalModelPreflight(ctx)
	require.NoError(t, err)
	assert.Empty(t, clean.Groups)
}

func TestMergeCanonicalModels_AbortsOnSubscriptionMappingCollision(t *testing.T) {
	r := setupModelTestDB(t)
	ctx := context.Background()
	upperPK, lowerPK := canonicalMergeFixture(t, r)

	// Both members bind the SAME subscription account under the SAME group —
	// re-pointing would violate uk_account_model_group
	// (subscription_account_id, model_id, group_name).
	require.NoError(t, r.UpsertSubscriptionMapping(ctx, &biz.ModelSubscriptionMapping{SubscriptionAccountID: 7, GroupName: "default", ModelPK: upperPK, EnabledHasValue: true}))
	require.NoError(t, r.UpsertSubscriptionMapping(ctx, &biz.ModelSubscriptionMapping{SubscriptionAccountID: 7, GroupName: "default", ModelPK: lowerPK, EnabledHasValue: true}))

	report, err := r.CanonicalModelPreflight(ctx)
	require.NoError(t, err)
	require.Len(t, report.Groups, 1)
	group := report.Groups[0]
	group.SurvivingPK = lowerPK

	_, err = r.MergeCanonicalModels(ctx, group)
	require.Error(t, err)
	assert.ErrorIs(t, err, biz.ErrCanonicalConflict)

	// Transaction rolled back: both rows still exist with their mappings.
	_, err = r.GetModel(ctx, upperPK)
	assert.NoError(t, err)
	_, err = r.GetModel(ctx, lowerPK)
	assert.NoError(t, err)
}

func TestMergeCanonicalModels_AliasCollisionImpossibleUnderUniqueKey(t *testing.T) {
	// Documented invariant: model_aliases.alias has a global UNIQUE key, so
	// two group members can never share an alias string in the first place.
	// The DB rejects the second insert with ErrAliasExists long before the
	// merge runs. detectAliasKeyCollision therefore stays as defence-in-depth
	// (e.g. for a schema without uk_alias); this test pins the real behaviour.
	r := setupModelTestDB(t)
	ctx := context.Background()
	upperPK, _ := canonicalMergeFixture(t, r)

	require.NoError(t, r.CreateModelAlias(ctx, &biz.ModelAlias{ModelPK: upperPK, Alias: "shared"}))
	err := r.CreateModelAlias(ctx, &biz.ModelAlias{ModelPK: upperPK, Alias: "shared"})
	assert.ErrorIs(t, err, biz.ErrAliasExists)
}

func TestMergeCanonicalModels_AbortsOnChannelMappingCollision(t *testing.T) {
	r := setupModelTestDB(t)
	ctx := context.Background()
	upperPK, lowerPK := canonicalMergeFixture(t, r)

	// Both members serve the SAME channel — re-pointing would violate
	// uk_channel_model (channel_id, model_id).
	require.NoError(t, r.UpsertChannelMapping(ctx, &biz.ModelChannelMapping{ChannelID: 1, ModelPK: upperPK, EnabledHasValue: true}))
	require.NoError(t, r.UpsertChannelMapping(ctx, &biz.ModelChannelMapping{ChannelID: 1, ModelPK: lowerPK, EnabledHasValue: true}))

	report, err := r.CanonicalModelPreflight(ctx)
	require.NoError(t, err)
	require.Len(t, report.Groups, 1)
	group := report.Groups[0]
	group.SurvivingPK = lowerPK

	_, err = r.MergeCanonicalModels(ctx, group)
	require.Error(t, err)
	assert.ErrorIs(t, err, biz.ErrCanonicalConflict)
	_, err = r.GetModel(ctx, upperPK)
	assert.NoError(t, err)
}

func TestMergeCanonicalModels_RejectsUnnormalisedCanonicalID(t *testing.T) {
	uc := biz.NewModelUsecase(nil)
	// The biz layer must reject a caller who tries to rename mid-merge.
	_, err := uc.MergeCanonicalModels(context.Background(), biz.DuplicateModelGroup{
		CanonicalID: "GLM-5.2", // not normalised
		Members:     []biz.DuplicateModelRef{{ModelPK: 1}, {ModelPK: 2}},
	})
	require.Error(t, err)
}

// ── memory-mode parity ─────────────────────────────────────────────────────
// The memory fallback enforces EqualFold dedup in createModelMemory, so by
// construction it can never hold a case-only duplicate. These tests pin that
// invariant: preflight is always clean on a memory store, and merge of an
// empty group is a no-op. The DB-backed path (above) covers the real merge.

func TestCanonicalPreflight_MemoryIsCleanByConstruction(t *testing.T) {
	r := newMemoryRepository()
	ctx := context.Background()
	require.NoError(t, r.CreateModel(ctx, &biz.Model{ModelID: "GLM-5.2", DisplayName: "GLM"}))
	// Case-only duplicate is REJECTED by the memory store's EqualFold check.
	err := r.CreateModel(ctx, &biz.Model{ModelID: "glm-5.2", DisplayName: "glm"})
	assert.ErrorIs(t, err, biz.ErrModelIDExists)

	report, err := r.CanonicalModelPreflight(ctx)
	require.NoError(t, err)
	assert.Empty(t, report.Groups, "memory store cannot hold case-only duplicates")
}

func TestMergeCanonicalModels_MemoryEmptyGroupIsNoop(t *testing.T) {
	r := newMemoryRepository()
	ctx := context.Background()
	// Seed one model so the survivor exists; a group with only that one member
	// has nothing to merge.
	require.NoError(t, r.CreateModel(ctx, &biz.Model{ModelID: "glm-5.2", DisplayName: "glm"}))
	models, _, err := r.ListModels(ctx, 1, 10, biz.ListModelsFilter{})
	require.NoError(t, err)
	require.Len(t, models, 1)
	res, err := r.MergeCanonicalModels(ctx, biz.DuplicateModelGroup{
		CanonicalID: "glm-5.2",
		SurvivingPK: models[0].ID,
		Members:     []biz.DuplicateModelRef{{ModelPK: models[0].ID, ModelID: "glm-5.2"}},
	})
	require.NoError(t, err)
	assert.NotNil(t, res)
	assert.Empty(t, res.MergedModelPKs)
}

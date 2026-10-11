package data

import (
	"context"
	"strings"
	"testing"

	"micro-one-api/app/channel/internal/biz"
	"micro-one-api/domain/authorization"
	authztest "micro-one-api/domain/authorization/testutil"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

// Usage recording needs the model identity, not the admin detail projection.
// Drive the real usecase/repository path so a detail lookup cannot silently add
// mapping aggregates or source-availability scans to every relay request.
func TestModelUsecase_RecordUsageReadsOnlyModelIdentity(t *testing.T) {
	repo := setupModelTestDB(t)
	ctx := context.Background()
	model := &biz.Model{ModelID: "glm-5.3", DisplayName: "GLM", Status: biz.ModelStatusEnabled}
	require.NoError(t, repo.CreateModel(ctx, model))

	var reads []string
	recordRead := func(db *gorm.DB) {
		reads = append(reads, db.Statement.SQL.String())
	}
	require.NoError(t, repo.db.Callback().Query().After("gorm:query").Register("test:usage_identity_reads", recordRead))
	// Scan/Rows uses the Row callback rather than Query; the supplier
	// aggregates must be observed too.
	require.NoError(t, repo.db.Callback().Row().After("gorm:row").Register("test:usage_identity_reads", recordRead))
	t.Cleanup(func() {
		_ = repo.db.Callback().Query().Remove("test:usage_identity_reads")
		_ = repo.db.Callback().Row().Remove("test:usage_identity_reads")
	})

	uc := biz.NewModelUsecase(repo)
	require.NoError(t, uc.RecordModelUsage(ctx, "GLM-5.3", 1, 123, 0, 25, "2026-10-09"))
	for _, query := range reads {
		for _, table := range []string{"model_channel_mapping", "model_subscription_mapping", "channels", "subscription_accounts"} {
			assert.NotContains(t, query, table, "usage must not read source metadata")
		}
	}
	require.Len(t, reads, 2, "one model identity read and one daily usage read")
	assert.True(t, strings.HasPrefix(reads[0], "SELECT `id` FROM `models`"), "identity lookup must select only id: %s", reads[0])

	stats, total, err := repo.ListModelUsageStats(ctx, model.ID, "", "", 1, 10)
	require.NoError(t, err)
	require.EqualValues(t, 1, total)
	require.Len(t, stats, 1)
	assert.Equal(t, model.ID, stats[0].ModelPK)
	assert.EqualValues(t, 123, stats[0].TokenCount)
}

func TestModelUsecase_RecordUsageIdentityErrorSemantics(t *testing.T) {
	repo := setupModelTestDB(t)
	ctx := context.Background()
	model := &biz.Model{ModelID: "glm-5.3", DisplayName: "GLM"}
	require.NoError(t, repo.CreateModel(ctx, model))
	uc := biz.NewModelUsecase(repo)
	record := func(current context.Context, id string) error {
		return uc.RecordModelUsage(current, id, 1, 123, 0, 25, "2026-10-09")
	}

	// An unregistered model is still a best-effort drop; a denied read and
	// a storage failure must be returned and must not record usage.
	require.NoError(t, record(ctx, "unknown"))
	denied := authorization.WithQueryScope(ctx, "channel.model.read", authorization.QueryScope{ActorID: 1})
	require.ErrorIs(t, record(denied, model.ModelID), authorization.ErrDenied)
	canceled, cancel := context.WithCancel(ctx)
	cancel()
	require.ErrorIs(t, record(canceled, model.ModelID), context.Canceled)
	stats, total, err := repo.ListModelUsageStats(ctx, model.ID, "", "", 1, 10)
	require.NoError(t, err)
	assert.Zero(t, total)
	assert.Empty(t, stats)

	allowed := authorization.WithQueryScope(ctx, "channel.model.read", authztest.Resources(model.ID))
	require.NoError(t, record(allowed, "GLM-5.3"))
	stats, total, err = repo.ListModelUsageStats(ctx, model.ID, "", "", 1, 10)
	require.NoError(t, err)
	require.EqualValues(t, 1, total)
	require.Len(t, stats, 1)
	assert.EqualValues(t, 1, stats[0].RequestCount)
}

func TestModelUsecase_RecordUsageMemoryIdentity(t *testing.T) {
	repo := newMemoryRepository()
	ctx := context.Background()
	model := &biz.Model{ModelID: "glm-5.3", DisplayName: "GLM"}
	require.NoError(t, repo.CreateModel(ctx, model))
	uc := biz.NewModelUsecase(repo)
	require.NoError(t, uc.RecordModelUsage(ctx, "GLM-5.3", 1, 123, 0, 25, "2026-10-09"))
	require.NoError(t, uc.RecordModelUsage(ctx, "unknown", 1, 123, 0, 25, "2026-10-09"))
	stats, total, err := repo.ListModelUsageStats(ctx, model.ID, "", "", 1, 10)
	require.NoError(t, err)
	require.EqualValues(t, 1, total)
	require.Len(t, stats, 1)
	assert.Equal(t, model.ID, stats[0].ModelPK)
	assert.EqualValues(t, 123, stats[0].TokenCount)
}

// Embed only ModelRepo to emulate implementations without the optional seam.
type modelUsageLegacyRepo struct {
	biz.ModelRepo
	model       *biz.Model
	lookupCount int
}

func (r *modelUsageLegacyRepo) GetModelByID(_ context.Context, modelID string) (*biz.Model, error) {
	r.lookupCount++
	if modelID != r.model.ModelID {
		return nil, biz.ErrModelNotFound
	}
	return r.model, nil
}

func TestModelUsecase_RecordUsageLegacyRepoFallback(t *testing.T) {
	repo := newMemoryRepository()
	ctx := context.Background()
	model := &biz.Model{ModelID: "glm-5.3", DisplayName: "GLM"}
	require.NoError(t, repo.CreateModel(ctx, model))
	legacy := &modelUsageLegacyRepo{ModelRepo: repo, model: model}
	uc := biz.NewModelUsecase(legacy)
	require.NoError(t, uc.RecordModelUsage(ctx, "GLM-5.3", 1, 123, 0, 25, "2026-10-09"))
	assert.Equal(t, 1, legacy.lookupCount)
	stats, total, err := repo.ListModelUsageStats(ctx, model.ID, "", "", 1, 10)
	require.NoError(t, err)
	require.EqualValues(t, 1, total)
	require.Len(t, stats, 1)
	assert.EqualValues(t, 123, stats[0].TokenCount)
}

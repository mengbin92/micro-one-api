package data

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"

	"micro-one-api/app/channel/internal/biz"
)

// F26: source+model isolation — the consecutive-ambiguous threshold trips a
// persisted block; verified verdicts reset the counter; resolve clears the
// persisted row; the blocked set is re-readable (i.e. survives "restarts").
func TestUsageSemanticSourceBlocks_QuarantineLifecycle(t *testing.T) {
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	require.NoError(t, err)
	require.NoError(t, db.AutoMigrate(&usageSemanticSourceBlockModel{}))
	repo := &Repository{db: db}
	ctx := context.Background()
	now := time.Date(2026, 8, 31, 12, 0, 0, 0, time.UTC)
	window := 5 * time.Minute
	blockDuration := 15 * time.Minute
	threshold := int32(3)

	ambiguous := biz.UsageSemanticVerdict{
		SourceKind:      biz.UsageSemanticSourceKindChannel,
		SourceID:        9,
		UpstreamModelID: "step-explore",
		AdapterProtocol: "openai_chat",
		ParseStatus:     "ambiguous",
		Reason:          "cached_exceeds_reported_prompt",
	}

	// Two ambiguous verdicts inside the window: no block yet.
	for i := 0; i < 2; i++ {
		row, err := repo.UpsertUsageSemanticVerdict(ctx, ambiguous, window, blockDuration, threshold, now)
		require.NoError(t, err)
		assert.Equal(t, biz.UsageSemanticBlockStatusActive, row.Status)
		assert.Equal(t, int32(i+1), row.ConsecutiveAmbiguous)
	}

	// A verified verdict resets the consecutive counter.
	verified := ambiguous
	verified.ParseStatus = "verified"
	row, err := repo.UpsertUsageSemanticVerdict(ctx, verified, window, blockDuration, threshold, now.Add(time.Minute))
	require.NoError(t, err)
	assert.Equal(t, int32(0), row.ConsecutiveAmbiguous)
	assert.False(t, row.LastVerifiedAt.IsZero())

	// Three consecutive ambiguous verdicts trip the block.
	blockedUntil := time.Time{}
	for i := 0; i < 3; i++ {
		row, err = repo.UpsertUsageSemanticVerdict(ctx, ambiguous, window, blockDuration, threshold, now.Add(2*time.Minute))
		require.NoError(t, err)
		if i == 2 {
			assert.Equal(t, biz.UsageSemanticBlockStatusBlocked, row.Status)
			blockedUntil = row.BlockedUntil
		}
	}
	assert.Equal(t, now.Add(2*time.Minute).Add(blockDuration), blockedUntil)

	// The blocked set is re-readable from the DB (cross-instance/restart
	// authority), and an unrelated key is not blocked.
	blocked, err := repo.ListBlockedUsageSemanticBlocks(ctx, now.Add(3*time.Minute))
	require.NoError(t, err)
	require.Len(t, blocked, 1)
	assert.Equal(t, int64(9), blocked[0].SourceID)
	assert.Equal(t, "step-explore", blocked[0].UpstreamModelID)

	// After blocked_until expires the key no longer filters.
	blocked, err = repo.ListBlockedUsageSemanticBlocks(ctx, blockedUntil.Add(time.Second))
	require.NoError(t, err)
	assert.Empty(t, blocked)

	// Manual resolve clears the persisted row.
	resolved, err := repo.ResolveUsageSemanticBlock(ctx, "channel", 9, "step-explore", "openai_chat", now)
	require.NoError(t, err)
	assert.True(t, resolved)
	resolved, err = repo.ResolveUsageSemanticBlock(ctx, "channel", 9, "step-explore", "openai_chat", now)
	require.NoError(t, err)
	assert.False(t, resolved, "second resolve finds no active row")
}

// Window expiry: ambiguous verdicts spread beyond the window never reach the
// threshold.
func TestUsageSemanticSourceBlocks_WindowExpiry(t *testing.T) {
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	require.NoError(t, err)
	require.NoError(t, db.AutoMigrate(&usageSemanticSourceBlockModel{}))
	repo := &Repository{db: db}
	ctx := context.Background()
	now := time.Date(2026, 8, 31, 12, 0, 0, 0, time.UTC)

	ambiguous := biz.UsageSemanticVerdict{
		SourceKind: "channel", SourceID: 9, UpstreamModelID: "m",
		ParseStatus: "ambiguous", Reason: "r",
	}
	row, err := repo.UpsertUsageSemanticVerdict(ctx, ambiguous, 5*time.Minute, 15*time.Minute, 3, now)
	require.NoError(t, err)
	assert.Equal(t, int32(1), row.ConsecutiveAmbiguous)

	// 6 minutes later the window restarts instead of accumulating.
	row, err = repo.UpsertUsageSemanticVerdict(ctx, ambiguous, 5*time.Minute, 15*time.Minute, 3, now.Add(6*time.Minute))
	require.NoError(t, err)
	assert.Equal(t, int32(1), row.ConsecutiveAmbiguous)
	assert.Equal(t, biz.UsageSemanticBlockStatusActive, row.Status)
}

func TestUsageSemanticSourceBlocks_VerifiedDoesNotCreateHealthyRow(t *testing.T) {
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	require.NoError(t, err)
	require.NoError(t, db.AutoMigrate(&usageSemanticSourceBlockModel{}))
	repo := &Repository{db: db}
	row, err := repo.UpsertUsageSemanticVerdict(context.Background(), biz.UsageSemanticVerdict{
		SourceKind: "channel", SourceID: 1, UpstreamModelID: "m", ParseStatus: "verified",
	}, 5*time.Minute, 15*time.Minute, 3, time.Now())
	require.NoError(t, err)
	assert.Nil(t, row)
	var count int64
	require.NoError(t, db.Model(&usageSemanticSourceBlockModel{}).Count(&count).Error)
	assert.Zero(t, count)
}

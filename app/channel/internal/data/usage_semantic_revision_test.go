package data

import (
	"context"
	"math"
	"strconv"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"micro-one-api/app/channel/internal/biz"
	"micro-one-api/domain/authorization"
	authztest "micro-one-api/domain/authorization/testutil"
	dbtest "micro-one-api/platform/database/testutil"
)

func TestIAMB2UsageSemanticRejectsWrappedRevision(t *testing.T) {
	db := dbtest.RoutingContextDB(t, "sqlite")
	repo := &Repository{db: db}
	now := time.Now().UTC()
	block, err := repo.UpsertUsageSemanticVerdict(context.Background(), biz.UsageSemanticVerdict{SourceKind: "channel", SourceID: 9, UpstreamModelID: "upstream", AdapterProtocol: "openai", ParseStatus: "ambiguous", Reason: "changed"}, time.Minute, time.Hour, 1, now)
	require.NoError(t, err)
	require.NotNil(t, block)
	require.NoError(t, db.Model(&usageSemanticSourceBlockModel{}).Where("id = ?", block.ID).Update("revision", -1).Error)
	ctx := authorization.WithQueryScope(authorization.WithWriteReason(context.Background(), "reject corrupted revision"), "channel.usage_semantic_block.resolve", authztest.Resources(block.ID))
	ctx = authorization.WithExpectedResourceRevision(ctx, math.MaxUint64)
	resolved, err := repo.ResolveUsageSemanticBlock(ctx, block.SourceKind, block.SourceID, block.UpstreamModelID, block.AdapterProtocol, now)
	require.ErrorIs(t, err, authorization.ErrWriteConflict)
	require.False(t, resolved)
	var stored usageSemanticSourceBlockModel
	require.NoError(t, db.First(&stored, block.ID).Error)
	require.Equal(t, biz.UsageSemanticBlockStatusBlocked, stored.Status)
}

func TestIAMB2UsageSemanticResolveRevisionDialects(t *testing.T) {
	for _, driver := range []string{"sqlite", "mysql", "postgres"} {
		t.Run(driver, func(t *testing.T) {
			db := dbtest.RoutingContextDB(t, driver)
			repo := &Repository{db: db}
			base := context.Background()
			now := time.Now().UTC()
			block, err := repo.UpsertUsageSemanticVerdict(base, biz.UsageSemanticVerdict{SourceKind: "channel", SourceID: 9, UpstreamModelID: "upstream", AdapterProtocol: "openai", ParseStatus: "ambiguous", Reason: "provider response changed"}, time.Minute, time.Hour, 1, now)
			require.NoError(t, err)
			require.NotNil(t, block)
			require.EqualValues(t, 2, block.Revision)
			ctx := authorization.WithWriteReason(base, "resolve quarantined adapter block")
			ctx = authorization.WithQueryScope(ctx, "channel.usage_semantic_block.resolve", authztest.Resources(block.ID))
			resolved, err := repo.ResolveUsageSemanticBlock(ctx, block.SourceKind, block.SourceID, block.UpstreamModelID, block.AdapterProtocol, now.Add(time.Second))
			require.False(t, resolved)
			require.ErrorIs(t, err, authorization.ErrWritePrecondition)
			ctx = authorization.WithExpectedResourceRevision(ctx, uint64(block.Revision-1))
			resolved, err = repo.ResolveUsageSemanticBlock(ctx, block.SourceKind, block.SourceID, block.UpstreamModelID, block.AdapterProtocol, now.Add(time.Second))
			require.False(t, resolved)
			require.ErrorIs(t, err, authorization.ErrWriteConflict)
			ctx = authorization.WithExpectedResourceRevision(ctx, uint64(block.Revision))
			resolved, err = repo.ResolveUsageSemanticBlock(ctx, block.SourceKind, block.SourceID, block.UpstreamModelID, block.AdapterProtocol, now.Add(time.Second))
			require.NoError(t, err)
			require.True(t, resolved)
			var stored usageSemanticSourceBlockModel
			require.NoError(t, db.First(&stored, block.ID).Error)
			require.Equal(t, biz.UsageSemanticBlockStatusResolved, stored.Status)
			require.EqualValues(t, block.Revision+1, stored.Revision)
			var audits int64
			require.NoError(t, db.Table("resource_write_audits").Where("operation = ? AND resource_id = ? AND result = ?", "channel.usage_semantic_block.resolve", strconv.FormatInt(block.ID, 10), "success").Count(&audits).Error)
			require.EqualValues(t, 1, audits)
		})
	}
}

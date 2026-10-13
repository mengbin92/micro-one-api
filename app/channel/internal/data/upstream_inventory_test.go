package data

import (
	"context"
	"github.com/stretchr/testify/require"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
	"micro-one-api/app/channel/internal/biz"
	"micro-one-api/platform/database/testutil"
	"path/filepath"
	"testing"
	"time"
)

func TestInventoryRefreshKeepsManualAuthorizationAndExactIDs(t *testing.T) {
	db, err := gorm.Open(sqlite.Open(filepath.Join(t.TempDir(), "inventory.db")), &gorm.Config{})
	require.NoError(t, err)
	require.NoError(t, db.AutoMigrate(&upstreamInventoryModel{}, &channelModel{}))
	sql, _ := db.DB()
	t.Cleanup(func() { _ = sql.Close() })
	require.NoError(t, db.Create(&channelModel{ID: 1, Models: "operator-authorized"}).Error)
	r := &Repository{db: db}
	ctx := context.Background()
	first, err := r.SaveUpstreamInventory(ctx, biz.UpstreamInventory{SourceKind: "channel", SourceID: 1, ExactModelIDs: []string{"vendor/Exact", "vendor/exact"}, AttemptID: "scan-1", ObservedAt: 1})
	require.NoError(t, err)
	require.Len(t, first.Added, 2)
	next, err := r.SaveUpstreamInventory(ctx, biz.UpstreamInventory{SourceKind: "channel", SourceID: 1, ExactModelIDs: []string{"vendor/Exact", "vendor/new"}, AttemptID: "scan-2", ObservedAt: 2})
	require.NoError(t, err)
	require.Equal(t, []string{"vendor/new"}, next.Added)
	require.Equal(t, []string{"vendor/exact"}, next.Missing)
	var channel channelModel
	require.NoError(t, db.First(&channel).Error)
	require.Equal(t, "operator-authorized", channel.Models)
	var inventory upstreamInventoryModel
	require.NoError(t, db.First(&inventory).Error)
	require.Equal(t, "scan-2", inventory.AttemptID)
	_, err = r.SaveUpstreamInventory(ctx, biz.UpstreamInventory{SourceKind: "channel", SourceID: 1, ExactModelIDs: []string{"stale"}, AttemptID: "stale", ObservedAt: 1})
	require.NoError(t, err)
	require.NoError(t, db.First(&inventory).Error)
	require.Equal(t, "scan-2", inventory.AttemptID)
}

func TestExactInventoryAndQuarantineModelKeysAcrossDialects(t *testing.T) {
	for _, driver := range []string{"sqlite", "mysql", "postgres"} {
		t.Run(driver, func(t *testing.T) {
			db := testutil.RoutingContextDB(t, driver)
			r := &Repository{db: db}
			ctx := context.Background()
			require.True(t, r.UsageV2Available(ctx))
			diff, err := r.SaveUpstreamInventory(ctx, biz.UpstreamInventory{SourceKind: "channel", SourceID: 123, ExactModelIDs: []string{"vendor/Exact", "vendor/exact"}, AttemptID: "inventory", ObservedAt: time.Now().Unix()})
			require.NoError(t, err)
			require.Len(t, diff.Added, 2)
			for _, model := range []string{"vendor/Exact", "vendor/exact"} {
				_, err := r.UpsertUsageSemanticVerdict(ctx, biz.UsageSemanticVerdict{SourceKind: "channel", SourceID: 123, UpstreamModelID: model, AdapterProtocol: "openai_chat", ParseStatus: "unsupported"}, time.Minute, time.Minute, 1, time.Now())
				require.NoError(t, err)
			}
			blocks, err := r.ListBlockedUsageSemanticBlocks(ctx, time.Now().Add(24*time.Hour))
			require.NoError(t, err)
			require.Len(t, blocks, 2, "case-sensitive upstream keys must never collapse")
		})
	}
}

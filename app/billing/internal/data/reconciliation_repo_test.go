package data

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
	"micro-one-api/app/billing/internal/biz"
)

func TestGetLedgerConsumeSummaryCollapsesDualTrackRows(t *testing.T) {
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	require.NoError(t, err)
	require.NoError(t, db.AutoMigrate(&ledgerModel{}))
	referenceID := "res-mixed"
	require.NoError(t, db.Create([]*ledgerModel{
		{Type: biz.LedgerTypeConsume, ReferenceID: &referenceID, Quota: 123, ChannelID: 7, SourceKind: biz.CostSourceChannel, UpstreamCost: 45, CostSource: biz.CostSourceSubscription},
		{Type: biz.LedgerTypeConsume, ReferenceID: &referenceID, Quota: 123, ChannelID: 7, SourceKind: biz.CostSourceChannel, CostSource: biz.CostSourceBalance},
	}).Error)

	repo := NewReconciliationRepo(&Data{db: db})
	summary, err := repo.GetLedgerConsumeSummary(context.Background())
	require.NoError(t, err)
	require.Equal(t, int64(1), summary.Count)
	require.Equal(t, int64(123), summary.Quota)
	channels, err := repo.SumConsumeLedgerUsageByChannel(context.Background())
	require.NoError(t, err)
	require.Len(t, channels, 1)
	require.Equal(t, int64(123), channels[0].Quota)
	require.Equal(t, int64(45), channels[0].UpstreamCost)
}

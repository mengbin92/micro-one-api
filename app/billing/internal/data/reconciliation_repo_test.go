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

func TestListRefundReconciliationsPairsTradeAndUserAndFindsOrphans(t *testing.T) {
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	require.NoError(t, err)
	require.NoError(t, db.AutoMigrate(&PaymentOrder{}, &ledgerModel{}))
	require.NoError(t, db.Create([]*PaymentOrder{
		{UserID: "1", TradeNo: "matched", Status: biz.PaymentOrderStatusRefunded, AssetType: biz.PaymentAssetTypeSubscription, MoneyCents: 10000, PlanSnapshot: "frozen-snapshot"},
		{UserID: "2", TradeNo: "wrong-user", Status: biz.PaymentOrderStatusRefunded, AssetType: biz.PaymentAssetTypeSubscription, MoneyCents: 20000},
		{UserID: "3", TradeNo: "duplicate", Status: biz.PaymentOrderStatusRefunded, AssetType: biz.PaymentAssetTypeSubscription, MoneyCents: 30000},
		{UserID: "4", TradeNo: "paid", Status: biz.PaymentOrderStatusPaid, AssetType: biz.PaymentAssetTypeSubscription, MoneyCents: 40000},
	}).Error)
	ledger := func(userID, referenceID string, amount int64) *ledgerModel {
		return &ledgerModel{UserID: userID, ReferenceID: &referenceID, Type: biz.LedgerTypeRefund, CostSource: biz.CostSourceReversal, Amount: amount}
	}
	require.NoError(t, db.Create([]*ledgerModel{
		ledger("1", "matched", 100), ledger("99", "wrong-user", 200),
		ledger("3", "duplicate", 100), ledger("3", "duplicate", 200),
		ledger("4", "paid", 400), ledger("5", "missing-order", 500),
		{UserID: "1", Type: biz.LedgerTypeRefund, CostSource: biz.CostSourceBalance, Amount: 999},
	}).Error)
	rows, err := NewReconciliationRepo(&Data{db: db}).ListRefundReconciliations(context.Background())
	require.NoError(t, err)
	require.Len(t, rows, 6)
	byKey := map[string]*biz.RefundReconciliation{}
	for _, row := range rows {
		byKey[row.TradeNo+":"+row.UserID] = row
	}
	require.True(t, byKey["matched:1"].RefundedOrder)
	require.Equal(t, "frozen-snapshot", byKey["matched:1"].PlanSnapshot)
	require.Equal(t, "CNY", byKey["matched:1"].Currency)
	require.Equal(t, int64(100), byKey["matched:1"].ReversalLedgerAmount)
	require.Zero(t, byKey["wrong-user:2"].ReversalLedgerCount)
	require.False(t, byKey["wrong-user:99"].RefundedOrder)
	require.Equal(t, int64(200), byKey["wrong-user:99"].ReversalLedgerAmount)
	require.Equal(t, int64(2), byKey["duplicate:3"].ReversalLedgerCount)
	require.Equal(t, int64(300), byKey["duplicate:3"].ReversalLedgerAmount)
	require.False(t, byKey["paid:4"].RefundedOrder)
	require.False(t, byKey["missing-order:5"].RefundedOrder)
}

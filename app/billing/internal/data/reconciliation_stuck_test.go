package data

import (
	"context"
	"testing"
	"time"

	"micro-one-api/app/billing/internal/biz"

	"github.com/stretchr/testify/require"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
)

// TestListStuckIssuedOrders_ExcludesBalanceOrders guards the v0.18 P1 C6
// finding: a paid+issued BALANCE order has subscription_id=0 by design (the
// asset is wallet credit issued inline at MarkOrderPaid, not a subscription
// grant), so the stuck predicate must exclude asset_type=balance or every paid
// balance order would false-positive as stuck.
func TestListStuckIssuedOrders_ExcludesBalanceOrders(t *testing.T) {
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	require.NoError(t, err)
	require.NoError(t, db.AutoMigrate(&PaymentOrder{}))

	d := &Data{db: db}
	payRepo := NewPaymentRepo(d)
	reconRepo := NewReconciliationRepo(d)
	ctx := context.Background()
	now := time.Now()

	// Balance order: paid + issued + subscription_id=0 (normal terminal state).
	_, err = payRepo.CreateOrder(ctx, &biz.PaymentOrder{
		UserID: "1", TradeNo: "PAY-BAL-1", Channel: "alipay",
		AssetType: biz.PaymentAssetTypeBalance, AssetAmount: 2000000, MoneyCents: 2000,
		Status: biz.PaymentOrderStatusPending, AssetIssueStatus: biz.PaymentAssetIssueStatusPending,
		CreatedAt: now, UpdatedAt: now,
	})
	require.NoError(t, err)

	// Subscription order: paid + issued + subscription_id=0 (genuinely stuck).
	_, err = payRepo.CreateOrder(ctx, &biz.PaymentOrder{
		UserID: "1", TradeNo: "PAY-SUB-1", Channel: "alipay",
		AssetType: biz.PaymentAssetTypeSubscription, AssetAmount: 30, MoneyCents: 2000,
		Status: biz.PaymentOrderStatusPending, AssetIssueStatus: biz.PaymentAssetIssueStatusPending,
		CreatedAt: now, UpdatedAt: now,
	})
	require.NoError(t, err)

	// Subscription order: fulfilled (subscription_id>0) — not stuck.
	_, err = payRepo.CreateOrder(ctx, &biz.PaymentOrder{
		UserID: "1", TradeNo: "PAY-SUB-2", Channel: "alipay",
		AssetType: biz.PaymentAssetTypeSubscription, AssetAmount: 30, MoneyCents: 2000,
		Status: biz.PaymentOrderStatusPending, AssetIssueStatus: biz.PaymentAssetIssueStatusPending,
		CreatedAt: now, UpdatedAt: now,
	})
	require.NoError(t, err)

	// Mark all three paid+issued; give PAY-SUB-2 a fulfilled subscription_id.
	require.NoError(t, db.Model(&PaymentOrder{}).Where("trade_no = ?", "PAY-BAL-1").
		Updates(map[string]interface{}{"status": biz.PaymentOrderStatusPaid, "asset_issue_status": biz.PaymentAssetIssueStatusIssued}).Error)
	require.NoError(t, db.Model(&PaymentOrder{}).Where("trade_no = ?", "PAY-SUB-1").
		Updates(map[string]interface{}{"status": biz.PaymentOrderStatusPaid, "asset_issue_status": biz.PaymentAssetIssueStatusIssued}).Error)
	require.NoError(t, db.Model(&PaymentOrder{}).Where("trade_no = ?", "PAY-SUB-2").
		Updates(map[string]interface{}{"status": biz.PaymentOrderStatusPaid, "asset_issue_status": biz.PaymentAssetIssueStatusIssued, "subscription_id": 42}).Error)

	stuck, err := reconRepo.ListStuckIssuedOrders(ctx)
	require.NoError(t, err)
	require.Len(t, stuck, 1, "only the genuine subscription stuck order must be reported")
	require.Equal(t, "PAY-SUB-1", stuck[0].TradeNo, "balance order must NOT be reported as stuck")
}

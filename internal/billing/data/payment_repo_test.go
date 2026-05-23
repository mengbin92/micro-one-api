package data

import (
	"context"
	"testing"
	"time"

	"micro-one-api/internal/billing/biz"

	"github.com/stretchr/testify/require"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
)

func TestPaymentRepo_CreateAndMarkPaid(t *testing.T) {
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	require.NoError(t, err)
	require.NoError(t, db.AutoMigrate(&PaymentOrder{}))

	repo := NewPaymentRepo(&Data{db: db})
	created, err := repo.CreateOrder(context.Background(), &biz.PaymentOrder{
		UserID:           "42",
		TradeNo:          "PAY-1",
		Channel:          biz.PaymentChannelAlipay,
		AssetType:        biz.PaymentAssetTypeQuota,
		AssetAmount:      500000,
		MoneyCents:       1000,
		Currency:         "CNY",
		Status:           biz.PaymentOrderStatusPending,
		AssetIssueStatus: biz.PaymentAssetIssueStatusPending,
		CreatedAt:        time.Now(),
		UpdatedAt:        time.Now(),
	})
	require.NoError(t, err)
	require.NotNil(t, created)

	paid, changed, err := repo.MarkOrderPaid(context.Background(), "PAY-1", "provider-1", func(order *biz.PaymentOrder) error {
		require.Equal(t, "PAY-1", order.TradeNo)
		return nil
	})
	require.NoError(t, err)
	require.True(t, changed)
	require.NotNil(t, paid)
	require.Equal(t, biz.PaymentOrderStatusPaid, paid.Status)
	require.Equal(t, biz.PaymentAssetIssueStatusIssued, paid.AssetIssueStatus)
	require.NotNil(t, paid.PaidAt)
}

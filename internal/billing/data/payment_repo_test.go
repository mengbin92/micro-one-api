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

func TestPaymentRepo_MarkOrderClosed(t *testing.T) {
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

	closed, changed, err := repo.MarkOrderClosed(context.Background(), "PAY-1", "provider-1")
	require.NoError(t, err)
	require.True(t, changed)
	require.NotNil(t, closed)
	require.Equal(t, biz.PaymentOrderStatusClosed, closed.Status)
	require.Equal(t, "provider-1", closed.ProviderTradeNo)
	require.Nil(t, closed.PaidAt)
}

func TestPaymentRepo_ListOrdersFiltersAndPaginates(t *testing.T) {
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	require.NoError(t, err)
	require.NoError(t, db.AutoMigrate(&PaymentOrder{}))

	repo := NewPaymentRepo(&Data{db: db})
	now := time.Now()
	orders := []*biz.PaymentOrder{
		{
			UserID:           "42",
			TradeNo:          "PAY-1",
			Channel:          biz.PaymentChannelAlipay,
			AssetType:        biz.PaymentAssetTypeQuota,
			AssetAmount:      500000,
			MoneyCents:       1000,
			Currency:         "CNY",
			Status:           biz.PaymentOrderStatusPaid,
			ProviderTradeNo:  "ALI-1",
			AssetIssueStatus: biz.PaymentAssetIssueStatusIssued,
			CreatedAt:        now.Add(-time.Minute),
			UpdatedAt:        now,
		},
		{
			UserID:           "43",
			TradeNo:          "PAY-2",
			Channel:          biz.PaymentChannelMock,
			AssetType:        biz.PaymentAssetTypeQuota,
			AssetAmount:      1000000,
			MoneyCents:       2000,
			Currency:         "CNY",
			Status:           biz.PaymentOrderStatusPending,
			AssetIssueStatus: biz.PaymentAssetIssueStatusPending,
			CreatedAt:        now,
			UpdatedAt:        now,
		},
	}
	for _, order := range orders {
		_, err := repo.CreateOrder(context.Background(), order)
		require.NoError(t, err)
	}

	list, total, err := repo.ListOrders(context.Background(), biz.ListPaymentOrdersRequest{
		Page:     1,
		PageSize: 10,
		UserID:   "42",
		Status:   biz.PaymentOrderStatusPaid,
		Channel:  biz.PaymentChannelAlipay,
		TradeNo:  "ALI",
	})
	require.NoError(t, err)
	require.Equal(t, int64(1), total)
	require.Len(t, list, 1)
	require.Equal(t, "PAY-1", list[0].TradeNo)
	require.Equal(t, "ALI-1", list[0].ProviderTradeNo)
}

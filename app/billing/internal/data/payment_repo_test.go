package data

import (
	"context"
	"testing"
	"time"

	"micro-one-api/app/billing/internal/biz"

	subscriptionbiz "micro-one-api/domain/subscription/biz"

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
		AssetType:        biz.PaymentAssetTypeBalance,
		AssetAmount:      1000000,
		MoneyCents:       1000,
		Currency:         "CNY",
		Status:           biz.PaymentOrderStatusPending,
		AssetIssueStatus: biz.PaymentAssetIssueStatusPending,
		CreatedAt:        time.Now(),
		UpdatedAt:        time.Now(),
	})
	require.NoError(t, err)
	require.NotNil(t, created)

	paid, changed, err := repo.MarkOrderPaid(context.Background(), "PAY-1", "provider-1", func(order *biz.PaymentOrder, tx subscriptionbiz.Tx) error {
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
		AssetType:        biz.PaymentAssetTypeBalance,
		AssetAmount:      1000000,
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
			AssetType:        biz.PaymentAssetTypeBalance,
			AssetAmount:      1000000,
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
			AssetType:        biz.PaymentAssetTypeBalance,
			AssetAmount:      2000000,
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

func TestPaymentRepo_MarkOrderAssetIssuedCAS(t *testing.T) {
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	require.NoError(t, err)
	require.NoError(t, db.AutoMigrate(&PaymentOrder{}))

	repo := NewPaymentRepo(&Data{db: db})
	_, err = repo.CreateOrder(context.Background(), &biz.PaymentOrder{
		UserID:           "42",
		TradeNo:          "PAY-CAS-1",
		Channel:          biz.PaymentChannelAlipay,
		AssetType:        biz.PaymentAssetTypeSubscription,
		AssetAmount:      30,
		Status:           biz.PaymentOrderStatusPaid,
		AssetIssueStatus: biz.PaymentAssetIssueStatusPending,
	})
	require.NoError(t, err)

	// First completion wins the claim (pending -> issued).
	order, claimed, err := repo.MarkOrderAssetIssued(context.Background(), "PAY-CAS-1", "42")
	require.NoError(t, err)
	require.True(t, claimed)
	require.NotNil(t, order)
	require.Equal(t, biz.PaymentAssetIssueStatusIssued, order.AssetIssueStatus)

	// Replay loses the claim and observes issued.
	order, claimed, err = repo.MarkOrderAssetIssued(context.Background(), "PAY-CAS-1", "42")
	require.NoError(t, err)
	require.False(t, claimed)
	require.NotNil(t, order)
	require.Equal(t, biz.PaymentAssetIssueStatusIssued, order.AssetIssueStatus)

	// Compensation releases the claim (issued -> pending).
	order, unmarked, err := repo.UnmarkOrderAssetIssued(context.Background(), "PAY-CAS-1")
	require.NoError(t, err)
	require.True(t, unmarked)
	require.NotNil(t, order)
	require.Equal(t, biz.PaymentAssetIssueStatusPending, order.AssetIssueStatus)

	// Unmark on an already-pending order is a no-op.
	_, unmarked, err = repo.UnmarkOrderAssetIssued(context.Background(), "PAY-CAS-1")
	require.NoError(t, err)
	require.False(t, unmarked)

	// A different user cannot claim the order.
	_, claimed, err = repo.MarkOrderAssetIssued(context.Background(), "PAY-CAS-1", "999")
	require.NoError(t, err)
	require.False(t, claimed)
}

func TestPaymentRepo_MarkOrderAssetIssuedRefusesRefunded(t *testing.T) {
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	require.NoError(t, err)
	require.NoError(t, db.AutoMigrate(&PaymentOrder{}))

	repo := NewPaymentRepo(&Data{db: db})
	_, err = repo.CreateOrder(context.Background(), &biz.PaymentOrder{
		UserID:           "42",
		TradeNo:          "PAY-CAS-2",
		Status:           biz.PaymentOrderStatusPaid,
		AssetIssueStatus: "refunded",
	})
	require.NoError(t, err)

	order, claimed, err := repo.MarkOrderAssetIssued(context.Background(), "PAY-CAS-2", "42")
	require.NoError(t, err)
	require.False(t, claimed)
	require.NotNil(t, order)
	require.Equal(t, "refunded", order.AssetIssueStatus)
}

// F17-3 重复签名回调（repo 层）：重放已支付的 MarkOrderPaid 不得再次执行
// 发放回调，也不得以新的 provider 单号覆盖原值 —— 状态短路是第一道闸门，
// 账本 dedupe claim（topup:{user}:payment:{trade_no}）是第二道。
func TestPaymentRepo_MarkOrderPaidReplayDoesNotReissue(t *testing.T) {
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	require.NoError(t, err)
	require.NoError(t, db.AutoMigrate(&PaymentOrder{}))

	repo := NewPaymentRepo(&Data{db: db})
	_, err = repo.CreateOrder(context.Background(), &biz.PaymentOrder{
		UserID:           "42",
		TradeNo:          "PAY-REPLAY-1",
		Channel:          biz.PaymentChannelAlipay,
		AssetType:        biz.PaymentAssetTypeBalance,
		AssetAmount:      1000000,
		MoneyCents:       1000,
		Status:           biz.PaymentOrderStatusPending,
		AssetIssueStatus: biz.PaymentAssetIssueStatusPending,
	})
	require.NoError(t, err)

	issues := 0
	paid, changed, err := repo.MarkOrderPaid(context.Background(), "PAY-REPLAY-1", "provider-1", func(order *biz.PaymentOrder, tx subscriptionbiz.Tx) error {
		issues++
		return nil
	})
	require.NoError(t, err)
	require.True(t, changed)
	require.NotNil(t, paid)
	require.Equal(t, 1, issues)

	// 支付宝重发同一笔签名回调：不得再发放，也不得覆盖原 provider 单号。
	replayed, changed, err := repo.MarkOrderPaid(context.Background(), "PAY-REPLAY-1", "provider-EVIL-REPLAY", func(order *biz.PaymentOrder, tx subscriptionbiz.Tx) error {
		issues++
		return nil
	})
	require.NoError(t, err)
	require.False(t, changed)
	require.NotNil(t, replayed)
	require.Equal(t, 1, issues)
	require.Equal(t, biz.PaymentOrderStatusPaid, replayed.Status)
	require.Equal(t, "provider-1", replayed.ProviderTradeNo)
}

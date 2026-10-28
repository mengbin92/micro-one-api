package data

import (
	"context"
	"sync"
	"testing"

	"github.com/stretchr/testify/require"
	"micro-one-api/app/billing/internal/biz"
	"micro-one-api/platform/database/testutil"
)

type wechatPaidStatus struct {
	currency string
	amount   int64
}

func (p wechatPaidStatus) CreateOrder(context.Context, *biz.PaymentOrder) (*biz.PaymentProviderOrder, error) {
	return &biz.PaymentProviderOrder{}, nil
}

func (p wechatPaidStatus) QueryOrder(_ context.Context, order *biz.PaymentOrder) (*biz.PaymentProviderStatus, error) {
	return &biz.PaymentProviderStatus{TradeNo: order.TradeNo, ProviderTradeNo: "wechat-transaction", TradeStatus: "SUCCESS", Paid: true, TotalAmount: p.amount, Currency: p.currency}, nil
}

func TestWechatQueryAndNotifyIssueOnce(t *testing.T) {
	for _, dialect := range []string{"sqlite", "mysql", "postgres"} {
		t.Run(dialect, func(t *testing.T) {
			db := testutil.RoutingContextDB(t, dialect)
			if dialect == "sqlite" {
				sqlDB, err := db.DB()
				require.NoError(t, err)
				sqlDB.SetMaxOpenConns(1)
			}
			ctx := context.Background()
			require.NoError(t, db.Table("users").Create(map[string]any{"id": 42, "username": "wechat-race", "status": 1, "balance": 0}).Error)
			d := &Data{db: db}
			repo := NewPaymentRepo(d)
			billing := biz.NewBillingUsecase(NewAccountRepo(d), NewReservationRepo(d), NewLedgerRepo(d), NewRedeemRepo(d), nil)
			issuer := biz.NewPaymentAssetIssuer(billing)
			uc := biz.NewPaymentUsecase(repo, wechatPaidStatus{currency: "CNY", amount: 500}, issuer)
			_, err := repo.CreateOrder(ctx, &biz.PaymentOrder{
				TradeNo: "wechat-race", UserID: "42", Channel: biz.PaymentChannelWechat,
				AssetType: biz.PaymentAssetTypeBalance, AssetAmount: 1000, MoneyCents: 500,
				Currency: "CNY", Status: biz.PaymentOrderStatusPending, AssetIssueStatus: biz.PaymentAssetIssueStatusPending,
			})
			require.NoError(t, err)
			notification := biz.PaymentNotify{TradeNo: "wechat-race", ProviderTradeNo: "wechat-transaction", Channel: biz.PaymentChannelWechat, Success: true, TotalAmount: 500, Currency: "CNY"}
			for _, invalid := range []biz.PaymentNotify{
				{TradeNo: "wechat-race", Channel: biz.PaymentChannelWechat, Success: true, TotalAmount: 501, Currency: "CNY"},
				{TradeNo: "wechat-race", Channel: biz.PaymentChannelWechat, Success: true, TotalAmount: 500, Currency: "USD"},
			} {
				_, err := uc.AcceptVerifiedNotify(ctx, &invalid)
				require.Error(t, err)
			}
			for _, provider := range []wechatPaidStatus{{currency: "CNY", amount: 501}, {currency: "USD", amount: 500}} {
				invalidQuery := biz.NewPaymentUsecase(repo, provider, issuer)
				_, err := invalidQuery.GetOrderByTradeNo(ctx, "wechat-race")
				require.Error(t, err)
			}

			var wg sync.WaitGroup
			start := make(chan struct{})
			errs := make(chan error, 16)
			for i := range 16 {
				wg.Add(1)
				go func(query bool) {
					defer wg.Done()
					<-start
					var err error
					if query {
						_, err = uc.GetOrderByTradeNo(ctx, "wechat-race")
					} else {
						_, err = uc.AcceptVerifiedNotify(ctx, &notification)
					}
					errs <- err
				}(i%2 == 0)
			}
			close(start)
			wg.Wait()
			close(errs)
			for err := range errs {
				require.NoError(t, err)
			}
			stored, err := repo.GetOrderByTradeNo(ctx, "wechat-race")
			require.NoError(t, err)
			require.Equal(t, biz.PaymentOrderStatusPaid, stored.Status)
			require.Equal(t, biz.PaymentAssetIssueStatusIssued, stored.AssetIssueStatus)
			require.Equal(t, "wechat-transaction", stored.ProviderTradeNo)

			closed, changed, err := repo.MarkOrderClosed(ctx, "wechat-race", "late-close")
			require.NoError(t, err)
			require.False(t, changed)
			require.Equal(t, biz.PaymentOrderStatusPaid, closed.Status)
			_, err = uc.AcceptVerifiedNotify(ctx, &notification)
			require.NoError(t, err)

			_, err = repo.CreateOrder(ctx, &biz.PaymentOrder{
				TradeNo: "wechat-closed", UserID: "42", Channel: biz.PaymentChannelWechat,
				AssetType: biz.PaymentAssetTypeBalance, AssetAmount: 1000, MoneyCents: 500,
				Currency: "CNY", Status: biz.PaymentOrderStatusPending, AssetIssueStatus: biz.PaymentAssetIssueStatusPending,
			})
			require.NoError(t, err)
			_, changed, err = repo.MarkOrderClosed(ctx, "wechat-closed", "")
			require.NoError(t, err)
			require.True(t, changed)
			notification.TradeNo = "wechat-closed"
			_, err = uc.AcceptVerifiedNotify(ctx, &notification)
			require.Error(t, err)
			stored, err = repo.GetOrderByTradeNo(ctx, "wechat-closed")
			require.NoError(t, err)
			require.Equal(t, biz.PaymentOrderStatusClosed, stored.Status)

			var balance, ledgers int64
			require.NoError(t, db.Table("users").Where("id = ?", 42).Pluck("balance", &balance).Error)
			require.EqualValues(t, 1000, balance)
			require.NoError(t, db.Table("billing_ledgers").Where("user_id = ? AND type = ?", "42", biz.LedgerTypeRecharge).Count(&ledgers).Error)
			require.EqualValues(t, 1, ledgers)
		})
	}
}

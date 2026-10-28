package data

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"micro-one-api/app/billing/internal/biz"
	"micro-one-api/platform/database/testutil"
)

func TestWechatPrepayClaimFencesLateResponses(t *testing.T) {
	for _, dialect := range []string{"sqlite", "mysql", "postgres"} {
		t.Run(dialect, func(t *testing.T) {
			db := testutil.RoutingContextDB(t, dialect)
			sqlDB, err := db.DB()
			require.NoError(t, err)
			if dialect == "sqlite" {
				sqlDB.SetMaxOpenConns(1)
			}
			repo := NewPaymentRepo(&Data{db: db}).(*paymentRepo)
			ctx := context.Background()
			_, err = repo.CreateOrder(ctx, &biz.PaymentOrder{TradeNo: "wechat-prepay", UserID: "42", Channel: biz.PaymentChannelWechat, AssetType: biz.PaymentAssetTypeBalance, AssetAmount: 1, MoneyCents: 1, Currency: "CNY", Status: biz.PaymentOrderStatusPending, CreatedAt: time.Now()})
			require.NoError(t, err)
			var wg sync.WaitGroup
			claims := make(chan *biz.PaymentOrder, 8)
			for range 8 {
				wg.Add(1)
				go func() {
					defer wg.Done()
					order, err := repo.ClaimProviderOrder(ctx, "wechat-prepay")
					if err != nil {
						t.Error(err)
						return
					}
					if order.ProviderClaim != "" {
						claims <- order
					}
				}()
			}
			wg.Wait()
			close(claims)
			var first *biz.PaymentOrder
			count := 0
			for order := range claims {
				first = order
				count++
			}
			require.Equal(t, 1, count, "only one process may call Native prepay")
			// Expire the lease, then let a replacement finish before the old response arrives.
			require.NoError(t, db.Model(&PaymentOrder{}).Where("trade_no = ?", "wechat-prepay").UpdateColumn("provider_payload", `{"prepay_until":1}`).Error)
			replacement, err := repo.ClaimProviderOrder(ctx, "wechat-prepay")
			require.NoError(t, err)
			require.NotEmpty(t, replacement.ProviderClaim)
			replacement.PayURL = "weixin://wxpay/bizpayurl/up?pr=new"
			replacement.ProviderPayload = `{"provider":"wechat"}`
			stored, err := repo.AttachProviderResult(ctx, replacement)
			require.NoError(t, err)
			require.Equal(t, replacement.PayURL, stored.PayURL)
			first.PayURL = "weixin://wxpay/bizpayurl/up?pr=expired"
			first.ProviderPayload = `{"provider":"wechat"}`
			stored, err = repo.AttachProviderResult(ctx, first)
			require.NoError(t, err)
			require.Equal(t, replacement.PayURL, stored.PayURL, "late response must not overwrite valid QR")
			next, err := repo.ClaimProviderOrder(ctx, "wechat-prepay")
			require.NoError(t, err)
			require.Empty(t, next.ProviderClaim)
		})
	}
}

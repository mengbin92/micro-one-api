package data

import (
	"context"
	"github.com/stretchr/testify/require"
	"micro-one-api/app/billing/internal/biz"
	"micro-one-api/domain/authorization"
	authztest "micro-one-api/domain/authorization/testutil"
	dbtest "micro-one-api/platform/database/testutil"
	"micro-one-api/platform/security/serviceidentity"
	"testing"
	"time"
)

func TestIAMB3BillingOwnerDialects(t *testing.T) {
	for _, driver := range []string{"sqlite", "mysql", "postgres"} {
		t.Run(driver, func(t *testing.T) {
			db := dbtest.RoutingContextDB(t, driver)
			d := &Data{db: db}
			repo := NewLedgerRepo(d)
			ctx := context.Background()
			one := &biz.Ledger{UserID: "10", Amount: -100, UpstreamCost: 30, Type: biz.LedgerTypeConsume, ReferenceID: "one", CreatedAt: time.Now()}
			two := &biz.Ledger{UserID: "20", Amount: -500, UpstreamCost: 90, Type: biz.LedgerTypeConsume, ReferenceID: "two", CreatedAt: time.Now()}
			require.NoError(t, repo.CreateLedger(ctx, one))
			require.NoError(t, repo.CreateLedger(ctx, two))
			policy := &authztest.Resolver{ActorID: 10, Scopes: map[string]authorization.QueryScope{"billing.account.ledger.read": authztest.Users(10), "billing.payment.list": authztest.Users(10), "billing.payment.read": authztest.Users(10)}}
			uc := biz.NewBillingUsecase(nil, nil, repo, nil, nil)
			uc.SetAuthorization(policy)
			request := authztest.Context()
			rows, total, err := uc.ListLedgers(request, "", 1, 1)
			require.NoError(t, err)
			require.EqualValues(t, 1, total)
			require.Len(t, rows, 1)
			require.Equal(t, "10", rows[0].UserID)
			require.False(t, rows[0].CostFieldsVisible)
			require.Zero(t, rows[0].UpstreamCost)
			_, err = uc.GetLedgerByID(request, int64(two.ID))
			require.Error(t, err)
			buckets, totals, err := uc.AggregateUsage(request, biz.UsageFilter{GroupBy: []string{biz.UsageDimUser}})
			require.NoError(t, err)
			require.Len(t, buckets, 1)
			require.EqualValues(t, 100, totals.Quota)
			require.False(t, totals.CostFieldsVisible)
			require.Zero(t, totals.GrossProfit)
			policy.Scopes["billing.account.cost.read"] = authztest.Users(10)
			_, totals, err = uc.AggregateUsage(request, biz.UsageFilter{GroupBy: []string{biz.UsageDimUser}})
			require.NoError(t, err)
			require.True(t, totals.CostFieldsVisible)
			require.EqualValues(t, 30, totals.UpstreamCost)
			// Resource IDs for account permissions refer to accounts, not ledger IDs.
			policy.Scopes["billing.account.ledger.read"] = authztest.Resources(10)
			rows, total, err = uc.ListLedgers(request, "", 1, 1)
			require.NoError(t, err)
			require.EqualValues(t, 1, total)
			require.Equal(t, "10", rows[0].UserID)
			payments := NewPaymentRepo(d)
			for _, user := range []string{"10", "20"} {
				_, err = payments.CreateOrder(ctx, &biz.PaymentOrder{TradeNo: "trade-" + user, UserID: user, Channel: biz.PaymentChannelAlipay, AssetType: biz.PaymentAssetTypeBalance, Status: biz.PaymentOrderStatusClosed, Currency: "CNY", AssetIssueStatus: biz.PaymentAssetIssueStatusIssued})
				require.NoError(t, err)
			}
			paymentUC := biz.NewPaymentUsecase(payments, nil, nil)
			paymentUC.SetAuthorization(policy)
			self := serviceidentity.WithRPCMethod(serviceidentity.WithPrincipal(request, serviceidentity.Principal{Name: "identity", Dedicated: true}), "/api.billing.v1.BillingService/ListPaymentOrders")
			orders, total, err := paymentUC.ListOrders(self, biz.ListPaymentOrdersRequest{Page: 1, PageSize: 1})
			require.NoError(t, err)
			require.EqualValues(t, 1, total)
			require.Len(t, orders, 1)
			require.Equal(t, "10", orders[0].UserID)
			_, err = paymentUC.GetOrderByTradeNo(self, "trade-20")
			require.Error(t, err)
			orders, total, err = paymentUC.ListOrders(self, biz.ListPaymentOrdersRequest{UserID: "20", Page: 1, PageSize: 1})
			require.NoError(t, err)
			require.Zero(t, total)
			require.Empty(t, orders)
		})
	}
}

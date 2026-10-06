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

func TestIAMB3WritesAndExportsDialects(t *testing.T) {
	for _, driver := range []string{"sqlite", "mysql", "postgres"} {
		t.Run(driver, func(t *testing.T) {
			db := dbtest.RoutingContextDB(t, driver)
			d := &Data{db: db}
			legacy := context.Background()
			require.NoError(t, db.Table("users").Create(map[string]any{"id": 10, "username": "writer10", "password_hash": "test", "balance": 100, "used_amount": 0, "request_count": 0, "frozen_amount": 0, "status": 1}).Error)
			require.NoError(t, db.Table("users").Create(map[string]any{"id": 20, "username": "writer20", "password_hash": "test", "balance": 200, "used_amount": 0, "request_count": 0, "frozen_amount": 0, "status": 1}).Error)
			ledger := NewLedgerRepo(d)
			account := NewAccountRepo(d)
			redeem := NewRedeemRepo(d)
			uc := biz.NewBillingUsecase(account, nil, ledger, redeem, nil)
			uc.SetTxRunner(NewTxRunner(d))
			policy := &authztest.Resolver{ActorID: 10, Scopes: map[string]authorization.QueryScope{"billing.account.balance.reset": authztest.Resources(10), "billing.redemption.create": authztest.All(), "billing.redemption.batch_create": authztest.All(), "billing.report.export": authztest.Users(10)}}
			for op, q := range policy.Scopes {
				q.ActorID = 10
				policy.Scopes[op] = q
			}
			uc.SetAuthorization(policy)
			ctx := authorization.WithWriteReason(authztest.Context(), "reviewed financial correction")
			balance, err := uc.ResetBalance(ctx, "10", 300, 100, "reviewed financial correction", "reset-one")
			require.NoError(t, err)
			require.EqualValues(t, 300, balance)
			_, err = uc.ResetBalance(ctx, "10", 400, 100, "stale", "reset-two")
			require.ErrorIs(t, err, biz.ErrRoutingContextConflict)
			_, err = uc.ResetBalance(ctx, "20", 400, 200, "blocked", "reset-three")
			require.ErrorIs(t, err, authorization.ErrDenied)
			a, err := account.GetAccountSnapshot(legacy, "10")
			require.NoError(t, err)
			require.EqualValues(t, 300, a.Balance)
			rows, err := uc.ExportLedgerEntries(ctx, biz.LedgerListOptions{Page: 1, PageSize: 10})
			require.NoError(t, err)
			require.Len(t, rows, 1)
			require.Equal(t, "10", rows[0].UserID)
			require.False(t, rows[0].CostFieldsVisible)
			_, _, err = uc.ListLedgers(ctx, "", 1, 10)
			require.Error(t, err, "export grant is independent from read")
			require.NoError(t, ledger.CreateLedger(legacy, &biz.Ledger{UserID: "20", Amount: -500, UpstreamCost: 80, Type: biz.LedgerTypeConsume, ReferenceID: "outside", CreatedAt: time.Now()}))
			buckets, totals, err := uc.ExportCostReport(ctx, biz.UsageFilter{GroupBy: []string{biz.UsageDimUser}}, false)
			require.NoError(t, err)
			require.Len(t, buckets, 1)
			require.Equal(t, "10", buckets[0].UserID)
			require.EqualValues(t, 200, totals.Quota)
			require.NoError(t, uc.CreateRedeemCode(ctx, "first", "first", 100, 1, "forged999"))
			require.NoError(t, uc.CreateRedeemCode(ctx, "second", "second", 200, 1, "forged999"))
			first, err := redeem.GetRedeemCode(legacy, "first")
			require.NoError(t, err)
			second, err := redeem.GetRedeemCode(legacy, "second")
			require.NoError(t, err)
			require.Equal(t, "10", first.CreatedBy)
			policy.Scopes["billing.redemption.list"] = authztest.Resources(first.ID)
			policy.Scopes["billing.redemption.read"] = authztest.Resources(first.ID)
			policy.Scopes["billing.redemption.update"] = authztest.Resources(first.ID)
			policy.Scopes["billing.redemption.delete"] = authztest.Resources(first.ID)
			policy.Scopes["billing.redemption.export"] = authztest.Resources(first.ID)
			codes, total, err := uc.ListRedeemCodes(ctx, 1, 1)
			require.NoError(t, err)
			require.EqualValues(t, 1, total)
			require.Len(t, codes, 1)
			_, err = uc.GetRedeemCode(ctx, second.Code)
			require.ErrorIs(t, err, authorization.ErrDenied)
			require.NoError(t, uc.UpdateRedeemCode(biz.WithExpectedWriteVersion(ctx, 1), first.Code, "edited", 100, 0))
			require.ErrorIs(t, uc.UpdateRedeemCode(biz.WithExpectedWriteVersion(ctx, 1), first.Code, "stale", 100, 1), biz.ErrRoutingContextConflict)
			exported, err := uc.ExportRedeemCodes(ctx)
			require.NoError(t, err)
			require.Len(t, exported, 1)
			require.EqualValues(t, 2, exported[0].Revision)
			require.ErrorIs(t, uc.DeleteRedeemCode(biz.WithExpectedWriteVersion(ctx, 1), first.Code), biz.ErrRoutingContextConflict)
			require.NoError(t, uc.DeleteRedeemCode(biz.WithExpectedWriteVersion(ctx, 2), first.Code))
			var audits int64
			require.NoError(t, db.Table("resource_write_audits").Count(&audits).Error)
			require.GreaterOrEqual(t, audits, int64(5))
		})
	}
}

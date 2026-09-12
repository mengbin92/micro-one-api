package data

import (
	"context"
	"fmt"
	"github.com/stretchr/testify/require"
	"micro-one-api/app/billing/internal/biz"
	"micro-one-api/domain/routing"
	subscriptionbiz "micro-one-api/domain/subscription/biz"
	"micro-one-api/platform/database/testutil"
	"sync"
	"testing"
	"time"
)

func TestSubscriptionCommerceAtomic(t *testing.T) {
	for _, dialect := range []string{"sqlite", "mysql", "postgres"} {
		t.Run(dialect, func(t *testing.T) {
			t.Setenv("BILLING_REQUEST_SNAPSHOT_V2", "true")
			t.Setenv("SUBSCRIPTION_ENTITLEMENTS_V2", "true")
			db := testutil.RoutingContextDB(t, dialect)
			billing, _, d, repo, _ := snapshotBillingFixture(t, db, false, 1, 1)
			billing.SetRoutingPolicyRepo(NewRoutingPolicyRepo(d))
			ctx := context.Background()
			limit := 1.0
			group := &subscriptionbiz.SubscriptionGroup{Name: "commerce", Status: 1, RateMultiplier: 2, DailyLimitUSD: &limit}
			require.NoError(t, repo.CreateGroup(ctx, group))
			contract, err := subscriptionbiz.NewContract(group, []subscriptionbiz.RoutingCoverage{{GroupID: 9, GroupKey: "default", GrantsAccess: true}})
			require.NoError(t, err)
			plan := &subscriptionbiz.SubscriptionPlan{GroupID: group.ID, Name: "frozen", PriceQuota: 100, ValidityDays: 30, ForSale: true, Contract: contract, Coverage: contract.Coverage}
			require.NoError(t, repo.CreatePlan(ctx, plan))
			subs := subscriptionbiz.NewSubscriptionUsecase(repo, repo)
			commerce := biz.NewSubscriptionCommerce(billing, subs, repo, NewSubscriptionCommerceRepo(d))
			req := biz.SubscriptionCommerceRequest{UserID: 7001, RequestID: "purchase", PlanID: plan.ID}
			first, err := commerce.Execute(ctx, req)
			require.NoError(t, err)
			require.EqualValues(t, 999900, first.Balance)
			require.Equal(t, contract.Digest, first.Subscription.Contract.Digest)
			replay, err := commerce.Execute(ctx, req)
			require.NoError(t, err)
			require.Equal(t, first, replay)
			changed := req
			changed.ChangePolicy = "immediate"
			_, err = commerce.Execute(ctx, changed)
			require.ErrorIs(t, err, biz.ErrRoutingContextConflict)
			var count int64
			require.NoError(t, db.Table("billing_ledgers").Count(&count).Error)
			require.EqualValues(t, 1, count)
			// A failed debit must roll back the renewal and receipt, permitting a retry.
			require.NoError(t, db.Table("users").Where("id = ?", 7001).Update("balance", 0).Error)
			req.RequestID = "renew"
			_, err = commerce.Execute(ctx, req)
			require.Error(t, err)
			current, err := repo.GetSubscriptionByID(ctx, first.Subscription.ID)
			require.NoError(t, err)
			require.Equal(t, first.Subscription.ExpiresAt, current.ExpiresAt)
			require.NoError(t, db.Table("subscription_commerce_receipts").Where("request_id = ?", "renew").Count(&count).Error)
			require.Zero(t, count)
			require.NoError(t, db.Table("users").Where("id = ?", 7001).Update("balance", 1000).Error)
			renewed, err := commerce.Execute(ctx, req)
			require.NoError(t, err)
			require.Equal(t, first.Subscription.ID, renewed.Subscription.ID)
			require.Equal(t, first.Subscription.ExpiresAt+30*86400, renewed.Subscription.ExpiresAt)
			// Editing the plan cannot silently replace the purchased coverage on renewal.
			plan.Contract, err = subscriptionbiz.NewContract(group, []subscriptionbiz.RoutingCoverage{{GroupID: 10, GroupKey: "second"}})
			require.NoError(t, err)
			require.NoError(t, repo.UpdatePlan(ctx, plan))
			req.RequestID = "different"
			_, err = commerce.Execute(ctx, req)
			require.Error(t, err)
			account, err := d.accountRepo.GetAccountSnapshot(ctx, "7001")
			require.NoError(t, err)
			require.EqualValues(t, 900, account.Balance)
			require.NoError(t, subs.RecordUsage(ctx, 7001, 0.1))
			req.RequestID = "explicit-change"
			req.FromSubscriptionID = first.Subscription.ID
			req.ChangePolicy = "immediate"
			change, err := commerce.Execute(ctx, req)
			require.NoError(t, err)
			require.True(t, change.Change.Applied)
			require.Equal(t, plan.Contract.Digest, change.Subscription.Contract.Digest)
			require.InDelta(t, 0.2, change.Subscription.DailyUsageUSD, 1e-10)
			replay, err = commerce.Execute(ctx, req)
			require.NoError(t, err)
			require.Equal(t, change, replay)
			cheap := &subscriptionbiz.SubscriptionPlan{GroupID: group.ID, Name: "next cycle", PriceQuota: 50, ValidityDays: 30, ForSale: true, Contract: contract}
			require.NoError(t, repo.CreatePlan(ctx, cheap))
			req.PlanID = cheap.ID
			req.RequestID = "schedule-change"
			req.ChangePolicy = "next_cycle"
			scheduled, err := commerce.Execute(ctx, req)
			require.NoError(t, err)
			require.False(t, scheduled.Change.Applied)
			require.EqualValues(t, 900, scheduled.Balance)
			require.Equal(t, plan.Contract.Digest, scheduled.Subscription.Contract.Digest)
			req.FromSubscriptionID = 0
			req.RequestID = "renew-target"
			req.ChangePolicy = ""
			next, err := commerce.Execute(ctx, req)
			require.NoError(t, err)
			require.Equal(t, contract.Digest, next.Subscription.Contract.Digest)
			require.InDelta(t, 0.2, next.Subscription.DailyUsageUSD, 1e-10)
			require.EqualValues(t, 850, next.Balance)

		})
	}
}

func TestRoutingSettlementModes(t *testing.T) {
	for _, dialect := range []string{"sqlite", "mysql", "postgres"} {
		for _, mode := range []string{routing.WalletOnly, routing.SubscriptionFirst, routing.SubscriptionOnly} {
			t.Run(dialect+"/"+mode, func(t *testing.T) {
				t.Setenv("BILLING_REQUEST_SNAPSHOT_V2", "true")
				t.Setenv("SUBSCRIPTION_ENTITLEMENTS_V2", "true")
				db := testutil.RoutingContextDB(t, dialect)
				uc, _, d, repo, _ := snapshotBillingFixture(t, db, false, 1, 1)
				policies := NewRoutingPolicyRepo(d)
				uc.SetRoutingPolicyRepo(policies)
				ctx := context.Background()
				p := &routing.BillingPolicy{GroupID: 9, BillingMode: mode, PriceRatio: 0.5}
				require.NoError(t, uc.PublishRoutingBillingPolicy(ctx, p, 0))
				require.ErrorIs(t, uc.PublishRoutingBillingPolicy(ctx, p, 0), biz.ErrRoutingContextConflict)
				limit := 0.1
				group := &subscriptionbiz.SubscriptionGroup{Name: "mode", Status: 1, RateMultiplier: 2, DailyLimitUSD: &limit}
				require.NoError(t, repo.CreateGroup(ctx, group))
				contract, err := subscriptionbiz.NewContract(group, []subscriptionbiz.RoutingCoverage{{GroupID: 9, GroupKey: "default", GrantsAccess: true}})
				require.NoError(t, err)
				if mode == routing.WalletOnly {
					contract = nil
				}
				now := time.Now().Unix()
				sub := &subscriptionbiz.UserSubscription{UserID: 7001, GroupID: group.ID, Contract: contract, Status: subscriptionbiz.SubscriptionStatusActive, StartsAt: now, ExpiresAt: now + 86400, DailyWindowStart: now, WeeklyWindowStart: now, MonthlyWindowStart: now}
				require.NoError(t, repo.CreateSubscription(ctx, sub))
				uc.SetSubscriptionPrimatives(subscriptionbiz.NewSubscriptionUsecase(repo, repo))
				rc := requestContext()
				rc.SubscriptionID = sub.ID
				rc.SubscriptionEntitlementVersion = sub.EntitlementRevision
				if mode == routing.SubscriptionOnly {
					_, err = uc.ReserveQuota(ctx, "7001", "unbounded", 1000, "m", "1", 0, rc)
					require.ErrorIs(t, err, biz.ErrSubscriptionBoundRequired)
					ctx = routing.WithCostBound(ctx, routing.CostBound{Protocol: "openai_text_embeddings", InputTokens: 1000, UpstreamModel: "text-embedding-3-small"})
					_, err = uc.ReserveQuota(ctx, "7001", "too-big", 1000, "m", "1", 0, rc)
					require.ErrorIs(t, err, biz.ErrSubscriptionQuotaInsufficient)
					ctx = routing.WithCostBound(ctx, routing.CostBound{Protocol: "openai_text_embeddings", InputTokens: 100, UpstreamModel: "text-embedding-3-small"})
				}
				r, err := uc.ReserveQuota(ctx, "7001", "bounded", 100, "m", "1", 0, rc)
				require.NoError(t, err)
				if mode == routing.WalletOnly {
					require.Zero(t, r.SubscriptionID)
				} else {
					require.Equal(t, sub.ID, r.SubscriptionID)
				}
				// A price revision after reserve must not alter the accepted request.
				require.NoError(t, uc.PublishRoutingBillingPolicy(ctx, &routing.BillingPolicy{GroupID: 9, BillingMode: mode, PriceRatio: 9}, 1))
				if mode == routing.SubscriptionOnly {
					_, _, err = uc.CommitQuotaWithUsage(ctx, r.ReservationID, 10000, true, biz.LedgerUsage{PromptTokens: 10000})
					require.ErrorIs(t, err, biz.ErrSubscriptionBoundExceeded)
				}
				_, _, err = uc.CommitQuotaWithUsage(ctx, r.ReservationID, 100, true, biz.LedgerUsage{PromptTokens: 100})
				require.NoError(t, err)
				account, err := d.accountRepo.GetAccountSnapshot(ctx, "7001")
				require.NoError(t, err)
				if mode == routing.WalletOnly {
					require.Less(t, account.Balance, int64(1000000))
				} else {
					require.EqualValues(t, 1000000, account.Balance)
				}
				if mode == routing.SubscriptionFirst {
					outside := *rc
					outside.GroupID = 10
					uncovered, err := uc.ReserveQuota(ctx, "7001", "uncovered", 100, "m", "1", 0, &outside)
					require.NoError(t, err)
					require.Zero(t, uncovered.SubscriptionID)
					_, _, err = uc.CommitQuotaWithUsage(ctx, uncovered.ReservationID, 100, true, biz.LedgerUsage{PromptTokens: 100})
					require.NoError(t, err)
					after, err := d.accountRepo.GetAccountSnapshot(ctx, "7001")
					require.NoError(t, err)
					require.Less(t, after.Balance, account.Balance)
				}
				if mode == routing.SubscriptionOnly {
					require.ErrorIs(t, uc.PublishRoutingBillingPolicy(ctx, &routing.BillingPolicy{GroupID: 9, BillingMode: routing.WalletOnly, PriceRatio: 1}, 2), biz.ErrRoutingContextConflict)
					require.NoError(t, db.Table("user_subscriptions").Where("id = ?", sub.ID).Update("expires_at", time.Now().Unix()-1).Error)
					_, err = uc.ReserveQuota(ctx, "7001", "expired", 100, "m", "1", 0, rc)
					require.ErrorIs(t, err, biz.ErrSubscriptionCoverageRequired)
				}
				current, err := repo.GetSubscriptionByID(ctx, sub.ID)
				require.NoError(t, err)
				if mode == routing.WalletOnly {
					require.Zero(t, current.DailyUsageUSD)
				} else {
					require.Greater(t, current.DailyUsageUSD, 0.0)
				}
			})
		}
	}
}

func TestContractPaymentAndRefund(t *testing.T) {
	for _, dialect := range []string{"sqlite", "mysql", "postgres"} {
		t.Run(dialect, func(t *testing.T) {
			t.Setenv("SUBSCRIPTION_ENTITLEMENTS_V2", "true")
			t.Setenv("BILLING_REQUEST_SNAPSHOT_V2", "true")
			db := testutil.RoutingContextDB(t, dialect)
			billing, _, d, repo, _ := snapshotBillingFixture(t, db, false, 1, 1)
			billing.SetRoutingPolicyRepo(NewRoutingPolicyRepo(d))
			ctx := context.Background()
			group := &subscriptionbiz.SubscriptionGroup{Name: "paid", Status: 1, RateMultiplier: 1}
			require.NoError(t, repo.CreateGroup(ctx, group))
			c, err := subscriptionbiz.NewContract(group, []subscriptionbiz.RoutingCoverage{{GroupID: 9, GroupKey: "default", GrantsAccess: true}})
			require.NoError(t, err)
			plan := &subscriptionbiz.SubscriptionPlan{GroupID: group.ID, Name: "paid plan", PriceQuota: 100, ValidityDays: 30, ForSale: true, Contract: c}
			require.NoError(t, repo.CreatePlan(ctx, plan))
			subs := subscriptionbiz.NewSubscriptionUsecase(repo, repo)
			orders := NewPaymentRepo(d)
			payments := biz.NewPaymentUsecaseWithAssignerAndSnapshotter(orders, biz.NewMockPaymentProvider(), nil, biz.NewPaymentSubscriptionAssigner(subs, repo, repo), biz.NewPaymentPlanSnapshotter(repo, billing))
			payments.SetSubscriptionPurchaseValidator(subs)
			req := biz.CreatePaymentOrderRequest{UserID: "7001", RequestID: "payment-intent", PlanID: plan.ID, Channel: "mock", AssetType: "subscription", AssetAmount: 1, MoneyCents: 1, Currency: "CNY"}
			order, err := payments.CreateOrder(ctx, req)
			require.NoError(t, err)
			require.EqualValues(t, 10000, order.MoneyCents)
			replay, err := payments.CreateOrder(ctx, req)
			require.NoError(t, err)
			require.Equal(t, order.TradeNo, replay.TradeNo)
			require.NoError(t, repo.DeletePlan(ctx, plan.ID))
			require.ErrorIs(t, billing.PublishRoutingBillingPolicy(ctx, &routing.BillingPolicy{GroupID: 9, BillingMode: routing.WalletOnly, PriceRatio: 1}, 0), biz.ErrRoutingContextConflict, "pending orders block a conflicting mode change")
			replay, err = payments.CreateOrder(ctx, req)
			require.NoError(t, err)
			require.Equal(t, order.TradeNo, replay.TradeNo)
			paid, err := payments.MarkOrderPaid(ctx, order.TradeNo, "provider-1")
			require.NoError(t, err)
			_, err = payments.MarkOrderPaid(ctx, order.TradeNo, "provider-1")
			require.NoError(t, err)
			sub, err := repo.GetSubscriptionByID(ctx, paid.SubscriptionID)
			require.NoError(t, err)
			require.Equal(t, c.Digest, sub.Contract.Digest)
			require.Len(t, sub.RoutingGrants(time.Now().Unix()), 1)
			refundRepo, ok := orders.(biz.RefundRepo)
			require.True(t, ok)
			refunds := biz.NewRefundUsecase(refundRepo, d.accountRepo, d.ledgerRepo, subs)
			_, err = refunds.RefundSubscriptionOrder(ctx, biz.RefundRequest{TradeNo: order.TradeNo, Policy: biz.RefundPolicyRevoke, Reason: "test"})
			require.NoError(t, err)
			_, err = refunds.RefundSubscriptionOrder(ctx, biz.RefundRequest{TradeNo: order.TradeNo, Policy: biz.RefundPolicyRevoke, Reason: "test"})
			require.NoError(t, err)
			current, err := repo.GetSubscriptionByID(ctx, sub.ID)
			require.NoError(t, err)
			require.Empty(t, current.RoutingGrants(time.Now().Unix()))
			var count int64
			require.NoError(t, db.Table("routing_change_outbox").Where("owner = ?", "subscription").Count(&count).Error)
			require.EqualValues(t, 2, count)
			require.NoError(t, db.Table("billing_ledgers").Where("type = ?", biz.LedgerTypeRefund).Count(&count).Error)
			require.EqualValues(t, 1, count)
		})
	}
}

func TestCoveredGroupsShareFrozenQuota(t *testing.T) {
	for _, dialect := range []string{"sqlite", "mysql", "postgres"} {
		t.Run(dialect, func(t *testing.T) {
			t.Setenv("SUBSCRIPTION_ENTITLEMENTS_V2", "true")
			t.Setenv("BILLING_REQUEST_SNAPSHOT_V2", "true")
			db := testutil.RoutingContextDB(t, dialect)
			uc, _, d, repo, _ := snapshotBillingFixture(t, db, false, 1, 1)
			uc.SetRoutingPolicyRepo(NewRoutingPolicyRepo(d))
			ctx := context.Background()
			for _, id := range []int64{9, 10} {
				require.NoError(t, uc.PublishRoutingBillingPolicy(ctx, &routing.BillingPolicy{GroupID: id, BillingMode: routing.SubscriptionOnly, PriceRatio: 0.5}, 0))
			}
			limit := 0.04
			group := &subscriptionbiz.SubscriptionGroup{Name: "shared", Status: 1, RateMultiplier: 2, DailyLimitUSD: &limit}
			require.NoError(t, repo.CreateGroup(ctx, group))
			contract, err := subscriptionbiz.NewContract(group, []subscriptionbiz.RoutingCoverage{{GroupID: 9, GroupKey: "default"}, {GroupID: 10, GroupKey: "other"}})
			require.NoError(t, err)
			now := time.Now().Unix()
			sub := &subscriptionbiz.UserSubscription{UserID: 7001, GroupID: group.ID, Contract: contract, Status: subscriptionbiz.SubscriptionStatusActive, StartsAt: now, ExpiresAt: now + 86400, DailyWindowStart: now, WeeklyWindowStart: now, MonthlyWindowStart: now}
			require.NoError(t, repo.CreateSubscription(ctx, sub))
			uc.SetSubscriptionPrimatives(subscriptionbiz.NewSubscriptionUsecase(repo, repo))
			ctx = routing.WithCostBound(ctx, routing.CostBound{Protocol: "openai_text_embeddings", InputTokens: 100, UpstreamModel: "text-embedding-3-small"})
			// SQLite serializes writes; use one connection for these billing-only calls.
			if dialect == "sqlite" {
				sqlDB, err := db.DB()
				require.NoError(t, err)
				sqlDB.SetMaxOpenConns(1)
			}
			var wg sync.WaitGroup
			reservations := make(chan *biz.Reservation, 16)
			failures := make(chan error, 16)
			for i := 0; i < 16; i++ {
				wg.Add(1)
				go func(i int) {
					defer wg.Done()
					rc := requestContext()
					rc.GroupID = 9 + int64(i%2)
					rc.SubscriptionID = sub.ID
					rc.SubscriptionEntitlementVersion = sub.EntitlementRevision
					r, err := uc.ReserveQuota(ctx, "7001", fmt.Sprintf("shared-%d", i), 100, "m", "1", 0, rc)
					if err != nil {
						failures <- err
					} else {
						reservations <- r
					}
				}(i)
			}
			wg.Wait()
			close(reservations)
			close(failures)
			require.NotEmpty(t, reservations)
			require.NotEmpty(t, failures)
			for err := range failures {
				require.ErrorIs(t, err, biz.ErrSubscriptionQuotaInsufficient)
			}
			var frozen float64
			for r := range reservations {
				frozen += *r.SubscriptionAccountingUSD
				require.NoError(t, uc.ReleaseQuota(ctx, r.ReservationID, "test"))
			}
			require.LessOrEqual(t, frozen, limit+1e-10)
			account, err := d.accountRepo.GetAccountSnapshot(ctx, "7001")
			require.NoError(t, err)
			require.EqualValues(t, 1000000, account.Balance)
			current, err := repo.GetSubscriptionByID(ctx, sub.ID)
			require.NoError(t, err)
			require.Zero(t, current.DailyUsageUSD)
		})
	}
}

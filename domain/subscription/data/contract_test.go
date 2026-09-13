package data

import (
	"context"
	"fmt"
	"github.com/stretchr/testify/require"
	"micro-one-api/domain/subscription/biz"
	"micro-one-api/platform/database/testutil"
	"testing"
	"time"
)

type contractGroups struct{}

func (contractGroups) ValidateSubscriptionGroup(_ context.Context, id int64) (string, error) {
	if id <= 0 {
		return "", biz.ErrSubscriptionContractInvalid
	}
	return fmt.Sprintf("group-%d", id), nil
}
func TestSubscriptionContracts(t *testing.T) {
	for _, driver := range []string{"sqlite", "mysql", "postgres"} {
		t.Run(driver, func(t *testing.T) {
			t.Setenv("SUBSCRIPTION_ENTITLEMENTS_V2", "true")
			db := testutil.RoutingContextDB(t, driver)
			repo := NewRepository(db, nil)
			ctx := context.Background()
			limit := 100.0
			group := &biz.SubscriptionGroup{Name: "q", DisplayName: "Quota", Status: 1, RateMultiplier: 2, DailyLimitUSD: &limit}
			require.NoError(t, repo.CreateGroup(ctx, group))
			plans := biz.NewPlanUsecase(repo, repo)
			plans.SetContractGroupReader(contractGroups{})
			p := &biz.SubscriptionPlan{GroupID: group.ID, Name: "two groups", Coverage: []biz.RoutingCoverage{{GroupID: 2, GrantsAccess: true}, {GroupID: 1}}}
			require.NoError(t, plans.Create(ctx, p))
			require.NoError(t, p.Contract.Validate())
			frozen := p.ToPlanSnapshot()
			read, err := repo.GetPlanByID(ctx, p.ID)
			require.NoError(t, err)
			require.Equal(t, p.Contract.Digest, read.Contract.Digest)
			require.EqualValues(t, 1, read.Coverage[0].GroupID)
			uc := biz.NewSubscriptionUsecase(repo, repo)
			uc.SetTxRunner(NewTxRunner(repo))
			uc.SetContractGroupReader(contractGroups{})
			now := time.Now().Unix()
			req := &biz.AssignSubscriptionRequest{UserID: 100, GroupID: group.ID, StartsAt: now, ExpiresAt: now + 3600, Contract: frozen.Contract, SourceOrder: "order-1"}
			sub, extended, err := uc.AssignOrExtend(ctx, req)
			require.NoError(t, err)
			require.False(t, extended)
			require.Len(t, sub.RoutingGrants(now), 1)
			require.Empty(t, sub.RoutingGrants(now+3600))
			// New plan edits do not rewrite the purchased coverage or quota policy.
			group.RateMultiplier = 7
			require.NoError(t, repo.UpdateGroup(ctx, group))
			p.Coverage = []biz.RoutingCoverage{{GroupID: 3, GrantsAccess: true}}
			require.NoError(t, plans.Update(ctx, p))
			require.NotEqual(t, frozen.Contract.Digest, p.Contract.Digest)
			_, _, err = uc.AssignOrExtend(ctx, &biz.AssignSubscriptionRequest{UserID: 100, GroupID: group.ID, StartsAt: now, ExpiresAt: now + 3600, Contract: p.Contract})
			require.ErrorIs(t, err, biz.ErrSubscriptionContractConflict)
			require.NoError(t, uc.RecordUsage(ctx, 100, 3))
			require.NoError(t, NewTxRunner(repo).RunInTx(ctx, func(ctx context.Context, tx biz.Tx) error {
				return uc.RecordUsageForSubscriptionInTx(ctx, tx, sub.ID, 3, now)
			}))
			renewed, extended, err := uc.AssignOrExtend(ctx, req)
			require.NoError(t, err)
			require.True(t, extended)
			require.EqualValues(t, now+7200, renewed.ExpiresAt)
			require.Equal(t, 12.0, renewed.DailyUsageUSD)
			require.Equal(t, frozen.Contract.Digest, renewed.Contract.Digest)
			require.Greater(t, renewed.EntitlementRevision, sub.EntitlementRevision)
			require.NoError(t, uc.Revoke(ctx, sub.ID, "test"))
			revoked, err := repo.GetSubscriptionByID(ctx, sub.ID)
			require.NoError(t, err)
			require.Empty(t, revoked.RoutingGrants(now))
			var rows int64
			require.NoError(t, db.Table("subscription_routing_entitlements").Where("subscription_id = ?", sub.ID).Count(&rows).Error)
			require.EqualValues(t, 2, rows, "history retains the original coverage even after revocation")
			require.NoError(t, db.Table("routing_change_outbox").Where("owner = ?", "subscription").Count(&rows).Error)
			require.EqualValues(t, 3, rows)
		})
	}
}

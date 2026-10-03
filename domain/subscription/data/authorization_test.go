package data

import (
	"context"
	"github.com/stretchr/testify/require"
	"micro-one-api/domain/authorization"
	authztest "micro-one-api/domain/authorization/testutil"
	"micro-one-api/domain/subscription/biz"
	dbtest "micro-one-api/platform/database/testutil"
	"testing"
	"time"
)

func TestIAMB3SubscriptionOwnerDialects(t *testing.T) {
	for _, driver := range []string{"sqlite", "mysql", "postgres"} {
		t.Run(driver, func(t *testing.T) {
			t.Setenv("SUBSCRIPTION_ENTITLEMENTS_V2", "false")
			db := dbtest.RoutingContextDB(t, driver)
			repo := NewRepository(db, nil)
			legacy := context.Background()
			g1 := &biz.SubscriptionGroup{Name: "g1", Status: 1, RateMultiplier: 1}
			g2 := &biz.SubscriptionGroup{Name: "g2", Status: 1, RateMultiplier: 1}
			require.NoError(t, repo.CreateGroup(legacy, g1))
			require.NoError(t, repo.CreateGroup(legacy, g2))
			p1 := &biz.SubscriptionPlan{Name: "p1", GroupID: g1.ID, ForSale: true, ValidityDays: 30}
			p2 := &biz.SubscriptionPlan{Name: "p2", GroupID: g2.ID, ForSale: true, ValidityDays: 30}
			require.NoError(t, repo.CreatePlan(legacy, p1))
			require.NoError(t, repo.CreatePlan(legacy, p2))
			policy := &authztest.Resolver{ActorID: 1, Scopes: map[string]authorization.QueryScope{"subscription.quota_policy.list": authztest.Resources(g1.ID), "subscription.quota_policy.read": authztest.Resources(g1.ID), "subscription.quota_policy.update": authztest.Resources(g1.ID), "subscription.plan.list": authztest.Resources(p1.ID), "subscription.plan.read": authztest.Resources(p1.ID), "subscription.plan.update": authztest.Resources(p1.ID), "subscription.plan.unpublish": authztest.Resources(p1.ID), "subscription.user_subscription.list": authztest.Users(10), "subscription.user_subscription.read": authztest.Users(10), "subscription.user_subscription.revoke": authztest.Users(10), "subscription.user_subscription.assign": authztest.Users(10), "subscription.user_subscription.extend": authztest.Users(10), "subscription.user_subscription.quota.reset": authztest.Users(10)}}
			groups := biz.NewGroupUsecase(repo)
			groups.SetAuthorization(policy, "admin")
			plans := biz.NewPlanUsecase(repo, repo)
			plans.SetAuthorization(policy, "admin")
			subs := biz.NewSubscriptionUsecase(repo, repo)
			subs.SetAuthorization(policy, "admin")
			subs.SetTxRunner(NewTxRunner(repo))
			ctx := authorization.WithWriteReason(authztest.Context(), "verification")
			visible, err := groups.List(ctx)
			require.NoError(t, err)
			require.Len(t, visible, 1)
			require.Equal(t, g1.ID, visible[0].ID)
			_, err = groups.Get(ctx, g2.ID)
			require.Error(t, err)
			g2.Name = "blocked"
			require.ErrorIs(t, groups.Update(ctx, g2), authorization.ErrDenied)
			plist, err := plans.List(ctx)
			require.NoError(t, err)
			require.Len(t, plist, 1)
			p1.ForSale = false
			require.NoError(t, plans.Update(ctx, p1))
			stored, err := repo.GetPlanByID(legacy, p1.ID)
			require.NoError(t, err)
			require.False(t, stored.ForSale)
			require.NoError(t, plans.SetForSale(biz.WithExpectedRevision(ctx, p1.Revision), p1.ID, false), "write-only lifecycle returns without managed read permission")
			sub, err := subs.Assign(ctx, &biz.AssignSubscriptionRequest{UserID: 10, GroupID: g1.ID, ExpiresAt: time.Now().Add(time.Hour).Unix()})
			require.NoError(t, err)
			other := &biz.UserSubscription{UserID: 20, GroupID: g2.ID, Status: biz.SubscriptionStatusActive, ExpiresAt: time.Now().Add(time.Hour).Unix()}
			require.NoError(t, repo.CreateSubscription(legacy, other))
			list, err := subs.List(ctx)
			require.NoError(t, err)
			require.Len(t, list, 1)
			require.Equal(t, sub.ID, list[0].ID)
			require.ErrorIs(t, subs.Revoke(ctx, other.ID, "blocked"), authorization.ErrDenied)
			require.NoError(t, subs.Extend(ctx, sub.ID, time.Now().Add(2*time.Hour).Unix()))
			require.NoError(t, subs.ResetQuota(ctx, sub.ID, "all"))
			require.NoError(t, subs.Revoke(ctx, sub.ID, "verified"))
			require.ErrorIs(t, subs.Revoke(ctx, other.ID, "blocked"), authorization.ErrDenied)
			var audits int64
			require.NoError(t, db.Table("resource_write_audits").Count(&audits).Error)
			require.GreaterOrEqual(t, audits, int64(6))
			// A mandatory deny wins even when a resource-scoped allow matches.
			q := authztest.Resources(g1.ID)
			q.Deny = []authorization.Scope{{Clauses: []authorization.Clause{{ResourceIDs: []int64{g1.ID}}}}}
			policy.Scopes["subscription.quota_policy.list"] = q
			visible, err = groups.List(ctx)
			require.NoError(t, err)
			require.Empty(t, visible)
			// Self scope is derived from an actual actor session and rejects foreign owners.
			self := biz.WithSelfRequest(ctx)
			_, err = subs.GetProgress(self, 20)
			require.Error(t, err)
		})
	}
}

func TestIAMSubscriptionMemoryReadAndDurability(t *testing.T) {
	repo := NewMemoryRepositoryForTest()
	legacy := context.Background()
	first := &biz.SubscriptionGroup{Name: "one", Status: 1}
	other := &biz.SubscriptionGroup{Name: "two", Status: 1}
	require.NoError(t, repo.CreateGroup(legacy, first))
	require.NoError(t, repo.CreateGroup(legacy, other))
	ctx := authorization.WithQueryScope(legacy, "subscription.quota_policy.list", authztest.Resources(first.ID))
	groups, err := repo.ListGroups(ctx)
	require.NoError(t, err)
	require.Len(t, groups, 1)
	require.Equal(t, first.ID, groups[0].ID)
	_, err = repo.GetGroupByID(ctx, other.ID)
	require.ErrorIs(t, err, biz.ErrSubscriptionGroupNotFound)
	p1 := &biz.SubscriptionPlan{Name: "one", GroupID: first.ID, ForSale: true}
	p2 := &biz.SubscriptionPlan{Name: "two", GroupID: other.ID, ForSale: true}
	require.NoError(t, repo.CreatePlan(legacy, p1))
	require.NoError(t, repo.CreatePlan(legacy, p2))
	plans, err := repo.ListPlans(authorization.WithQueryScope(legacy, "subscription.plan.list", authztest.Resources(p1.ID)))
	require.NoError(t, err)
	require.Len(t, plans, 1)
	s1 := &biz.UserSubscription{UserID: 10, GroupID: first.ID}
	s2 := &biz.UserSubscription{UserID: 20, GroupID: other.ID}
	require.NoError(t, repo.CreateSubscription(legacy, s1))
	require.NoError(t, repo.CreateSubscription(legacy, s2))
	scoped := authorization.WithQueryScope(legacy, "subscription.user_subscription.list", authztest.Users(10))
	subs, err := repo.ListAllSubscriptions(scoped)
	require.NoError(t, err)
	require.Len(t, subs, 1)
	require.EqualValues(t, 10, subs[0].UserID)
	_, err = repo.GetSubscriptionByID(scoped, s2.ID)
	require.ErrorIs(t, err, biz.ErrSubscriptionNotFound)
	write := authorization.WithQueryScope(legacy, "subscription.user_subscription.assign", authztest.All())
	require.ErrorIs(t, repo.CreateSubscription(write, &biz.UserSubscription{UserID: 30}), authorization.ErrWriteStorageUnavailable)
	write = authorization.WithQueryScope(legacy, "subscription.quota_policy.update", authztest.All())
	require.ErrorIs(t, repo.UpdateGroup(write, first), authorization.ErrWriteStorageUnavailable)
	write = authorization.WithQueryScope(legacy, "subscription.plan.update", authztest.All())
	require.ErrorIs(t, repo.UpdatePlan(write, p1), authorization.ErrWriteStorageUnavailable)
}

package data

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"micro-one-api/domain/authorization"
	authztest "micro-one-api/domain/authorization/testutil"
	"micro-one-api/domain/subscription/biz"
	dbtest "micro-one-api/platform/database/testutil"
)

func TestSubscriptionWritesReturnRevisionErrors(t *testing.T) {
	t.Setenv("SUBSCRIPTION_ENTITLEMENTS_V2", "false")
	repo := NewRepository(dbtest.RoutingContextDB(t, "sqlite"), nil)
	ctx := context.Background()
	group := &biz.SubscriptionGroup{Name: "revision", Status: 1, RateMultiplier: 1}
	require.NoError(t, repo.CreateGroup(ctx, group))
	plan := &biz.SubscriptionPlan{GroupID: group.ID, Name: "revision", ForSale: true}
	require.NoError(t, repo.CreatePlan(ctx, plan))
	sub := &biz.UserSubscription{UserID: 10, GroupID: group.ID, Status: biz.SubscriptionStatusActive, ExpiresAt: time.Now().Add(time.Hour).Unix()}
	require.NoError(t, repo.CreateSubscription(ctx, sub))
	write := func(operation string) context.Context {
		return authorization.WithWriteReason(authorization.WithQueryScope(ctx, operation, authztest.All()), "verification")
	}
	for _, tt := range []struct {
		name string
		call func() error
		want error
	}{
		{"group stale", func() error {
			copy := *group
			copy.Revision++
			return repo.UpdateGroup(write("subscription.quota_policy.update"), &copy)
		}, authorization.ErrWriteConflict},
		{"group missing", func() error {
			copy := *group
			copy.Revision = 0
			return repo.UpdateGroup(write("subscription.quota_policy.update"), &copy)
		}, authorization.ErrWritePrecondition},
		{"group delete stale", func() error {
			return repo.DeleteGroup(biz.WithExpectedRevision(write("subscription.quota_policy.delete"), group.Revision+1), group.ID)
		}, authorization.ErrWriteConflict},
		{"group delete missing", func() error { return repo.DeleteGroup(write("subscription.quota_policy.delete"), group.ID) }, authorization.ErrWritePrecondition},
		{"plan stale", func() error {
			copy := *plan
			copy.Revision++
			return repo.UpdatePlan(write("subscription.plan.update"), &copy)
		}, authorization.ErrWriteConflict},
		{"plan missing", func() error {
			copy := *plan
			copy.Revision = 0
			return repo.UpdatePlan(write("subscription.plan.update"), &copy)
		}, authorization.ErrWritePrecondition},
		{"plan delete stale", func() error {
			return repo.DeletePlan(biz.WithExpectedRevision(write("subscription.plan.delete"), plan.Revision+1), plan.ID)
		}, authorization.ErrWriteConflict},
		{"plan delete missing", func() error { return repo.DeletePlan(write("subscription.plan.delete"), plan.ID) }, authorization.ErrWritePrecondition},
		{"plan publish missing", func() error {
			return biz.NewPlanUsecase(repo, repo).SetForSale(write("subscription.plan.publish"), plan.ID, true)
		}, authorization.ErrWritePrecondition},
		{"subscription stale", func() error {
			copy := *sub
			copy.EntitlementRevision++
			return repo.UpdateSubscriptionFields(write("subscription.user_subscription.extend"), &copy, []biz.SubscriptionField{biz.SubscriptionFieldExpiresAt})
		}, authorization.ErrWriteConflict},
		{"subscription expected stale", func() error {
			return repo.UpdateSubscriptionFields(biz.WithExpectedRevision(write("subscription.user_subscription.extend"), sub.EntitlementRevision+1), sub, []biz.SubscriptionField{biz.SubscriptionFieldExpiresAt})
		}, authorization.ErrWriteConflict},
		{"subscription expected missing", func() error {
			return repo.UpdateSubscriptionFields(biz.WithExpectedRevision(write("subscription.user_subscription.extend"), 0), sub, []biz.SubscriptionField{biz.SubscriptionFieldExpiresAt})
		}, authorization.ErrWritePrecondition},
	} {
		t.Run(tt.name, func(t *testing.T) { require.ErrorIs(t, tt.call(), tt.want) })
	}
	storedGroup, err := repo.GetGroupByID(ctx, group.ID)
	require.NoError(t, err)
	require.Equal(t, group.Revision, storedGroup.Revision)
	storedPlan, err := repo.GetPlanByID(ctx, plan.ID)
	require.NoError(t, err)
	require.Equal(t, plan.Revision, storedPlan.Revision)
	storedSub, err := repo.GetSubscriptionByID(ctx, sub.ID)
	require.NoError(t, err)
	require.Equal(t, sub.EntitlementRevision, storedSub.EntitlementRevision)
}

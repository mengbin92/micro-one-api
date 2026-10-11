package data

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"micro-one-api/domain/subscription/biz"
	"micro-one-api/platform/database/testutil"
)

func TestLegacySubscriptionZeroPriceWithoutContractSchema(t *testing.T) {
	t.Setenv("SUBSCRIPTION_ENTITLEMENTS_V2", "false")
	repo := setupSubscriptionTestDB(t)
	ctx := context.Background()
	group := &biz.SubscriptionGroup{Name: "pre-contract schema", Platform: "openai", Status: 1}
	require.NoError(t, repo.CreateGroup(ctx, group))
	sub := &biz.UserSubscription{UserID: 1, GroupID: group.ID, Status: biz.SubscriptionStatusActive, ExpiresAt: time.Now().Add(time.Hour).Unix()}
	require.NoError(t, repo.CreateSubscription(ctx, sub))
	read, err := repo.GetSubscriptionByID(ctx, sub.ID)
	require.NoError(t, err)
	require.Zero(t, read.PricePaid)
	nextGroup := &biz.SubscriptionGroup{Name: "pre-contract next group", Platform: "openai", Status: 1}
	require.NoError(t, repo.CreateGroup(ctx, nextGroup))
	_, err = biz.NewSubscriptionUsecase(repo, repo).ChangeSubscription(ctx, biz.ChangeRequest{UserID: 1, FromSubscriptionID: sub.ID, ToGroupID: nextGroup.ID, Policy: "immediate"})
	require.NoError(t, err, "an unpriced legacy group move must not require the contract price column")
}

func TestLegacySubscriptionPersistsNominalPaidPrice(t *testing.T) {
	t.Setenv("SUBSCRIPTION_ENTITLEMENTS_V2", "false")
	repo := NewRepository(testutil.RoutingContextDB(t, "sqlite"), nil)
	ctx := context.Background()
	group := &biz.SubscriptionGroup{Name: "nominal CNY paid", Status: 1}
	require.NoError(t, repo.CreateGroup(ctx, group))
	sub := &biz.UserSubscription{UserID: 1, GroupID: group.ID, PricePaid: 100, Status: biz.SubscriptionStatusActive, ExpiresAt: time.Now().Add(time.Hour).Unix()}
	require.NoError(t, repo.CreateSubscription(ctx, sub))
	read, err := repo.GetSubscriptionByID(ctx, sub.ID)
	require.NoError(t, err)
	require.EqualValues(t, 100, read.PricePaid)
}

func TestLegacyPricedGroupChangeAdvancesNominalCredit(t *testing.T) {
	t.Setenv("SUBSCRIPTION_ENTITLEMENTS_V2", "false")
	repo := NewRepository(testutil.RoutingContextDB(t, "sqlite"), nil)
	ctx := context.Background()
	oldGroup := &biz.SubscriptionGroup{Name: "CNY 100", Status: 1}
	newGroup := &biz.SubscriptionGroup{Name: "CNY 150", Status: 1}
	require.NoError(t, repo.CreateGroup(ctx, oldGroup))
	require.NoError(t, repo.CreateGroup(ctx, newGroup))
	sub := &biz.UserSubscription{UserID: 1, GroupID: oldGroup.ID, PricePaid: 100, Status: biz.SubscriptionStatusActive, ExpiresAt: time.Now().Add(time.Hour).Unix()}
	require.NoError(t, repo.CreateSubscription(ctx, sub))
	uc := biz.NewSubscriptionUsecase(repo, repo)
	result, err := uc.ChangeSubscription(ctx, biz.ChangeRequest{UserID: 1, FromSubscriptionID: sub.ID, ToGroupID: newGroup.ID, OldPriceQuota: 100, NewPriceQuota: 150, Policy: "immediate"})
	require.NoError(t, err)
	require.True(t, result.Applied)
	read, err := repo.GetSubscriptionByID(ctx, sub.ID)
	require.NoError(t, err)
	require.EqualValues(t, 150, read.PricePaid, "a priced group upgrade must advance the nominal CNY credit too")
}

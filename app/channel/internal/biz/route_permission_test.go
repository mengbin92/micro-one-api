package biz

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"micro-one-api/domain/routing"
)

func TestRoutingCandidateProbeIncludesUnrestrictedChannels(t *testing.T) {
	repo := &mockChannelRepo{channels: map[int64]*Channel{1: {ID: 1, Status: ChannelStatusEnabled, Group: "default"}}}
	uc := NewChannelUsecase(repo, nil)
	ctx := context.Background()
	_, err := uc.SelectChannel(ctx, "default", "unlisted-model", false)
	require.NoError(t, err)
	has, err := uc.HasRoutingCandidates(ctx, &routing.Group{ID: 10, Key: "default", Status: "enabled"}, "unlisted-model")
	require.NoError(t, err)
	require.True(t, has, "ordered routing must see the same catch-all candidate as selection")
}

type failingRoutingRepo struct{ ModelRoutingRepo }

func (failingRoutingRepo) ListModelRoutingsForSelect(context.Context, string, string, string) ([]*ModelRouting, error) {
	return nil, errors.New("policy storage offline")
}

func TestRoutingPolicyFailureClosesSelectionAndPermission(t *testing.T) {
	repo := &mockChannelRepo{accounts: map[int64]*SubscriptionAccount{1: {ID: 1, Status: 1, Platform: "codex"}}, accAbilities: map[string][]SubscriptionAccountAbility{"codex:default:m": {{AccountID: 1, Enabled: true}}}}
	uc := NewChannelUsecase(repo, nil)
	uc.SetModelRoutingRepo(failingRoutingRepo{})
	_, err := uc.SelectSubscriptionAccount(context.Background(), "default", "m", "codex", false)
	require.ErrorContains(t, err, "policy storage offline")
	permission, err := uc.CanRoute(context.Background(), "default", "m", routing.Source{Kind: routing.Subscription, ID: 1})
	require.ErrorContains(t, err, "policy storage offline")
	require.False(t, permission.Allowed)
}

func TestModelRoutingMutationInvalidatesCatalogue(t *testing.T) {
	channels := NewChannelUsecase(&mockChannelRepo{}, nil)
	channels.modelsListCache = newModelsListCache(time.Hour)
	repo := &mockModelRoutingRepo{}
	uc := NewModelRoutingUsecase(repo)
	uc.SetCacheInvalidator(channels)
	channels.modelsListCache.set("vip", []string{"managed"})
	require.NoError(t, uc.UpsertModelRouting(context.Background(), &ModelRouting{ID: 1, GroupName: "vip", Model: "managed", SubscriptionAccountID: 1}))
	_, ok := channels.modelsListCache.get("vip")
	require.False(t, ok)
	channels.modelsListCache.set("vip", []string{"managed"})
	require.NoError(t, uc.DeleteModelRouting(context.Background(), 1))
	_, ok = channels.modelsListCache.get("vip")
	require.False(t, ok)
}

package biz

import (
	"context"
	"errors"
	"testing"

	"github.com/stretchr/testify/require"
	"micro-one-api/domain/routing"
)

type permissionClient struct {
	*recordingChannelClient
	check func(string, string, routing.Source) (routing.Permission, error)
}

func (c permissionClient) CanRoute(_ context.Context, group, model string, source routing.Source) (routing.Permission, error) {
	return c.check(group, model, source)
}

func TestStoredSubscriptionUsesAuthorityRatherThanCSV(t *testing.T) {
	account := &SubscriptionAccount{ID: 7, Status: 1, Platform: "codex", Group: "default", Models: []string{"unrelated"}}
	allowed := true
	client := permissionClient{recordingChannelClient: &recordingChannelClient{byID: map[int64]*SubscriptionAccount{7: account}}, check: func(group, model string, source routing.Source) (routing.Permission, error) {
		require.Equal(t, "vip", group)
		require.Equal(t, "managed", model)
		require.Equal(t, routing.Source{Kind: routing.Subscription, ID: 7}, source)
		return routing.Permission{Allowed: allowed, UpstreamModelID: "new-upstream"}, nil
	}}
	uc := NewRelayUsecase(nil, client, nil, nil)
	channel, _, err := uc.ResolveSubscriptionRoutingSource(context.Background(), 7, "vip", "managed", "managed")
	require.NoError(t, err)
	require.Equal(t, "new-upstream", channel.UpstreamModelID)
	allowed = false
	_, _, err = uc.ResolveSubscriptionRoutingSource(context.Background(), 7, "vip", "managed", "managed")
	require.Error(t, err)
}

func TestRouteAuthorityErrorsDoNotTryAlias(t *testing.T) {
	calls := 0
	client := permissionClient{recordingChannelClient: &recordingChannelClient{}, check: func(_, model string, _ routing.Source) (routing.Permission, error) {
		calls++
		require.Equal(t, "client", model)
		return routing.Permission{}, errors.New("offline")
	}}
	uc := NewRelayUsecase(nil, client, nil, nil)
	permission, err := uc.CanRoute(context.Background(), "vip", "client", "global", routing.Source{Kind: routing.Channel, ID: 1})
	require.Error(t, err)
	require.False(t, permission.Allowed)
	require.Equal(t, 1, calls)
}

func TestRetryRejectsRevokedPrecomputedAndSameSource(t *testing.T) {
	for _, precomputed := range []bool{false, true} {
		t.Run(map[bool]string{false: "same-source", true: "candidate"}[precomputed], func(t *testing.T) {
			client := permissionClient{recordingChannelClient: &recordingChannelClient{}, check: func(group, model string, source routing.Source) (routing.Permission, error) {
				return routing.Permission{}, nil
			}}
			uc := NewRelayUsecase(nil, client, nil, nil)
			uc.retryPolicy = fastRetryPolicy(2)
			initial := &Channel{ID: 1}
			plan := &RelayPlan{Auth: &AuthSnapshot{Group: "vip"}, ClientModel: "client", GlobalModel: "global", Channel: initial}
			if precomputed {
				plan.Candidates = &RoutingCandidateList{Candidates: []RoutingCandidate{{Identity: RoutingSourceIdentity{Kind: UpstreamRouteChannel, ID: 2}, Channel: &Channel{ID: 2}}}}
			}
			calls := 0
			result := uc.NewRetryExecutor().ExecuteWithCandidates(context.Background(), plan, 0, func(context.Context, *Channel) error {
				calls++
				return &RetryableError{Status: 502, Err: errors.New("upstream failed")}
			})
			require.ErrorContains(t, result.Err, "permission revoked")
			require.Equal(t, 1, calls, "revoked source must not reach upstream")
		})
	}
}

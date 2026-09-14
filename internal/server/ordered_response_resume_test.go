package server

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"google.golang.org/grpc"
	commonv1 "micro-one-api/api/common/v1"
	identityv1 "micro-one-api/api/identity/v1"
	"micro-one-api/domain/routing"
	relaybiz "micro-one-api/internal/biz"
)

type orderedResumeIdentity struct{ rawIdentityClient }

func (orderedResumeIdentity) GetAuthSnapshot(context.Context, *identityv1.GetAuthSnapshotRequest, ...grpc.CallOption) (*identityv1.GetAuthSnapshotReply, error) {
	return &identityv1.GetAuthSnapshotReply{UserId: 1, TokenId: 2, UserEnabled: true, TokenEnabled: true, Group: "default", RoutingContextVersion: 2,
		RoutingFacts: &commonv1.RoutingSubjectFacts{DefaultGroupId: 3, PublicGroupAccess: "all", AccessRevision: 1, TokenMode: "ordered", TokenRevision: 1, TokenGroupIds: []int64{3, 7}}}, nil
}

type orderedResumeChannel struct {
	orderedGateChannel
	disabled  bool
	denyFirst bool
}

func (c *orderedResumeChannel) GetRoutingGroup(_ context.Context, id int64) (*routing.Group, error) {
	key, status, access := "empty", "enabled", "public"
	if id == 7 {
		key = "bound"
		if c.disabled {
			status = "disabled"
		}
	} else if c.denyFirst {
		access = "restricted"
	}
	return &routing.Group{ID: id, Key: key, Status: status, AccessMode: access, ModelAccessMode: "all_authorized", Revision: 1}, nil
}
func (c *orderedResumeChannel) CanRoute(_ context.Context, group, _ string, _ routing.Source) (routing.Permission, error) {
	return routing.Permission{Allowed: group == "bound"}, nil
}

func TestOrderedStoredResponseResumesBoundGroup(t *testing.T) {
	t.Setenv("RELAY_ROUTING_CONTEXT_V2", "true")
	t.Setenv("RELAY_ROUTING_ORDERED", "true")
	channel := &orderedResumeChannel{}
	s := &HTTPServer{identityClient: orderedResumeIdentity{}, channelClient: rawChannelClient{}, relayUsecase: relaybiz.NewRelayUsecase(nil, channel, nil, nil),
		responseRoutes: map[string]responseRouteEntry{"resp_bound": {route: responseRoute{Model: "gpt-5", UserID: 1, TokenID: 2, RoutingGroupID: 7, Channel: relaybiz.Channel{ID: 11}}, expiresAt: time.Now().Add(time.Hour)}}}
	for _, denyFirst := range []bool{false, true} {
		channel.denyFirst = denyFirst
		plan, ok := NewOpenAIWSRoutingScheduler(s).ResolveStoredRoute(context.Background(), "token", "gpt-5", "resp_bound")
		require.True(t, ok, "earlier candidate must not replace or block the bound route")
		require.EqualValues(t, 7, plan.Auth.RoutingContext.GroupID)
		require.Equal(t, "bound", plan.Auth.Group)
	}
	channel.disabled = true
	_, ok := NewOpenAIWSRoutingScheduler(s).ResolveStoredRoute(context.Background(), "token", "gpt-5", "resp_bound")
	require.False(t, ok, "disabled bound group must reject without advancing")
}

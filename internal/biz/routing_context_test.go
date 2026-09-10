package biz

import (
	"context"
	"github.com/stretchr/testify/require"
	"micro-one-api/domain/routing"
	"testing"
)

type routingContextChannel struct {
	testChannelClient
	group *routing.Group
}

func (c routingContextChannel) GetRoutingGroup(context.Context, int64) (*routing.Group, error) {
	return c.group, nil
}

func TestRelayRoutingContextUsesExplicitIdentityFacts(t *testing.T) {
	t.Setenv("RELAY_ROUTING_CONTEXT_V2", "true")
	channel := routingContextChannel{group: &routing.Group{ID: 3, Key: "default", Status: "enabled", Revision: 1}}
	uc := NewRelayUsecase(testIdentityClient{}, channel, nil, nil)
	auth := &AuthSnapshot{UserID: 1, TokenID: 2, Group: "default", UserEnabled: true, TokenEnabled: true}
	require.Error(t, uc.ResolveRoutingContext(context.Background(), auth))
	auth.RoutingContextVersion = 2
	auth.RoutingFacts = &routing.SubjectFacts{DefaultGroupID: 3, PublicGroupAccess: "explicit_only", AccessRevision: 1, TokenMode: "inherit", TokenRevision: 1, Grants: []routing.UserGroupGrant{{GroupID: 3, Status: "active"}}}
	require.NoError(t, uc.ResolveRoutingContext(context.Background(), auth))
	require.EqualValues(t, 3, auth.RoutingContext.GroupID)
	auth.Group = "other"
	require.Error(t, uc.ResolveRoutingContext(context.Background(), auth))
	auth.Group = "default"
	channel.group.Status = "disabled"
	require.Error(t, uc.ResolveRoutingContext(context.Background(), auth))
}

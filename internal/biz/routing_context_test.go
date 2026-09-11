package biz

import (
	"context"
	"fmt"
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

type fixedIdentityFake struct {
	revoked bool
	groupID int64
}

func (f *fixedIdentityFake) GetAuthSnapshot(_ context.Context, key, _ string) (*AuthSnapshot, error) {
	id := int64(2)
	if key == "key-b" {
		id = 3
	}
	group := f.groupID
	if group == 0 {
		group = id
	}
	grants := []routing.UserGroupGrant{{GroupID: group, Status: "active"}}
	if f.revoked {
		grants = nil
	}
	return &AuthSnapshot{UserID: 1, TokenID: id, Group: "default", UserEnabled: true, TokenEnabled: true, RoutingContextVersion: 2, RoutingFacts: &routing.SubjectFacts{DefaultGroupID: 1, TokenMode: "fixed", TokenGroupID: group, TokenRevision: 1, AccessRevision: 1, PublicGroupAccess: "explicit_only", Grants: grants}}, nil
}

type fixedChannelFake struct {
	testChannelClient
	disabled bool
	selected []string
}

func (c *fixedChannelFake) GetRoutingGroup(_ context.Context, id int64) (*routing.Group, error) {
	status := "enabled"
	if c.disabled {
		status = "disabled"
	}
	return &routing.Group{ID: id, Key: fmt.Sprintf("group-%d", id), Status: status, Revision: 1}, nil
}
func (c *fixedChannelFake) SelectChannel(ctx context.Context, group, model string, b bool) (*Channel, error) {
	c.selected = append(c.selected, group)
	return c.testChannelClient.SelectChannel(ctx, group, model, b)
}
func (c *fixedChannelFake) CanRoute(context.Context, string, string, routing.Source) (routing.Permission, error) {
	return routing.Permission{Allowed: !c.disabled}, nil
}
func TestFixedRoutingPlanAndRevocationBeforeRetry(t *testing.T) {
	t.Setenv("ADMIN_ROUTING_FIXED_KEYS", "false") // creation rollback does not reinterpret existing Keys
	t.Setenv("RELAY_ROUTING_CONTEXT_V2", "true")
	ctx := context.Background()
	identity := &fixedIdentityFake{}
	channel := &fixedChannelFake{}
	uc := NewRelayUsecase(identity, channel, nil, nil)
	for _, key := range []string{"key-a", "key-b"} {
		plan, err := uc.Plan(ctx, RelayRequest{Token: key, Model: "m"})
		require.NoError(t, err)
		require.Equal(t, plan.Auth.RoutingContext.GroupKey, plan.Auth.Group)
		require.Contains(t, plan.Channel.Name, plan.Auth.Group)
	}
	require.Contains(t, channel.selected, "group-2")
	require.Contains(t, channel.selected, "group-3")
	plan, err := uc.Plan(ctx, RelayRequest{Token: "key-a", Model: "m"})
	require.NoError(t, err)
	identity.revoked = true
	calls := 0
	result := uc.NewRetryExecutor().ExecuteWithCandidates(ctx, plan, 0, func(context.Context, *Channel) error { calls++; return nil })
	require.Error(t, result.Err)
	require.Zero(t, calls)
	identity.revoked = false
	identity.groupID = 3
	result = uc.NewRetryExecutor().ExecuteWithCandidates(ctx, plan, 0, func(context.Context, *Channel) error { calls++; return nil })
	require.Error(t, result.Err)
	require.Zero(t, calls, "a changed group cannot reuse the old reservation")
	identity.groupID = 0
	channel.disabled = true
	_, err = uc.Plan(ctx, RelayRequest{Token: "key-a", Model: "m"})
	require.Error(t, err)
}

type liveEntitlements struct {
	facts *routing.EntitlementFacts
	err   error
}

func (e *liveEntitlements) GetRoutingEntitlements(context.Context, int64) (*routing.EntitlementFacts, error) {
	return e.facts, e.err
}
func TestSubscriptionGrantRecheckedBeforeRetry(t *testing.T) {
	t.Setenv("SUBSCRIPTION_ENTITLEMENTS_V2", "true")
	t.Setenv("RELAY_ROUTING_CONTEXT_V2", "true")
	identity := &fixedIdentityFake{revoked: true}
	channel := &fixedChannelFake{}
	uc := NewRelayUsecase(identity, channel, nil, nil)
	e := &liveEntitlements{facts: &routing.EntitlementFacts{SubscriptionID: 50, Revision: 3, Grants: []routing.UserGroupGrant{{GroupID: 2, SourceType: "subscription", SourceRef: "50", Status: "active"}}}}
	uc.SetRoutingEntitlements(e)
	ctx := context.Background()
	plan, err := uc.Plan(ctx, RelayRequest{Token: "key-a", Model: "m"})
	require.NoError(t, err)
	require.EqualValues(t, 50, plan.Auth.RoutingContext.SubscriptionID)
	e.facts = &routing.EntitlementFacts{}
	calls := 0
	result := uc.NewRetryExecutor().ExecuteWithCandidates(ctx, plan, 0, func(context.Context, *Channel) error { calls++; return nil })
	require.Error(t, result.Err)
	require.Zero(t, calls)
	e.err = fmt.Errorf("subscription database unavailable")
	_, err = uc.Plan(ctx, RelayRequest{Token: "key-a", Model: "m"})
	require.Error(t, err)
	e.err = nil
	require.Error(t, uc.ResolveRoutingContext(ctx, plan.Auth), "reusing a request snapshot must replace old subscription grants")
}

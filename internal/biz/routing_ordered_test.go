package biz

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"micro-one-api/domain/routing"
)

// orderedIdentityFake serves an ordered token with a configurable grant set.
type orderedIdentityFake struct {
	groupIDs []int64
	grants   []int64
}

func (f *orderedIdentityFake) GetAuthSnapshot(_ context.Context, _, _ string) (*AuthSnapshot, error) {
	grants := make([]routing.UserGroupGrant, 0, len(f.grants))
	for _, gid := range f.grants {
		grants = append(grants, routing.UserGroupGrant{GroupID: gid, Status: "active"})
	}
	return &AuthSnapshot{
		UserID: 1, TokenID: 2, Group: "default", UserEnabled: true, TokenEnabled: true,
		RoutingContextVersion: routing.ContextVersion,
		RoutingFacts: &routing.SubjectFacts{
			DefaultGroupID: 1, TokenMode: "ordered", TokenGroupIDs: f.groupIDs,
			TokenRevision: 1, AccessRevision: 1, PublicGroupAccess: "explicit_only", Grants: grants,
		},
	}, nil
}

// orderedChannelFake serves per-group metadata plus the candidate probe.
type orderedChannelFake struct {
	testChannelClient
	statuses   map[int64]string
	candidates map[int64]bool
	probed     []int64
	selected   []string
}

func (c *orderedChannelFake) GetRoutingGroup(_ context.Context, id int64) (*routing.Group, error) {
	status, ok := c.statuses[id]
	if !ok {
		return nil, fmt.Errorf("routing group %d not found", id)
	}
	return &routing.Group{ID: id, Key: fmt.Sprintf("group-%d", id), Status: status, Revision: 1}, nil
}

func (c *orderedChannelFake) HasRoutingCandidates(_ context.Context, groupID int64, _ string) (bool, error) {
	c.probed = append(c.probed, groupID)
	return c.candidates[groupID], nil
}

func (c *orderedChannelFake) SelectChannel(_ context.Context, group, model string, _ bool) (*Channel, error) {
	c.selected = append(c.selected, group)
	return &Channel{ID: 1, Name: group + ":" + model, BaseURL: "https://api.openai.com/v1"}, nil
}

type settlementFake struct {
	allowed map[int64]bool
	err     error
	called  []int64
}

func (s *settlementFake) CheckRoutingSettlement(_ context.Context, _, groupID int64) (bool, string, error) {
	s.called = append(s.called, groupID)
	if s.err != nil {
		return false, "", s.err
	}
	return s.allowed[groupID], "settlement unavailable", nil
}

type stickyStoreFake struct {
	bound map[string]int64
}

func (s *stickyStoreFake) LookupSessionChannel(_ context.Context, _, sessionHash string) int64 {
	return s.bound[sessionHash]
}
func (s *stickyStoreFake) RefreshSessionTTL(context.Context, string, string, time.Duration) bool {
	return true
}

func orderedFixture(groupIDs, grants []int64) (*orderedIdentityFake, *orderedChannelFake, *RelayUsecase) {
	identity := &orderedIdentityFake{groupIDs: groupIDs, grants: grants}
	channel := &orderedChannelFake{statuses: map[int64]string{}, candidates: map[int64]bool{}}
	uc := NewRelayUsecase(identity, channel, nil, nil)
	return identity, channel, uc
}

func orderedFactsAuth(t *testing.T, uc *RelayUsecase) *AuthSnapshot {
	t.Helper()
	auth, err := uc.identity.GetAuthSnapshot(context.Background(), "key", "")
	require.NoError(t, err)
	return auth
}

func TestOrderedRoutingGateAndListValidation(t *testing.T) {
	t.Setenv("RELAY_ROUTING_CONTEXT_V2", "true")
	// Gate off by default: ordered tokens fail closed.
	_, channel, uc := orderedFixture([]int64{10, 20}, []int64{10, 20})
	channel.statuses[10], channel.statuses[20] = "enabled", "enabled"
	channel.candidates[10], channel.candidates[20] = true, true
	auth := orderedFactsAuth(t, uc)
	require.Error(t, uc.ResolveRoutingContext(context.Background(), auth, RoutingResolveOptions{Model: "m"}))

	t.Setenv("RELAY_ROUTING_ORDERED", "true")
	auth = orderedFactsAuth(t, uc)
	require.NoError(t, uc.ResolveRoutingContext(context.Background(), auth, RoutingResolveOptions{Model: "m"}))
	require.EqualValues(t, 10, auth.RoutingContext.GroupID)
	require.EqualValues(t, 0, auth.RoutingContext.AttemptOrdinal)
	require.Equal(t, []int64{10, 20}, auth.RoutingContext.CandidateGroupIDs)
	require.Equal(t, "group-10", auth.Group)

	// An empty/invalid candidate list never falls back to the global list.
	identity, channel2, uc2 := orderedFixture(nil, nil)
	_ = identity
	channel2.statuses[1] = "enabled"
	channel2.candidates[1] = true
	auth2 := orderedFactsAuth(t, uc2)
	require.Error(t, uc2.ResolveRoutingContext(context.Background(), auth2, RoutingResolveOptions{Model: "m"}))
}

func TestOrderedRoutingAdvanceAndTerminateRules(t *testing.T) {
	t.Setenv("RELAY_ROUTING_CONTEXT_V2", "true")
	t.Setenv("RELAY_ROUTING_ORDERED", "true")
	ctx := context.Background()

	// Disabled first group advances; user order is preserved.
	_, channel, uc := orderedFixture([]int64{10, 20, 30}, []int64{10, 20, 30})
	channel.statuses[10], channel.statuses[20], channel.statuses[30] = "disabled", "enabled", "enabled"
	channel.candidates[20], channel.candidates[30] = true, true
	auth := orderedFactsAuth(t, uc)
	require.NoError(t, uc.ResolveRoutingContext(ctx, auth, RoutingResolveOptions{Model: "m"}))
	require.EqualValues(t, 20, auth.RoutingContext.GroupID)
	require.EqualValues(t, 1, auth.RoutingContext.AttemptOrdinal)

	// Deleted group also advances.
	delete(channel.statuses, 30)
	auth = orderedFactsAuth(t, uc)
	require.NoError(t, uc.ResolveRoutingContext(ctx, auth, RoutingResolveOptions{Model: "m"}))
	require.EqualValues(t, 20, auth.RoutingContext.GroupID)

	// No candidate resources advances to the next group.
	channel.candidates[20] = false
	channel.candidates[30] = true
	channel.statuses[30] = "enabled"
	channel.probed = nil
	auth = orderedFactsAuth(t, uc)
	require.NoError(t, uc.ResolveRoutingContext(ctx, auth, RoutingResolveOptions{Model: "m"}))
	require.EqualValues(t, 30, auth.RoutingContext.GroupID)
	require.Equal(t, []int64{20, 30}, channel.probed)

	// No access terminates: later groups are never probed.
	identity, channel, uc := orderedFixture([]int64{10, 20}, []int64{20}) // no grant for 10
	identity.grants = []int64{20}
	channel.statuses[10], channel.statuses[20] = "enabled", "enabled"
	channel.candidates[10], channel.candidates[20] = true, true
	channel.probed = nil
	auth = orderedFactsAuth(t, uc)
	require.Error(t, uc.ResolveRoutingContext(ctx, auth, RoutingResolveOptions{Model: "m"}))
	require.Empty(t, channel.probed, "termination must not probe later candidates")

	// Exhausted list terminates without global fallback.
	identity, channel, uc = orderedFixture([]int64{10, 20}, []int64{10, 20})
	channel.statuses[10], channel.statuses[20] = "enabled", "enabled"
	channel.candidates[10], channel.candidates[20] = false, false
	auth = orderedFactsAuth(t, uc)
	require.Error(t, uc.ResolveRoutingContext(ctx, auth, RoutingResolveOptions{Model: "m"}))
}

func TestOrderedRoutingSettlementTermination(t *testing.T) {
	t.Setenv("RELAY_ROUTING_CONTEXT_V2", "true")
	t.Setenv("RELAY_ROUTING_ORDERED", "true")
	ctx := context.Background()
	identity, channel, uc := orderedFixture([]int64{10, 20}, []int64{10, 20})
	channel.statuses[10], channel.statuses[20] = "enabled", "enabled"
	channel.candidates[10], channel.candidates[20] = true, true

	// Capability absent: probe skipped, resolution proceeds.
	auth := orderedFactsAuth(t, uc)
	require.NoError(t, uc.ResolveRoutingContext(ctx, auth, RoutingResolveOptions{Model: "m"}))

	// Settlement denial terminates (never advances to the pricier group).
	settlement := &settlementFake{allowed: map[int64]bool{10: false, 20: true}}
	uc.SetRoutingSettlementClient(settlement)
	channel.probed = nil
	auth = orderedFactsAuth(t, uc)
	require.Error(t, uc.ResolveRoutingContext(ctx, auth, RoutingResolveOptions{Model: "m"}))
	require.Equal(t, []int64{10}, settlement.called)
	require.Empty(t, channel.probed, "settlement termination happens before the candidate probe")

	// Settlement RPC failure fails closed.
	settlement.err = fmt.Errorf("billing unavailable")
	auth = orderedFactsAuth(t, uc)
	require.Error(t, uc.ResolveRoutingContext(ctx, auth, RoutingResolveOptions{Model: "m"}))

	// Settlement allowed: probe runs and the group resolves.
	settlement.err = nil
	settlement.allowed = map[int64]bool{10: true, 20: true}
	channel.probed = nil
	auth = orderedFactsAuth(t, uc)
	require.NoError(t, uc.ResolveRoutingContext(ctx, auth, RoutingResolveOptions{Model: "m"}))
	require.EqualValues(t, 10, auth.RoutingContext.GroupID)
	require.Equal(t, []int64{10}, channel.probed)
	_ = identity
}

func TestOrderedRoutingStickyScanPrefersBoundGroup(t *testing.T) {
	t.Setenv("RELAY_ROUTING_CONTEXT_V2", "true")
	t.Setenv("RELAY_ROUTING_ORDERED", "true")
	ctx := context.Background()
	_, channel, uc := orderedFixture([]int64{10, 20}, []int64{10, 20})
	channel.statuses[10], channel.statuses[20] = "enabled", "enabled"
	channel.candidates[10], channel.candidates[20] = true, true
	store := &stickyStoreFake{bound: map[string]int64{
		"v2/u1/t2/g20/conv-1": 7,
	}}
	uc.SetSessionAccountStore(store, time.Minute, true)

	// The earlier group has candidates, but the bound conversation stays on
	// its original group; the candidate probe never runs for group 10.
	auth := orderedFactsAuth(t, uc)
	require.NoError(t, uc.ResolveRoutingContext(ctx, auth, RoutingResolveOptions{SessionHash: "conv-1", Model: "m"}))
	require.EqualValues(t, 20, auth.RoutingContext.GroupID)
	require.Empty(t, channel.probed)

	// A stale binding on a disabled group is a hard error: the conversation
	// must not silently move to another upstream.
	channel.statuses[20] = "disabled"
	auth = orderedFactsAuth(t, uc)
	require.Error(t, uc.ResolveRoutingContext(ctx, auth, RoutingResolveOptions{SessionHash: "conv-1", Model: "m"}))
}

func TestOrderedRoutingBoundRevalidationNeverAdvances(t *testing.T) {
	t.Setenv("RELAY_ROUTING_CONTEXT_V2", "true")
	t.Setenv("RELAY_ROUTING_ORDERED", "true")
	ctx := context.Background()
	identity, channel, uc := orderedFixture([]int64{10, 20}, []int64{10, 20})
	channel.statuses[10], channel.statuses[20] = "enabled", "enabled"
	channel.candidates[10], channel.candidates[20] = true, true

	auth := orderedFactsAuth(t, uc)
	require.NoError(t, uc.ResolveRoutingContext(ctx, auth, RoutingResolveOptions{Model: "m"}))
	require.EqualValues(t, 10, auth.RoutingContext.GroupID)
	bound := auth.RoutingContext.GroupID

	// Bound re-resolution stays on the original group even when the earlier
	// state changed.
	channel.candidates[10] = false
	channel.probed = nil
	auth = orderedFactsAuth(t, uc)
	require.NoError(t, uc.ResolveRoutingContext(ctx, auth, RoutingResolveOptions{Model: "m", BoundGroupID: bound}))
	require.EqualValues(t, 10, auth.RoutingContext.GroupID)
	require.Empty(t, channel.probed, "bound revalidation never probes candidates")

	// Access revoked: hard failure, no advance.
	identity.grants = []int64{20}
	auth = orderedFactsAuth(t, uc)
	require.Error(t, uc.ResolveRoutingContext(ctx, auth, RoutingResolveOptions{Model: "m", BoundGroupID: bound}))

	// Group dropped from the token list: the binding is stale.
	identity.grants = []int64{10, 20}
	identity.groupIDs = []int64{20}
	auth = orderedFactsAuth(t, uc)
	require.Error(t, uc.ResolveRoutingContext(ctx, auth, RoutingResolveOptions{Model: "m", BoundGroupID: bound}))
}

func TestOrderedRoutingRecheckKeepsBoundGroup(t *testing.T) {
	t.Setenv("RELAY_ROUTING_CONTEXT_V2", "true")
	t.Setenv("RELAY_ROUTING_ORDERED", "true")
	ctx := context.Background()
	identity, channel, uc := orderedFixture([]int64{10, 20}, []int64{10, 20})
	channel.statuses[10], channel.statuses[20] = "enabled", "enabled"
	channel.candidates[10], channel.candidates[20] = true, true

	plan, err := uc.Plan(ctx, RelayRequest{Token: "key", Model: "m"})
	require.NoError(t, err)
	require.EqualValues(t, 10, plan.Auth.RoutingContext.GroupID)
	originalDigest := plan.Auth.RoutingContext.Digest()

	// Mid-request state changes must not move the bound group.
	channel.candidates[10] = false
	channel.candidates[20] = true
	require.NoError(t, RecheckRoutingAdmission(ctx, plan.Auth, "m"))
	require.EqualValues(t, 10, plan.Auth.RoutingContext.GroupID)

	// Access revocation on the bound group hard-fails the recheck.
	identity.grants = []int64{20}
	require.Error(t, RecheckRoutingAdmission(ctx, plan.Auth, "m"))

	// Attempt ordinals make per-attempt digests unique: the same token at a
	// different ordinal is a different frozen context.
	identity.grants = []int64{10, 20}
	channel.statuses[10] = "disabled"
	auth := orderedFactsAuth(t, uc)
	require.NoError(t, uc.ResolveRoutingContext(ctx, auth, RoutingResolveOptions{Model: "m"}))
	require.EqualValues(t, 20, auth.RoutingContext.GroupID)
	require.NotEqual(t, originalDigest, auth.RoutingContext.Digest())
}

func TestOrderedRoutingPlanSelectsWithinResolvedGroup(t *testing.T) {
	t.Setenv("RELAY_ROUTING_CONTEXT_V2", "true")
	t.Setenv("RELAY_ROUTING_ORDERED", "true")
	ctx := context.Background()
	_, channel, uc := orderedFixture([]int64{10, 20}, []int64{10, 20})
	channel.statuses[10], channel.statuses[20] = "enabled", "enabled"
	channel.candidates[10], channel.candidates[20] = false, true

	plan, err := uc.Plan(ctx, RelayRequest{Token: "key", Model: "m"})
	require.NoError(t, err)
	require.EqualValues(t, 20, plan.Auth.RoutingContext.GroupID)
	require.Equal(t, "group-20", plan.Auth.Group)
	require.Equal(t, []string{"group-20"}, channel.selected, "channel selection runs inside the resolved group")
}

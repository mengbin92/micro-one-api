package server

import (
	"context"
	"fmt"
	commonv1 "micro-one-api/api/common/v1"
	identityv1 "micro-one-api/api/identity/v1"
	relaybiz "micro-one-api/internal/biz"
	"testing"
	"time"

	coderws "github.com/coder/websocket"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc"
	billingv1 "micro-one-api/api/billing/v1"
	"micro-one-api/domain/routing"
)

type routingBillingFake struct {
	billingv1.BillingServiceClient
	version            int32
	mismatch           bool
	reserves, releases int
}

func (f *routingBillingFake) GetRoutingCapabilities(context.Context, *billingv1.GetRoutingCapabilitiesRequest, ...grpc.CallOption) (*billingv1.GetRoutingCapabilitiesResponse, error) {
	return &billingv1.GetRoutingCapabilitiesResponse{RequestSnapshotVersion: f.version}, nil
}
func (f *routingBillingFake) ReserveQuota(_ context.Context, r *billingv1.ReserveQuotaRequest, _ ...grpc.CallOption) (*billingv1.ReserveQuotaResponse, error) {
	f.reserves++
	c := routingContextTestValue()
	hash := c.Digest()
	if f.mismatch {
		hash = "mismatch"
	}
	if r.RoutingContext == nil || r.RoutingContext.GroupId != c.GroupID || r.RoutingContext.GroupKey != c.GroupKey {
		return nil, fmt.Errorf("actual group context was lost")
	}
	return &billingv1.ReserveQuotaResponse{Success: true, ReservationId: "reserved", RequestSnapshotVersion: 2, RoutingContextHash: hash, RequestSnapshotHash: hash}, nil
}
func (f *routingBillingFake) ReleaseQuota(context.Context, *billingv1.ReleaseQuotaRequest, ...grpc.CallOption) (*billingv1.ReleaseQuotaResponse, error) {
	f.releases++
	return &billingv1.ReleaseQuotaResponse{}, nil
}
func routingContextTestValue() *routing.ResolvedRoutingContext {
	return &routing.ResolvedRoutingContext{Version: 2, UserID: 1, TokenID: 2, GroupID: 3, GroupKey: "actual", TokenMode: "inherit", TokenRevision: 1, UserAccessRevision: 1, GroupRevision: 1, SelectionSource: "user_default"}
}

func TestRoutingContextCapabilityBeforeReserve(t *testing.T) {
	t.Setenv("RELAY_ROUTING_CONTEXT_V2", "true")
	f := &routingBillingFake{}
	s := &HTTPServer{billingClient: f}
	ctx := context.Background()
	_, err := s.reserveQuota(ctx, "1", "req", 100, "m", "7", 0, routingContextTestValue())
	require.Error(t, err)
	require.Zero(t, f.reserves)
	f.version = 2
	_, err = s.reserveQuota(ctx, "1", "req", 100, "m", "7", 0)
	require.Error(t, err)
	require.Zero(t, f.reserves)
	_, err = s.reserveQuota(ctx, "1", "req", 100, "m", "7", 0, routingContextTestValue())
	require.NoError(t, err)
	require.Equal(t, 1, f.reserves)
	f.mismatch = true
	_, err = s.reserveQuota(ctx, "1", "next", 100, "m", "7", 0, routingContextTestValue())
	require.Error(t, err)
	require.Equal(t, 1, f.releases)
}

func TestRoutingWebsocketReserveBeforeEachTurn(t *testing.T) {
	ctx := context.Background()
	create := []byte(`{"type":"response.create","model":"m"}`)
	calls := 0
	turns := &routingWSTurns{active: &billingv1.ReserveQuotaResponse{ReservationId: "first"}, admit: func(context.Context, []byte) (*billingv1.ReserveQuotaResponse, []byte, error) {
		calls++
		return &billingv1.ReserveQuotaResponse{ReservationId: "second"}, []byte("rewritten"), nil
	}}
	_, err := turns.beforeWrite(ctx, coderws.MessageText, create)
	require.NoError(t, err)
	require.Zero(t, calls)
	_, err = turns.beforeWrite(ctx, coderws.MessageText, create)
	require.ErrorIs(t, err, errWSRoutingAdmission)
	require.Zero(t, calls)
	require.Equal(t, "first", turns.take().ReservationId)
	rewritten, err := turns.beforeWrite(ctx, coderws.MessageText, create)
	require.NoError(t, err)
	require.Equal(t, 1, calls)
	require.Equal(t, []byte("rewritten"), rewritten)
	require.Equal(t, "second", turns.take().ReservationId)
	turns.admit = func(context.Context, []byte) (*billingv1.ReserveQuotaResponse, []byte, error) {
		return nil, nil, fmt.Errorf("group revoked")
	}
	_, err = turns.beforeWrite(ctx, coderws.MessageText, create)
	require.ErrorIs(t, err, errWSRoutingAdmission)
	require.Nil(t, turns.take())
}

func TestFixedContextRequiresExplicitBillingCapability(t *testing.T) {
	t.Setenv("RELAY_ROUTING_CONTEXT_V2", "true")
	f := &routingBillingFake{version: 2}
	s := &HTTPServer{billingClient: f}
	c := routingContextTestValue()
	c.TokenMode = "fixed"
	c.SelectionSource = "token_fixed"
	_, err := s.reserveQuota(context.Background(), "1", "fixed", 100, "m", "7", 0, c)
	require.Error(t, err)
	require.Zero(t, f.reserves, "an older v2 billing instance must not accept fixed requests")
}
func TestResponseCacheRejectsOtherKeyOrGroup(t *testing.T) {
	s := &HTTPServer{}
	auth := &identityv1.GetAuthSnapshotReply{UserId: 1, TokenId: 2, RoutingContextVersion: 2, RoutingFacts: &commonv1.RoutingSubjectFacts{TokenMode: "fixed", TokenGroupId: 3}}
	for _, route := range []responseRoute{{UserID: 1, TokenID: 9, RoutingGroupID: 3}, {UserID: 1, TokenID: 2, RoutingGroupID: 9}, {UserID: 1}, {UserID: 9, TokenID: 2, RoutingGroupID: 3}} {
		_, allowed := s.refreshStoredResponseRoute(context.Background(), auth, "m", route)
		require.False(t, allowed)
	}
	a := &relaybiz.AuthSnapshot{UserID: 1, TokenID: 2, RoutingContext: &routing.ResolvedRoutingContext{GroupID: 3}}
	b := &relaybiz.AuthSnapshot{UserID: 1, TokenID: 4, RoutingContext: &routing.ResolvedRoutingContext{GroupID: 3}}
	store := newOpenAIWSStickyStore(nil)
	store.BindResponseChannel(context.Background(), routingSessionScope(a), "resp-known", 99, time.Minute)
	require.EqualValues(t, 99, store.LookupResponseChannel(context.Background(), routingSessionScope(a), "resp-known"))
	require.Zero(t, store.LookupResponseChannel(context.Background(), routingSessionScope(b), "resp-known"))
}

package data

import (
	"context"
	"net"
	"sync/atomic"
	"testing"

	"github.com/stretchr/testify/require"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/status"
	"google.golang.org/grpc/test/bufconn"
	channelv1 "micro-one-api/api/channel/v1"
	"micro-one-api/domain/routing"
	grpcauth "micro-one-api/platform/grpc"
	"micro-one-api/platform/grpc/xgrpc"
	"micro-one-api/platform/security/serviceidentity"
)

type routeAuthorityFixture struct {
	channelv1.UnimplementedChannelServiceServer
	allowed      atomic.Bool
	sourceChecks atomic.Int64
}

func (f *routeAuthorityFixture) ListRoutingGroups(context.Context, *channelv1.ListRoutingGroupsRequest) (*channelv1.ListRoutingGroupsReply, error) {
	return &channelv1.ListRoutingGroupsReply{Groups: []*channelv1.RoutingGroup{{Id: 7, Key: "bound", Status: "enabled"}}}, nil
}
func (f *routeAuthorityFixture) CheckRoute(_ context.Context, req *channelv1.CheckRouteRequest) (*channelv1.CheckRouteReply, error) {
	if req.RoutingGroupId != 7 || req.Group != "bound" || req.SourceId != 9 {
		return nil, status.Error(codes.InvalidArgument, "routing authority facts differ")
	}
	f.sourceChecks.Add(1)
	return &channelv1.CheckRouteReply{Allowed: f.allowed.Load(), UpstreamModelId: "upstream-model"}, nil
}

// The stored Responses path uses this actual adapter to resolve its bound key
// and recheck the source. Exercise the real fixed-caller interceptor too: a
// direct fake client would miss the catalog RPC that the relay could not call.
func TestStoredSourceChecksUseDedicatedRelayCapabilities(t *testing.T) {
	t.Setenv("RELAY_ROUTING_CONTEXT_V2", "true")
	verifier, err := serviceidentity.NewVerifier(map[string]string{"relay": "relay-fixture", "monitor": "monitor-fixture"}, "shared-fixture")
	require.NoError(t, err)
	server := grpc.NewServer(grpc.UnaryInterceptor(xgrpc.ServiceIdentityUnaryInterceptor(verifier)))
	authority := &routeAuthorityFixture{}
	authority.allowed.Store(true)
	channelv1.RegisterChannelServiceServer(server, authority)
	listener := bufconn.Listen(1 << 20)
	go func() { _ = server.Serve(listener) }()
	t.Cleanup(func() { server.Stop(); _ = listener.Close() })
	dial := func(token string) *grpc.ClientConn {
		conn, err := grpc.NewClient("passthrough:///route-authority", grpc.WithTransportCredentials(insecure.NewCredentials()),
			grpc.WithContextDialer(func(context.Context, string) (net.Conn, error) { return listener.Dial() }),
			grpc.WithPerRPCCredentials(grpcauth.NewInsecureTokenAuth(token)))
		require.NoError(t, err)
		t.Cleanup(func() { _ = conn.Close() })
		return conn
	}
	adapter := NewChannelAdapter(channelv1.NewChannelServiceClient(dial("relay-fixture")))
	source := routing.Source{Kind: routing.Channel, ID: 9}
	permission, err := adapter.CanRoute(context.Background(), "bound", "client-model", source)
	require.NoError(t, err)
	require.True(t, permission.Allowed)
	require.Equal(t, "upstream-model", permission.UpstreamModelID)
	authority.allowed.Store(false)
	permission, err = adapter.CanRoute(context.Background(), "bound", "client-model", source)
	require.NoError(t, err)
	require.False(t, permission.Allowed, "current source revocation still controls reuse")
	require.EqualValues(t, 2, authority.sourceChecks.Load())
	other := NewChannelAdapter(channelv1.NewChannelServiceClient(dial("monitor-fixture")))
	_, err = other.CanRoute(context.Background(), "bound", "client-model", source)
	require.Equal(t, codes.PermissionDenied, status.Code(err))
	require.EqualValues(t, 2, authority.sourceChecks.Load(), "unrelated caller never reaches the source check")
}

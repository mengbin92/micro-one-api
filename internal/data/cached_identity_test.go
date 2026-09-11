package data

import (
	"context"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc"
	identityv1 "micro-one-api/api/identity/v1"
	appcache "micro-one-api/platform/cache"
	"testing"
)

type freshRoutingIdentity struct {
	identityv1.IdentityServiceClient
	calls    int
	clientIP string
}

func (f *freshRoutingIdentity) GetAuthSnapshot(_ context.Context, r *identityv1.GetAuthSnapshotRequest, _ ...grpc.CallOption) (*identityv1.GetAuthSnapshotReply, error) {
	f.calls++
	f.clientIP = r.ClientIp
	return &identityv1.GetAuthSnapshotReply{UserId: 2, RoutingContextVersion: 2}, nil
}
func TestV2AlwaysReadsFreshIdentityFacts(t *testing.T) {
	t.Setenv("RELAY_ROUTING_CONTEXT_V2", "true")
	cache, err := appcache.NewAuthCache(nil, nil, func(context.Context, string) (*identityv1.GetAuthSnapshotReply, error) {
		t.Fatal("v2 must not trust a cached credential")
		return nil, nil
	})
	require.NoError(t, err)
	defer cache.Close()
	fresh := &freshRoutingIdentity{}
	client := NewCachedIdentityClient(fresh, cache)
	for range 2 {
		p, err := client.GetAuthSnapshot(context.Background(), &identityv1.GetAuthSnapshotRequest{Token: "key", ClientIp: "127.0.0.1"})
		require.NoError(t, err)
		require.EqualValues(t, 2, p.UserId)
	}
	require.Equal(t, 2, fresh.calls)
	require.Equal(t, "127.0.0.1", fresh.clientIP)
}

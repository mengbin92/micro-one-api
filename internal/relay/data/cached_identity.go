package data

import (
	"context"

	"google.golang.org/grpc"

	identityv1 "micro-one-api/api/identity/v1"
	appcache "micro-one-api/internal/pkg/cache"
)

// CachedIdentityClient wraps IdentityServiceClient.GetAuthSnapshot with the
// shared multi-level AuthCache while delegating all other identity RPCs.
type CachedIdentityClient struct {
	identityv1.IdentityServiceClient
	cache *appcache.AuthCache
}

func NewCachedIdentityClient(client identityv1.IdentityServiceClient, cache *appcache.AuthCache) identityv1.IdentityServiceClient {
	if cache == nil {
		return client
	}
	return &CachedIdentityClient{IdentityServiceClient: client, cache: cache}
}

func (c *CachedIdentityClient) GetAuthSnapshot(ctx context.Context, req *identityv1.GetAuthSnapshotRequest, opts ...grpc.CallOption) (*identityv1.GetAuthSnapshotReply, error) {
	if c.cache == nil {
		return c.IdentityServiceClient.GetAuthSnapshot(ctx, req, opts...)
	}
	return c.cache.Get(ctx, req.GetToken())
}

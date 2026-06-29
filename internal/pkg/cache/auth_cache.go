package cache

import (
	"context"
	"fmt"
	"time"

	"github.com/redis/go-redis/v9"
	"github.com/sony/gobreaker"

	identityv1 "micro-one-api/api/identity/v1"
	"micro-one-api/internal/pkg/events"
)

// AuthCache caches authentication snapshots (token → user info).
type AuthCache struct {
	cache *MultiLevelCache[identityv1.GetAuthSnapshotReply]
}

// NewAuthCache creates a new auth cache.
func NewAuthCache(
	redisClient *redis.Client,
	eventBus *events.EventBus,
	loader func(ctx context.Context, token string) (*identityv1.GetAuthSnapshotReply, error),
) (*AuthCache, error) {
	cfg := &Config{
		L1CacheSize: 10_000,
		L1TTL:       30 * time.Second, // Short TTL for auth data
		L2TTL:       5 * time.Minute,
		Prefix:      "auth",
	}

	cache, err := NewMultiLevelCache(
		redisClient,
		eventBus,
		loader,
		"auth",
		cfg,
	)
	if err != nil {
		return nil, err
	}

	return &AuthCache{cache: cache}, nil
}

// Get retrieves auth snapshot for a token.
func (c *AuthCache) Get(ctx context.Context, token string) (*identityv1.GetAuthSnapshotReply, error) {
	return c.cache.Get(ctx, token)
}

// Set stores auth snapshot for a token.
func (c *AuthCache) Set(ctx context.Context, token string, snapshot *identityv1.GetAuthSnapshotReply) error {
	return c.cache.Set(ctx, token, snapshot)
}

// Invalidate removes auth snapshot for a token.
func (c *AuthCache) Invalidate(ctx context.Context, token string) error {
	return c.cache.Invalidate(ctx, token)
}

// InvalidateByUser invalidates tokens for a specific user.
//
// Because the token→snapshot keys are hashed from the raw token, there is no
// cheap reverse index from userID to its token keys. Rather than silently
// no-op (which would leave stale snapshots in the cache), we clear the L1
// cache entirely for this prefix and rely on the short L1 TTL (30s) plus the
// L2 Redis TTL to bound staleness. For large fleets this is coarser than a
// per-user index, so callers needing precise invalidation should publish a
// token-scoped event via the event bus instead.
func (c *AuthCache) InvalidateByUser(ctx context.Context, userID int64) error {
	c.cache.ClearAll()
	return nil
}

// HasData checks if the cache has any data.
// Used for degradation decision.
func (c *AuthCache) HasData() bool {
	l1Size, _ := c.cache.Size()
	return l1Size > 0
}

// Close closes the cache.
func (c *AuthCache) Close() error {
	return c.cache.Close()
}

// AuthCacheLoader loads auth snapshots from the identity gRPC service.
// It satisfies cache.CacheLoader and is intended to be passed to NewAuthCache
// as the on-miss loader, so the cache actually fetches data instead of
// returning "not implemented" (REVIEW_v1 P0-2).
type AuthCacheLoader struct {
	client  identityv1.IdentityServiceClient
	breaker *gobreaker.CircuitBreaker
	timeout time.Duration
}

// NewAuthCacheLoader creates a new auth cache loader. client may be nil in
// which case Load returns an explicit error (rather than panicking), so the
// cache degrades to "unavailable" instead of crashing the relay path.
func NewAuthCacheLoader(
	client identityv1.IdentityServiceClient,
	breaker *gobreaker.CircuitBreaker,
	timeout time.Duration,
) *AuthCacheLoader {
	return &AuthCacheLoader{
		client:  client,
		breaker: breaker,
		timeout: timeout,
	}
}

// Load fetches an auth snapshot for a token from the identity service. When a
// circuit breaker is configured the call is executed through it so identity
// outages trip the breaker rather than blocking every request.
func (l *AuthCacheLoader) Load(ctx context.Context, token string) (*identityv1.GetAuthSnapshotReply, error) {
	if l == nil || l.client == nil {
		return nil, fmt.Errorf("auth cache loader: no identity client configured")
	}
	if token == "" {
		return nil, fmt.Errorf("auth cache loader: empty token")
	}

	call := func(ctx context.Context) (*identityv1.GetAuthSnapshotReply, error) {
		return l.client.GetAuthSnapshot(ctx, &identityv1.GetAuthSnapshotRequest{Token: token})
	}

	if l.timeout > 0 {
		origCall := call
		call = func(ctx context.Context) (*identityv1.GetAuthSnapshotReply, error) {
			ctx, cancel := context.WithTimeout(ctx, l.timeout)
			defer cancel()
			return origCall(ctx)
		}
	}

	if l.breaker != nil {
		result, err := l.breaker.Execute(func() (any, error) {
			return call(ctx)
		})
		if err != nil {
			return nil, err
		}
		resp, _ := result.(*identityv1.GetAuthSnapshotReply)
		if resp == nil {
			return nil, fmt.Errorf("auth cache loader: nil snapshot from identity")
		}
		return resp, nil
	}

	return call(ctx)
}

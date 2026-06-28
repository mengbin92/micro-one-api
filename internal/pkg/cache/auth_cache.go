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

// InvalidateByUser invalidates all tokens for a specific user.
// This requires either pattern-based invalidation or tracking user tokens.
func (c *AuthCache) InvalidateByUser(ctx context.Context, userID int64) error {
	// TODO: Implement user-based invalidation
	// Options:
	// 1. Track user→tokens mapping
	// 2. Use pattern: auth:*:user_id (need to change key structure)
	// 3. Publish event for each token
	return fmt.Errorf(" InvalidateByUser not implemented")
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

// AuthCacheLoader creates a cache loader for auth snapshots.
type AuthCacheLoader struct {
	client   any // identityServiceClient
	breaker  *gobreaker.CircuitBreaker
	timeout  time.Duration
}

// NewAuthCacheLoader creates a new auth cache loader.
func NewAuthCacheLoader(
	client any,
	breaker *gobreaker.CircuitBreaker,
	timeout time.Duration,
) *AuthCacheLoader {
	return &AuthCacheLoader{
		client:  client,
		breaker: breaker,
		timeout: timeout,
	}
}

// Load loads auth snapshot from the identity service.
func (l *AuthCacheLoader) Load(ctx context.Context, token string) (*identityv1.GetAuthSnapshotReply, error) {
	// TODO: Implement gRPC call to identity service
	// This would call identity.GetAuthSnapshot(ctx, &identityv1.GetAuthSnapshotRequest{
	// 	Token: token,
	// })
	return nil, fmt.Errorf("auth cache loader not implemented")
}

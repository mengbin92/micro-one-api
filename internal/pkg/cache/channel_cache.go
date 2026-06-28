package cache

import (
	"context"
	"fmt"
	"time"

	"github.com/redis/go-redis/v9"
	"github.com/sony/gobreaker"

	commonv1 "micro-one-api/api/common/v1"
	"micro-one-api/internal/pkg/events"
)

// ChannelCache caches channel selection results (group+model → channels).
type ChannelCache struct {
	cache *MultiLevelCache[[]*commonv1.ChannelInfo]
}

// NewChannelCache creates a new channel cache.
func NewChannelCache(
	redisClient *redis.Client,
	eventBus *events.EventBus,
	loader func(ctx context.Context, key string) ([]*commonv1.ChannelInfo, error),
) (*ChannelCache, error) {
	cfg := &Config{
		L1CacheSize: 5_000,
		L1TTL:       60 * time.Second, // Longer TTL for channel data
		L2TTL:       10 * time.Minute,
		Prefix:      "channel",
	}

	// Wrap the loader to return pointer to slice
	wrappedLoader := func(ctx context.Context, key string) (*[]*commonv1.ChannelInfo, error) {
		data, err := loader(ctx, key)
		if err != nil {
			return nil, err
		}
		return &data, nil
	}

	cache, err := NewMultiLevelCache(
		redisClient,
		eventBus,
		wrappedLoader,
		"channel",
		cfg,
	)
	if err != nil {
		return nil, err
	}

	return &ChannelCache{cache: cache}, nil
}

// Get retrieves channels for a group+model combination.
func (c *ChannelCache) Get(ctx context.Context, group, model string) ([]*commonv1.ChannelInfo, error) {
	key := fmt.Sprintf("%s:%s", group, model)
	data, err := c.cache.Get(ctx, key)
	if err != nil {
		return nil, err
	}
	if data == nil {
		return nil, fmt.Errorf("cache returned nil data")
	}
	return *data, nil
}

// Set stores channels for a group+model combination.
func (c *ChannelCache) Set(ctx context.Context, group, model string, channels []*commonv1.ChannelInfo) error {
	key := fmt.Sprintf("%s:%s", group, model)
	return c.cache.Set(ctx, key, &channels)
}

// Invalidate removes channels for a group+model combination.
func (c *ChannelCache) Invalidate(ctx context.Context, group, model string) error {
	key := fmt.Sprintf("%s:%s", group, model)
	return c.cache.Invalidate(ctx, key)
}

// InvalidateByGroup invalidates all channels for a specific group.
func (c *ChannelCache) InvalidateByGroup(ctx context.Context, group string) error {
	pattern := fmt.Sprintf("%s:*", group)
	return c.cache.InvalidateByPattern(ctx, pattern)
}

// InvalidateByChannel invalidates all cache entries containing a specific channel.
// This requires scanning all cache entries.
func (c *ChannelCache) InvalidateByChannel(ctx context.Context, channelID int64) error {
	// TODO: Implement channel-based invalidation
	// Options:
	// 1. Track channel→groups mapping
	// 2. Scan all entries and check each one
	// 3. Use separate keys for channel→groups lookup
	return fmt.Errorf("InvalidateByChannel not implemented")
}

// HasData checks if the cache has any data.
// Used for degradation decision.
func (c *ChannelCache) HasData() bool {
	l1Size, _ := c.cache.Size()
	return l1Size > 0
}

// Close closes the cache.
func (c *ChannelCache) Close() error {
	return c.cache.Close()
}

// ChannelCacheLoader creates a cache loader for channel data.
type ChannelCacheLoader struct {
	client  any // channelServiceClient
	breaker *gobreaker.CircuitBreaker
	timeout time.Duration
}

// NewChannelCacheLoader creates a new channel cache loader.
func NewChannelCacheLoader(
	client any,
	breaker *gobreaker.CircuitBreaker,
	timeout time.Duration,
) *ChannelCacheLoader {
	return &ChannelCacheLoader{
		client:  client,
		breaker: breaker,
		timeout: timeout,
	}
}

// Load loads channel data from the channel service.
func (l *ChannelCacheLoader) Load(ctx context.Context, key string) ([]*commonv1.ChannelInfo, error) {
	// TODO: Implement gRPC call to channel service
	// This would parse group:model from key and call channel.SelectChannel
	return nil, fmt.Errorf("channel cache loader not implemented")
}

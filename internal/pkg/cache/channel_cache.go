package cache

import (
	"context"
	"fmt"
	"time"

	"github.com/redis/go-redis/v9"
	"github.com/sony/gobreaker"

	channelv1 "micro-one-api/api/channel/v1"
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

// InvalidateByChannel invalidates cache entries that might reference a
// specific channel.
//
// The channel cache is keyed by "group:model" and a single key may resolve to
// a list of candidate channels, so there is no reverse index from channelID
// to the keys that contain it. We therefore clear the L1 cache entirely
// (bounded by the short L1 TTL) and invalidate the whole channel prefix in L2
// via a SCAN. This is safe — channel config changes are infrequent — and
// avoids the silent no-op the previous TODO represented.
func (c *ChannelCache) InvalidateByChannel(ctx context.Context, channelID int64) error {
	// Clear L1 entirely.
	c.cache.ClearAll()
	// Invalidate the entire channel L2 namespace. The pattern "*" matches
	// every key under the channel prefix.
	return c.cache.InvalidateByPattern(ctx, "*")
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

// ChannelCacheLoader loads channel info from the channel gRPC service.
// It is intended as the on-miss loader for ChannelCache so the cache actually
// reaches the channel service (REVIEW_v1 P0-2).
type ChannelCacheLoader struct {
	client  channelv1.ChannelServiceClient
	breaker *gobreaker.CircuitBreaker
	timeout time.Duration
}

// NewChannelCacheLoader creates a new channel cache loader. A nil client
// makes Load return an explicit error rather than panic.
func NewChannelCacheLoader(
	client channelv1.ChannelServiceClient,
	breaker *gobreaker.CircuitBreaker,
	timeout time.Duration,
) *ChannelCacheLoader {
	return &ChannelCacheLoader{
		client:  client,
		breaker: breaker,
		timeout: timeout,
	}
}

// Load fetches channel info for a "group:model" key from the channel service.
// The key format matches what ChannelCache.Get/Invalidate use.
func (l *ChannelCacheLoader) Load(ctx context.Context, key string) ([]*commonv1.ChannelInfo, error) {
	if l == nil || l.client == nil {
		return nil, fmt.Errorf("channel cache loader: no channel client configured")
	}
	group, model, ok := splitChannelKey(key)
	if !ok {
		return nil, fmt.Errorf("channel cache loader: invalid key %q", key)
	}

	call := func(ctx context.Context) (*channelv1.SelectChannelReply, error) {
		return l.client.SelectChannel(ctx, &channelv1.SelectChannelRequest{
			Group: group,
			Model: model,
		})
	}

	if l.timeout > 0 {
		origCall := call
		call = func(ctx context.Context) (*channelv1.SelectChannelReply, error) {
			ctx, cancel := context.WithTimeout(ctx, l.timeout)
			defer cancel()
			return origCall(ctx)
		}
	}

	var (
		reply *channelv1.SelectChannelReply
		err   error
	)
	if l.breaker != nil {
		var result any
		result, err = l.breaker.Execute(func() (any, error) { return call(ctx) })
		if err != nil {
			return nil, err
		}
		reply, _ = result.(*channelv1.SelectChannelReply)
	} else {
		reply, err = call(ctx)
	}
	if err != nil {
		return nil, err
	}
	if reply == nil || reply.GetChannel() == nil {
		return nil, fmt.Errorf("channel cache loader: no channel for %s/%s", group, model)
	}
	return []*commonv1.ChannelInfo{reply.GetChannel()}, nil
}

// splitChannelKey splits a "group:model" cache key. The model portion may
// contain colons, so only the first colon is a separator.
func splitChannelKey(key string) (group, model string, ok bool) {
	for i := 0; i < len(key); i++ {
		if key[i] == ':' {
			return key[:i], key[i+1:], true
		}
	}
	return "", "", false
}

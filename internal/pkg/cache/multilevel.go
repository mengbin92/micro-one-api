package cache

import (
	"context"
	"fmt"
	"time"

	"github.com/dgraph-io/ristretto"
	"github.com/redis/go-redis/v9"
	"golang.org/x/sync/singleflight"

	"micro-one-api/internal/pkg/events"
	"micro-one-api/internal/pkg/metrics"
	"micro-one-api/pkg/jsonx"
)

// CacheLoader defines the function to load data on cache miss.
type CacheLoader[T any] func(ctx context.Context, key string) (*T, error)

// MultiLevelCache provides L1 (local) + L2 (Redis) caching
// with event-driven invalidation.
type MultiLevelCache[T any] struct {
	l1       *ristretto.Cache
	l2       *redis.Client
	prefix   string
	ttl      time.Duration
	l2TTL    time.Duration
	eventBus *events.EventBus
	loader   CacheLoader[T]
	sf       singleflight.Group
	metrics  *cacheMetrics
}

// entry represents a cached item with expiration.
type entry[T any] struct {
	data      *T
	expiresAt time.Time
}

// expired checks if the entry has expired.
func (e *entry[T]) expired() bool {
	return !e.expiresAt.IsZero() && time.Now().After(e.expiresAt)
}

// Config holds configuration for multi-level cache.
type Config struct {
	// L1CacheSize is the maximum number of items in L1 cache.
	L1CacheSize int64
	// L1TTL is the TTL for L1 cache entries.
	L1TTL time.Duration
	// L2TTL is the TTL for L2 cache entries.
	L2TTL time.Duration
	// Prefix is the key prefix for this cache.
	Prefix string
}

// DefaultConfig returns default cache configuration.
func DefaultConfig() *Config {
	return &Config{
		L1CacheSize: 10_000,
		L1TTL:       30 * time.Second,
		L2TTL:       5 * time.Minute,
		Prefix:      "cache",
	}
}

// NewMultiLevelCache creates a new multi-level cache.
func NewMultiLevelCache[T any](
	l2Client *redis.Client,
	eventBus *events.EventBus,
	loader CacheLoader[T],
	cacheName string,
	cfg *Config,
) (*MultiLevelCache[T], error) {
	if cfg == nil {
		cfg = DefaultConfig()
	}

	// Initialize L1 cache
	l1, err := ristretto.NewCache(&ristretto.Config{
		NumCounters: int64(float64(cfg.L1CacheSize) * 10),
		MaxCost:     cfg.L1CacheSize,
		BufferItems: 64,
	})
	if err != nil {
		return nil, fmt.Errorf("failed to create L1 cache: %w", err)
	}

	return &MultiLevelCache[T]{
		l1:       l1,
		l2:       l2Client,
		prefix:   fmt.Sprintf("%s:%s:", cfg.Prefix, cacheName),
		ttl:      cfg.L1TTL,
		l2TTL:    cfg.L2TTL,
		eventBus: eventBus,
		loader:   loader,
		metrics:  &cacheMetrics{cacheName: cacheName},
	}, nil
}

// Get retrieves from L1 → L2 → source, populating upstream caches.
func (c *MultiLevelCache[T]) Get(ctx context.Context, key string) (*T, error) {
	start := time.Now()
	cacheKey := c.prefix + key

	// L1 check
	if val, ok := c.l1.Get(cacheKey); ok {
		entry, ok := val.(*entry[T])
		if ok && !entry.expired() {
			c.metrics.recordL1Hit()
			metrics.CacheHits.WithLabelValues(c.metrics.cacheName, "l1").Inc()
			metrics.CacheLatency.WithLabelValues(c.metrics.cacheName, "get", "l1").Observe(time.Since(start).Seconds())
			return entry.data, nil
		}
		// Entry expired or invalid type, remove it
		c.l1.Del(cacheKey)
	}

	// L2 check
	if c.l2 != nil {
		data, err := c.l2.Get(ctx, cacheKey).Bytes()
		if err == nil && len(data) > 0 {
			c.metrics.recordL2Hit()
			metrics.CacheHits.WithLabelValues(c.metrics.cacheName, "l2").Inc()

			// Deserialize
			var val T
			if err := c.unmarshal(data, &val); err == nil {
				// Populate L1
				c.populateL1(cacheKey, &val)
				metrics.CacheLatency.WithLabelValues(c.metrics.cacheName, "get", "l2").Observe(time.Since(start).Seconds())
				return &val, nil
			}
		}
	}

	// Cache miss - use singleflight to prevent thundering herd
	c.metrics.recordMiss()
	metrics.CacheMisses.WithLabelValues(c.metrics.cacheName).Inc()

	result, err, _ := c.sf.Do(key, func() (any, error) {
		// Load from source
		val, err := c.loader(ctx, key)
		if err != nil {
			return nil, err
		}

		// Populate L1 and L2
		c.populate(ctx, cacheKey, val)

		metrics.CacheLatency.WithLabelValues(c.metrics.cacheName, "get", "source").Observe(time.Since(start).Seconds())
		return val, nil
	})

	if err != nil {
		return nil, err
	}

	return result.(*T), nil
}

// Set stores a value in both L1 and L2.
func (c *MultiLevelCache[T]) Set(ctx context.Context, key string, value *T) error {
	cacheKey := c.prefix + key
	return c.populate(ctx, cacheKey, value)
}

// Invalidate removes a key from both L1 and L2.
// Triggered by event-driven invalidation or explicit API.
func (c *MultiLevelCache[T]) Invalidate(ctx context.Context, key string) error {
	cacheKey := c.prefix + key

	// Remove from L1
	c.l1.Del(cacheKey)

	// Remove from L2
	if c.l2 != nil {
		if err := c.l2.Del(ctx, cacheKey).Err(); err != nil {
			return fmt.Errorf("failed to delete from L2: %w", err)
		}
	}

	metrics.CacheEvictions.WithLabelValues(c.metrics.cacheName, "l1").Inc()
	if c.l2 != nil {
		metrics.CacheEvictions.WithLabelValues(c.metrics.cacheName, "l2").Inc()
	}

	return nil
}

// populate stores a value in both L1 and L2.
func (c *MultiLevelCache[T]) populate(ctx context.Context, key string, value *T) error {
	// Populate L1
	c.populateL1(key, value)

	// Populate L2
	if c.l2 != nil {
		data, err := c.marshal(value)
		if err != nil {
			return fmt.Errorf("failed to marshal value: %w", err)
		}

		if err := c.l2.Set(ctx, key, data, redis.KeepTTL).Err(); err != nil {
			return fmt.Errorf("failed to set in L2: %w", err)
		}

		// Set expiration
		if c.l2TTL > 0 {
			c.l2.Expire(ctx, key, c.l2TTL)
		}
	}

	return nil
}

// populateL1 stores a value in L1 cache. It blocks until ristretto has
// ingested the Set so a subsequent Get observes the value (ristretto's Set is
// processed by a background goroutine; without Wait the entry may not be
// visible immediately).
func (c *MultiLevelCache[T]) populateL1(key string, value *T) {
	expiresAt := time.Time{}
	if c.ttl > 0 {
		expiresAt = time.Now().Add(c.ttl)
	}
	c.l1.Set(key, &entry[T]{data: value, expiresAt: expiresAt}, 1)
	c.l1.Wait()
}

// marshal serializes a value for storage.
func (c *MultiLevelCache[T]) marshal(value *T) ([]byte, error) {
	return jsonx.Marshal(value)
}

// unmarshal deserializes a value from storage.
func (c *MultiLevelCache[T]) unmarshal(data []byte, value *T) error {
	return jsonx.Unmarshal(data, value)
}

// cacheMetrics holds metrics for a cache instance.
type cacheMetrics struct {
	cacheName string
	l1Hits    int64
	l2Hits    int64
	misses    int64
}

func (m *cacheMetrics) recordL1Hit() {
	m.l1Hits++
}

func (m *cacheMetrics) recordL2Hit() {
	m.l2Hits++
}

func (m *cacheMetrics) recordMiss() {
	m.misses++
}

// HitRate returns the overall cache hit rate.
func (m *cacheMetrics) HitRate() float64 {
	total := m.l1Hits + m.l2Hits + m.misses
	if total == 0 {
		return 0
	}
	return float64(m.l1Hits+m.l2Hits) / float64(total)
}

// L1HitRate returns the L1 cache hit rate.
func (m *cacheMetrics) L1HitRate() float64 {
	total := m.l1Hits + m.l2Hits + m.misses
	if total == 0 {
		return 0
	}
	return float64(m.l1Hits) / float64(total)
}

// InvalidateByPattern invalidates all L2 keys whose Redis key matches the
// given glob pattern (prefixed with the cache's prefix).
//
// L1 (ristretto) does not expose pattern-based deletion, so matching L1
// entries are NOT removed here; callers that need a full L1 wipe should use
// ClearAll. This is acceptable because L1 entries have a short TTL and are
// bounded, so a stale L1 hit is time-limited.
func (c *MultiLevelCache[T]) InvalidateByPattern(ctx context.Context, pattern string) error {
	if c.l2 != nil {
		iter := c.l2.Scan(ctx, 0, c.prefix+pattern, 0).Iterator()
		for iter.Next(ctx) {
			if err := c.l2.Del(ctx, iter.Val()).Err(); err != nil {
				return fmt.Errorf("failed to delete key: %w", err)
			}
		}
		if err := iter.Err(); err != nil {
			return fmt.Errorf("scan error: %w", err)
		}
	}
	return nil
}

// ClearAll removes every entry from L1. L2 is left untouched (use
// InvalidateByPattern for targeted L2 eviction). This is the brute-force
// fallback when a reverse index (user→tokens, channel→groups) is not
// maintained: callers that need precise invalidation should publish an event
// instead.
func (c *MultiLevelCache[T]) ClearAll() {
	if c.l1 != nil {
		c.l1.Clear()
	}
}

// Size returns the approximate number of items in the cache.
func (c *MultiLevelCache[T]) Size() (l1, l2 int64) {
	if c.l1 != nil && c.l1.Metrics != nil {
		// Use the metrics to get approximate size
		keysAdded := int64(c.l1.Metrics.KeysAdded())
		keysEvicted := int64(c.l1.Metrics.KeysEvicted())
		l1 = max(0, keysAdded-keysEvicted)
	}
	// L2 size is expensive to compute, skip for now
	return l1, 0
}

// Close closes the cache and releases resources.
func (c *MultiLevelCache[T]) Close() error {
	if c.l1 != nil {
		c.l1.Close()
	}
	return nil
}

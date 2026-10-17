package xdb

import (
	"context"
	"time"

	"github.com/redis/go-redis/v9"
)

// RedisPoolConfig holds Redis connection pool configuration
type RedisPoolConfig struct {
	PoolSize        int
	MinIdleConns    int
	DialTimeout     time.Duration
	ReadTimeout     time.Duration
	WriteTimeout    time.Duration
	ConnMaxIdleTime time.Duration
}

// DefaultRedisPoolConfig returns sensible defaults for Redis pooling
func DefaultRedisPoolConfig() *RedisPoolConfig {
	return &RedisPoolConfig{
		PoolSize:        100,
		MinIdleConns:    10,
		DialTimeout:     5 * time.Second,
		ReadTimeout:     3 * time.Second,
		WriteTimeout:    3 * time.Second,
		ConnMaxIdleTime: 5 * time.Minute,
	}
}

// NewRedisClient creates a new Redis client with default pool settings. Returns nil if addr is empty.
func NewRedisClient(addr, password string) *redis.Client {
	return NewRedisClientWithPool(addr, password, DefaultRedisPoolConfig())
}

// NewRedisClientWithPool creates a new Redis client with custom pool settings. Returns nil if addr is empty.
func NewRedisClientWithPool(addr, password string, pool *RedisPoolConfig) *redis.Client {
	if addr == "" {
		return nil
	}
	if pool == nil {
		pool = DefaultRedisPoolConfig()
	}
	client := redis.NewClient(&redis.Options{
		Addr:            addr,
		Password:        password,
		DialTimeout:     pool.DialTimeout,
		ReadTimeout:     pool.ReadTimeout,
		WriteTimeout:    pool.WriteTimeout,
		PoolSize:        pool.PoolSize,
		MinIdleConns:    pool.MinIdleConns,
		ConnMaxIdleTime: pool.ConnMaxIdleTime,
	})
	client.AddHook(redisTraceHook{})
	return client
}

// PingRedis checks if Redis is reachable. Returns error if not.
func PingRedis(ctx context.Context, client *redis.Client) error {
	if client == nil {
		return nil
	}
	return client.Ping(ctx).Err()
}

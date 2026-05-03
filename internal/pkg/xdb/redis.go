package xdb

import (
	"context"
	"time"

	"github.com/redis/go-redis/v9"
)

// NewRedisClient creates a new Redis client. Returns nil if addr is empty.
func NewRedisClient(addr string) *redis.Client {
	if addr == "" {
		return nil
	}
	return redis.NewClient(&redis.Options{
		Addr:         addr,
		DialTimeout:  5 * time.Second,
		ReadTimeout:  3 * time.Second,
		WriteTimeout: 3 * time.Second,
		PoolSize:     10,
	})
}

// PingRedis checks if Redis is reachable. Returns error if not.
func PingRedis(ctx context.Context, client *redis.Client) error {
	if client == nil {
		return nil
	}
	return client.Ping(ctx).Err()
}

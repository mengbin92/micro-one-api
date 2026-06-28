package events

import (
	"os"
	"strings"

	"github.com/redis/go-redis/v9"
)

// NewConfiguredEventBus returns a Redis Streams bus when a Redis client is
// available and EVENT_BUS_BACKEND=redis-streams is set. Otherwise it preserves
// the existing in-process MemoryEventBus behavior.
func NewConfiguredEventBus(redisClient *redis.Client, serviceName string) EventBus {
	if redisClient != nil && strings.EqualFold(strings.TrimSpace(os.Getenv("EVENT_BUS_BACKEND")), "redis-streams") {
		consumerID := strings.TrimSpace(os.Getenv("EVENT_BUS_CONSUMER_ID"))
		if consumerID == "" {
			consumerID = serviceName
		}
		return NewStreamEventBus(redisClient, consumerID)
	}
	return NewMemoryEventBus()
}

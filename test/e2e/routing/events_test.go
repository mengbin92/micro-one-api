package routingtest

import (
	"context"
	"os"
	"strings"
	"time"

	"github.com/redis/go-redis/v9"
	"github.com/stretchr/testify/require"
	"micro-one-api/platform/events"
	"micro-one-api/platform/routingoutbox"
)

func (s *suite) replayInvalidations() {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	client := redis.NewClient(&redis.Options{Addr: os.Getenv("REDIS_ADDR"), Password: os.Getenv("REDIS_PASSWORD")})
	defer client.Close()
	bus := events.NewStreamEventBus(client, "routing-e2e-replay")
	defer bus.Close()
	revision := s.scalar("SELECT routing_access_revision FROM users WHERE id = ?", s.state.UserID)
	for _, v := range []int64{revision, revision - 1, revision} {
		require.NoError(s.t, bus.Publish(ctx, routingoutbox.Topic, routingoutbox.Change{Owner: "identity", Kind: "user", AggregateID: s.state.UserID, Revision: v}))
	}
	// Both live relay consumer groups must acknowledge the duplicate/older events
	// before the following assertions verify that neither restores revoked access.
	require.Eventually(s.t, func() bool {
		groups, err := client.XInfoGroups(ctx, routingoutbox.Topic).Result()
		if err != nil {
			return false
		}
		ready := 0
		for _, g := range groups {
			if strings.Contains(g.Name, ":relay-routing-") && g.Pending == 0 && g.Lag == 0 {
				ready++
			}
		}
		return ready == 2
	}, 8*time.Second, 100*time.Millisecond, "both relay replicas acknowledge replayed invalidations")
}

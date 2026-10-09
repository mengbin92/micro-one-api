package events

import (
	"context"
	"testing"

	"github.com/alicebob/miniredis/v2"
	"github.com/redis/go-redis/v9"
	"github.com/stretchr/testify/require"
)

func TestHandlerPanicLeavesMessagePending(t *testing.T) {
	srv := miniredis.RunT(t)
	client := redis.NewClient(&redis.Options{Addr: srv.Addr()})
	t.Cleanup(func() { _ = client.Close() })
	b := NewStreamEventBus(client, "panic-test")
	t.Cleanup(func() { _ = b.Close() })
	const topic = "panic-topic"
	b.handlers[topic] = []Handler{func(context.Context, Event) error { panic("poison") }}
	require.NoError(t, b.ensureGroup(context.Background(), topic))
	require.NoError(t, b.Publish(context.Background(), topic, "payload"))
	streams, err := client.XReadGroup(context.Background(), &redis.XReadGroupArgs{Group: b.consumerGroup, Consumer: b.consumerID, Streams: []string{topic, ">"}, Count: 1}).Result()
	require.NoError(t, err)
	require.NotPanics(t, func() { b.processMessage(topic, &streams[0].Messages[0]) })
	pending, err := client.XPending(context.Background(), topic, b.consumerGroup).Result()
	require.NoError(t, err)
	require.EqualValues(t, 1, pending.Count)
}

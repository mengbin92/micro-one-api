package events

import (
	"context"
	"errors"
	"github.com/alicebob/miniredis/v2"
	"github.com/redis/go-redis/v9"
	"github.com/stretchr/testify/require"
	"testing"
)

func TestTrimPreservesPendingAndUnread(t *testing.T) {
	srv := miniredis.RunT(t)
	rdb := redis.NewClient(&redis.Options{Addr: srv.Addr()})
	defer rdb.Close()
	b := NewStreamEventBus(rdb, "slow")
	defer b.Close()
	b.maxlen = 1
	ctx := context.Background()
	topic := "retention"
	require.NoError(t, b.ensureGroup(ctx, topic))
	require.NoError(t, b.Publish(ctx, topic, 1))
	streams, err := rdb.XReadGroup(ctx, &redis.XReadGroupArgs{Group: b.consumerGroup, Consumer: b.consumerID, Streams: []string{topic, ">"}, Count: 1}).Result()
	require.NoError(t, err)
	for i := 0; i < 5; i++ {
		require.NoError(t, b.Publish(ctx, topic, i))
	}
	require.EqualValues(t, 6, rdb.XLen(ctx, topic).Val())
	require.EqualValues(t, 1, rdb.XPending(ctx, topic, b.consumerGroup).Val().Count)
	require.NoError(t, rdb.XAck(ctx, topic, b.consumerGroup, streams[0].Messages[0].ID).Err())
	unread, err := rdb.XReadGroup(ctx, &redis.XReadGroupArgs{Group: b.consumerGroup, Consumer: b.consumerID, Streams: []string{topic, ">"}, Count: 5}).Result()
	require.NoError(t, err)
	for _, msg := range unread[0].Messages {
		require.NoError(t, rdb.XAck(ctx, topic, b.consumerGroup, msg.ID).Err())
	}
	require.NoError(t, b.Publish(ctx, topic, 7))
	require.EqualValues(t, 2, rdb.XLen(ctx, topic).Val())
}
func TestPoisonMessageGoesToDeadLetter(t *testing.T) {
	srv := miniredis.RunT(t)
	rdb := redis.NewClient(&redis.Options{Addr: srv.Addr()})
	defer rdb.Close()
	ctx := context.Background()
	b := NewStreamEventBus(rdb, "poison")
	defer b.Close()
	topic := "poison"
	calls := 0
	b.handlers[topic] = []Handler{func(context.Context, Event) error { calls++; return errors.New("poison") }}
	require.NoError(t, b.ensureGroup(ctx, topic))
	require.NoError(t, b.Publish(ctx, topic, 1))
	for i := 0; i < 11; i++ {
		start := ">"
		if i > 0 {
			start = "0"
		}
		streams, err := rdb.XReadGroup(ctx, &redis.XReadGroupArgs{Group: b.consumerGroup, Consumer: b.consumerID, Streams: []string{topic, start}, Count: 1}).Result()
		require.NoError(t, err)
		b.processMessage(topic, &streams[0].Messages[0])
	}
	require.Equal(t, 10, calls)
	require.EqualValues(t, 0, rdb.XPending(ctx, topic, b.consumerGroup).Val().Count)
	require.EqualValues(t, 1, rdb.XLen(ctx, topic+".dlq").Val())
}

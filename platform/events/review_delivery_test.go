package events

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"
	"github.com/redis/go-redis/v9"
	"github.com/stretchr/testify/require"
)

func TestStreamEventBusRecreatesMissingGroup(t *testing.T) {
	srv := miniredis.RunT(t)
	rdb := redis.NewClient(&redis.Options{Addr: srv.Addr()})
	defer rdb.Close()
	bus := NewStreamEventBus(rdb, "group-recovery")
	defer bus.Close()
	bus.readTimeout = 20 * time.Millisecond
	topic := "group-recovery"
	received := make(chan struct{}, 1)
	bus.Subscribe(topic, func(context.Context, Event) error {
		select {
		case received <- struct{}{}:
		default:
		}
		return nil
	})
	ctx := context.Background()
	require.NoError(t, rdb.XGroupDestroy(ctx, topic, bus.consumerGroup).Err())
	require.NoError(t, bus.Publish(ctx, topic, true))
	select {
	case <-received:
	case <-time.After(2 * time.Second):
		t.Fatal("delivery did not resume after the Redis group disappeared")
	}
}

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

func TestTrimPrunesOnlyExpiredEphemeralGroups(t *testing.T) {
	srv := miniredis.RunT(t)
	rdb := redis.NewClient(&redis.Options{Addr: srv.Addr()})
	defer rdb.Close()
	ctx := context.Background()
	topic := "ephemeral-retention"
	start := time.Unix(1_800_000_000, 0)
	srv.SetTime(start)
	producer := NewStreamEventBus(rdb, "producer")
	defer producer.Close()
	producer.maxlen = 2
	durable := NewStreamEventBus(rdb, "durable")
	defer durable.Close()
	abandoned := NewEphemeralStreamEventBus(rdb, "abandoned")
	defer abandoned.Close()
	require.NoError(t, durable.ensureGroup(ctx, topic))
	require.NoError(t, abandoned.ensureGroup(ctx, topic))
	for range 3 {
		require.NoError(t, producer.Publish(ctx, topic, true))
	}
	require.EqualValues(t, 3, rdb.XLen(ctx, topic).Val())

	srv.SetTime(start.Add(2 * time.Minute))
	require.NoError(t, producer.Publish(ctx, topic, true))
	groups, err := rdb.XInfoGroups(ctx, topic).Result()
	require.NoError(t, err)
	require.Len(t, groups, 1)
	require.Equal(t, durable.consumerGroup, groups[0].Name)
	require.EqualValues(t, 4, rdb.XLen(ctx, topic).Val(), "unread durable events must survive")
}

func TestEphemeralGroupsRenewAndCleanUp(t *testing.T) {
	srv := miniredis.RunT(t)
	rdb := redis.NewClient(&redis.Options{Addr: srv.Addr()})
	defer rdb.Close()
	ctx := context.Background()
	topic := "ephemeral-shutdown"
	start := time.Unix(1_800_000_000, 0)
	srv.SetTime(start)
	producer := NewStreamEventBus(rdb, "producer")
	defer producer.Close()
	producer.maxlen = 2
	bus := NewEphemeralStreamEventBus(rdb, "relay")
	defer bus.Close()
	bus.readTimeout = 20 * time.Millisecond
	bus.Subscribe(topic, func(context.Context, Event) error { return errors.New("fixture leaves events pending") })
	for range 3 {
		require.NoError(t, producer.Publish(ctx, topic, true))
	}
	srv.SetTime(start.Add(45 * time.Second))
	require.NoError(t, bus.ensureGroup(ctx, topic))
	srv.SetTime(start.Add(75 * time.Second))
	require.NoError(t, producer.Publish(ctx, topic, true))
	groups, err := rdb.XInfoGroups(ctx, topic).Result()
	require.NoError(t, err)
	require.Len(t, groups, 1)
	require.Equal(t, bus.consumerGroup, groups[0].Name)
	require.NoError(t, bus.Close())
	groups, err = rdb.XInfoGroups(ctx, topic).Result()
	require.NoError(t, err)
	require.Empty(t, groups)
	require.NoError(t, producer.Publish(ctx, topic, true))
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

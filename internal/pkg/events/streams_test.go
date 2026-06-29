package events

import (
	"context"
	"net"
	"os"
	"sync/atomic"
	"testing"
	"time"

	"github.com/redis/go-redis/v9"
)

// These tests exercise StreamEventBus against a real Redis instance when the
// REDIS_TEST_ADDR environment variable is set. When it is unset (e.g. the
// sandboxed CI environment) the network-dependent cases are skipped, but the
// pure-logic Stats locking path is still validated against a stub client.
func skipIfNoRedis(t *testing.T) (*redis.Client, func()) {
	t.Helper()
	addr := os.Getenv("REDIS_TEST_ADDR")
	if addr == "" {
		t.Skip("REDIS_TEST_ADDR not set; skipping live-Redis test")
	}
	rdb := redis.NewClient(&redis.Options{Addr: addr})
	return rdb, func() { _ = rdb.Close() }
}

func TestStreamEventBus_PublishAndConsume(t *testing.T) {
	rdb, cleanup := skipIfNoRedis(t)
	defer cleanup()

	b := NewStreamEventBus(rdb, "test-consumer-"+t.Name())
	defer b.Close()

	var got atomic.Int32
	done := make(chan struct{})
	b.Subscribe("test.topic", func(ctx context.Context, event Event) error {
		got.Add(1)
		select {
		case <-done:
		default:
			close(done)
		}
		return nil
	})

	time.Sleep(200 * time.Millisecond)

	if err := b.Publish(context.Background(), "test.topic", "hello"); err != nil {
		t.Fatalf("Publish: %v", err)
	}

	select {
	case <-done:
	case <-time.After(3 * time.Second):
		t.Fatal("handler not invoked within 3s")
	}
	if got.Load() != 1 {
		t.Fatalf("got %d invocations, want 1", got.Load())
	}
}

// stubStreamBus is a StreamEventBus whose redis client never touches the
// network: every call returns a synthetic error immediately. It lets us
// verify Stats releases the handlers lock before issuing any Redis IO without
// needing a live Redis server.
func newStubStreamBus() *StreamEventBus {
	rdb := redis.NewClient(&redis.Options{
		Addr: "stub:0",
		Dialer: func(ctx context.Context, network, addr string) (net.Conn, error) {
			return nil, fastDialErr{}
		},
	})
	b := NewStreamEventBus(rdb, "stub")
	return b
}

type fastDialErr struct{}

func (fastDialErr) Error() string                      { return "stub dial" }
func (fastDialErr) Read(p []byte) (int, error)         { return 0, fastDialErr{} }
func (fastDialErr) Write(p []byte) (int, error)        { return 0, fastDialErr{} }
func (fastDialErr) Close() error                       { return nil }
func (fastDialErr) LocalAddr() net.Addr                { return stubAddr{} }
func (fastDialErr) RemoteAddr() net.Addr               { return stubAddr{} }
func (fastDialErr) SetDeadline(t time.Time) error      { return nil }
func (fastDialErr) SetReadDeadline(t time.Time) error  { return nil }
func (fastDialErr) SetWriteDeadline(t time.Time) error { return nil }

type stubAddr struct{}

func (stubAddr) Network() string { return "stub" }
func (stubAddr) String() string  { return "stub" }

// TestStreamEventBus_StatsReleasesLockBeforeRedisIO verifies the fix for
// REVIEW_v1 P1-3: Stats must snapshot topics under the handlers lock and
// release it before issuing any Redis IO. If the lock is held during the
// (stubbed, immediately-failing) Redis call, the concurrent hasHandler probe
// (which only briefly takes handlersMu) will block and time out.
func TestStreamEventBus_StatsReleasesLockBeforeRedisIO(t *testing.T) {
	b := newStubStreamBus()
	defer b.Close()

	b.handlersMu.Lock()
	b.handlers["slow.topic"] = []Handler{func(context.Context, Event) error { return nil }}
	b.handlersMu.Unlock()

	// hasHandler only takes handlersMu briefly; if Stats holds it during Redis
	// IO, this probe blocks until the Redis client gives up (~5s of retries).
	hasHandler := func(topic string) bool {
		b.handlersMu.RLock()
		_, ok := b.handlers[topic]
		b.handlersMu.RUnlock()
		return ok
	}

	probeBlocked := make(chan struct{})
	go func() {
		_ = hasHandler("slow.topic")
		close(probeBlocked)
	}()

	// Now call Stats with a short timeout. It will hit the stub dialer which
	// fails immediately, so XInfoGroups returns an error fast.
	ctx, cancel := context.WithTimeout(context.Background(), 1*time.Second)
	defer cancel()
	if _, err := b.Stats(ctx); err != nil {
		t.Fatalf("Stats returned error: %v", err)
	}

	select {
	case <-probeBlocked:
		// good: the probe acquired the lock, so Stats was not holding it
	default:
		t.Fatal("handlers lock was held during Redis IO; Subscribe-like probes blocked")
	}
}

func TestStreamEventBus_CloseIsIdempotent(t *testing.T) {
	rdb, cleanup := skipIfNoRedis(t)
	defer cleanup()

	b := NewStreamEventBus(rdb, "test-consumer-"+t.Name())
	if err := b.Close(); err != nil {
		t.Fatalf("first Close: %v", err)
	}
	if err := b.Close(); err != nil {
		t.Fatalf("second Close: %v", err)
	}
}

func TestStreamEventBus_ClaimPending(t *testing.T) {
	rdb, cleanup := skipIfNoRedis(t)
	defer cleanup()

	b := NewStreamEventBus(rdb, "claimer-"+t.Name())
	defer b.Close()
	topic := "claim.topic." + t.Name()
	group := b.consumerGroup

	if err := rdb.Do(context.Background(), "XGROUP", "CREATE", topic, group, "0", "MKSTREAM").Err(); err != nil {
		_ = err // group may already exist
	}

	if _, err := rdb.XAdd(context.Background(), &redis.XAddArgs{
		Stream: topic,
		MaxLen: 1000,
		Values: map[string]interface{}{
			"payload":   `{"Topic":"claim.topic","Payload":"x"}`,
			"timestamp": "1",
			"producer":  "dead",
		},
	}).Result(); err != nil {
		t.Fatalf("XAdd: %v", err)
	}

	if _, err := rdb.XReadGroup(context.Background(), &redis.XReadGroupArgs{
		Streams:  []string{topic, ">"},
		Group:    group,
		Consumer: "dead-consumer",
		Count:    10,
	}).Result(); err != nil {
		t.Fatalf("XReadGroup: %v", err)
	}

	var got atomic.Int32
	b.Subscribe(topic, func(ctx context.Context, event Event) error {
		got.Add(1)
		return nil
	})
	time.Sleep(200 * time.Millisecond)

	// Mark the pending message as idle by waiting beyond minIdleTime.
	n, err := b.ClaimPending(context.Background(), topic, 1*time.Second)
	if err != nil {
		t.Fatalf("ClaimPending: %v", err)
	}
	if n != 1 {
		t.Fatalf("ClaimPending claimed %d, want 1", n)
	}
	// handler may run asynchronously; give it a moment.
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) && got.Load() == 0 {
		time.Sleep(20 * time.Millisecond)
	}
	if got.Load() != 1 {
		t.Fatalf("handler invoked %d times, want 1", got.Load())
	}
}

func TestStreamEventBus_Trim(t *testing.T) {
	rdb, cleanup := skipIfNoRedis(t)
	defer cleanup()

	b := NewStreamEventBus(rdb, "trimmer-"+t.Name())
	defer b.Close()
	topic := "trim.topic." + t.Name()
	for i := 0; i < 10; i++ {
		if err := rdb.XAdd(context.Background(), &redis.XAddArgs{
			Stream: topic,
			Values: map[string]interface{}{"payload": "x"},
		}).Err(); err != nil {
			t.Fatalf("XAdd: %v", err)
		}
	}
	if err := b.Trim(context.Background(), topic, true, 5); err != nil {
		t.Fatalf("Trim: %v", err)
	}
	n, err := rdb.XLen(context.Background(), topic).Result()
	if err != nil {
		t.Fatalf("XLen: %v", err)
	}
	if n != 5 {
		t.Fatalf("stream len = %d, want 5", n)
	}
}

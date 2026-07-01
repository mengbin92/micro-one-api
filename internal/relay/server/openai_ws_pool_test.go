package server

import (
	"context"
	"net/http"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	coderws "github.com/coder/websocket"
)

// dialCountingDialer is an openAIWSUpstreamDialer that counts how many times
// Dial was called. It returns an in-memory fake connection pair so the pool
// can reuse / close them without a real network round-trip.
type dialCountingDialer struct {
	calls   atomic.Int64
	failNth int64 // if >0, the Nth (1-indexed) dial returns an error
}

func (d *dialCountingDialer) Dial(ctx context.Context, wsURL string, headers http.Header) (openAIWSFrameConn, int, http.Header, error) {
	n := d.calls.Add(1)
	if d.failNth > 0 && n == d.failNth {
		return nil, 500, nil, errDialFailed
	}
	// Return a fake conn whose reads block until closed (simulating a live
	// upstream that never sends unsolicited frames).
	clientSide, upstreamSide := newPoolFakeFrameConnPair()
	// Close the upstream-side read channel so a ReadFrame returns immediately
	// with "read closed" once the pool drains it — but keep a drain goroutine
	// so writes don't block.
	go func() {
		for range upstreamSide.out {
		}
	}()
	close(upstreamSide.in)
	return clientSide, 0, nil, nil
}

var errDialFailed = &dialErr{msg: "simulated dial failure"}

type dialErr struct{ msg string }

func (e *dialErr) Error() string { return e.msg }

func newPoolFakeFrameConnPair() (*poolFakeFrameConn, *poolFakeFrameConn) {
	a2b := make(chan fakeFrame, 4)
	b2a := make(chan fakeFrame, 4)
	return &poolFakeFrameConn{in: a2b, out: b2a}, &poolFakeFrameConn{in: b2a, out: a2b}
}

// fakeFrameConn for the pool tests (independent in/out channels, no loopback
// send-on-closed races).
type poolFakeFrameConn struct {
	in     chan fakeFrame
	out    chan fakeFrame
	closed atomic.Bool
}

func (c *poolFakeFrameConn) ReadFrame(ctx context.Context) (coderws.MessageType, []byte, error) {
	select {
	case f, ok := <-c.in:
		if !ok {
			return coderws.MessageText, nil, &fakeClosedErr{}
		}
		return f.msgType, f.payload, f.err
	case <-ctx.Done():
		return coderws.MessageText, nil, ctx.Err()
	}
}

func (c *poolFakeFrameConn) WriteFrame(ctx context.Context, msgType coderws.MessageType, payload []byte) error {
	select {
	case c.out <- fakeFrame{msgType: msgType, payload: payload}:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

func (c *poolFakeFrameConn) Close() error {
	c.closed.Store(true)
	return nil
}

type fakeClosedErr struct{}

func (e *fakeClosedErr) Error() string { return "read closed" }

func TestConnPoolReusesIdleConnection(t *testing.T) {
	pool := newOpenAIWSConnPool(2 * time.Second)
	pool.dialer = &dialCountingDialer{}
	defer pool.Close()

	ctx := context.Background()
	hdr := http.Header{"Authorization": []string{"Bearer x"}}

	pc1, err := pool.AcquireOrDial(ctx, 1, "wss://upstream/v1/responses", hdr)
	if err != nil {
		t.Fatalf("first acquire: %v", err)
	}
	if pool.acquireCreate.Load() != 1 {
		t.Errorf("expected 1 create, got %d", pool.acquireCreate.Load())
	}

	// Release and immediately re-acquire: should reuse the same conn.
	pool.Release(pc1, false)
	pc2, err := pool.AcquireOrDial(ctx, 1, "wss://upstream/v1/responses", hdr)
	if err != nil {
		t.Fatalf("second acquire: %v", err)
	}
	if pool.acquireReuse.Load() != 1 {
		t.Errorf("expected 1 reuse, got %d", pool.acquireReuse.Load())
	}
	pool.Release(pc2, false)
}

func TestConnPoolDialsNewWhenAllLeased(t *testing.T) {
	pool := newOpenAIWSConnPool(2 * time.Second)
	pool.dialer = &dialCountingDialer{}
	defer pool.Close()

	ctx := context.Background()
	hdr := http.Header{}

	pc1, _ := pool.AcquireOrDial(ctx, 1, "wss://u", hdr)
	// pc1 still leased -> second acquire must dial a new conn.
	pc2, err := pool.AcquireOrDial(ctx, 1, "wss://u", hdr)
	if err != nil {
		t.Fatalf("second acquire: %v", err)
	}
	if pool.acquireCreate.Load() != 2 {
		t.Errorf("expected 2 creates, got %d", pool.acquireCreate.Load())
	}
	pool.Release(pc1, false)
	pool.Release(pc2, false)
}

func TestConnPoolBrokenConnNotReused(t *testing.T) {
	pool := newOpenAIWSConnPool(2 * time.Second)
	pool.dialer = &dialCountingDialer{}
	defer pool.Close()

	ctx := context.Background()
	pc, _ := pool.AcquireOrDial(ctx, 1, "wss://u", http.Header{})
	// Release as broken: should be closed, not pooled.
	pool.Release(pc, true)

	// Next acquire must dial fresh (no reuse).
	_, err := pool.AcquireOrDial(ctx, 1, "wss://u", http.Header{})
	if err != nil {
		t.Fatalf("acquire after broken: %v", err)
	}
	if pool.acquireReuse.Load() != 0 {
		t.Errorf("expected 0 reuse after broken release, got %d", pool.acquireReuse.Load())
	}
}

func TestConnPoolFailoverOnDialError(t *testing.T) {
	// This tests the pool's dial-failure path (not the full HTTPServer
	// failover, which is covered by the e2e test). A dialer that always fails
	// should return an error, not panic.
	pool := newOpenAIWSConnPool(2 * time.Second)
	pool.dialer = &dialCountingDialer{failNth: 1}
	defer pool.Close()

	_, err := pool.AcquireOrDial(context.Background(), 1, "wss://u", http.Header{})
	if err == nil {
		t.Fatal("expected dial error")
	}
	if !strings.Contains(err.Error(), "simulated dial failure") {
		t.Errorf("unexpected error: %v", err)
	}
	if pool.acquireFail.Load() != 1 {
		t.Errorf("expected 1 acquireFail, got %d", pool.acquireFail.Load())
	}
}

// TestStickyStoreInMemoryOnly verifies the hot-cache path works without Redis.
func TestStickyStoreInMemoryOnly(t *testing.T) {
	store := newOpenAIWSStickyStore(nil)
	ctx := context.Background()

	store.BindResponseChannel(ctx, "default", "resp_123", 42, time.Hour)

	got := store.LookupResponseChannel(ctx, "default", "resp_123")
	if got != 42 {
		t.Errorf("expected channel 42, got %d", got)
	}

	// Unknown response id.
	if got := store.LookupResponseChannel(ctx, "default", "resp_unknown"); got != 0 {
		t.Errorf("expected 0 for unknown, got %d", got)
	}
}

func TestStickyStoreExpiresBinding(t *testing.T) {
	store := newOpenAIWSStickyStore(nil)
	ctx := context.Background()

	store.BindResponseChannel(ctx, "default", "resp_short", 7, 1*time.Millisecond)
	time.Sleep(10 * time.Millisecond)

	if got := store.LookupResponseChannel(ctx, "default", "resp_short"); got != 0 {
		t.Errorf("expected expired binding to return 0, got %d", got)
	}
}

func TestStickySessionStoreInMemoryOnly(t *testing.T) {
	store := newOpenAIWSStickyStore(nil)
	ctx := context.Background()

	store.BindSessionChannel(ctx, "default", "session-a", 42, time.Hour)

	got := store.LookupSessionChannel(ctx, "default", "session-a")
	if got != 42 {
		t.Errorf("expected channel 42, got %d", got)
	}

	if ok := store.RefreshSessionTTL(ctx, "default", "session-a", time.Hour); !ok {
		t.Fatal("expected RefreshSessionTTL to refresh existing binding")
	}

	store.DeleteSession(ctx, "default", "session-a")
	if got := store.LookupSessionChannel(ctx, "default", "session-a"); got != 0 {
		t.Errorf("expected deleted binding to return 0, got %d", got)
	}
}

func TestStickySessionStoreExpiresBinding(t *testing.T) {
	store := newOpenAIWSStickyStore(nil)
	ctx := context.Background()

	store.BindSessionChannel(ctx, "default", "session-short", 7, 1*time.Millisecond)
	time.Sleep(10 * time.Millisecond)

	if got := store.LookupSessionChannel(ctx, "default", "session-short"); got != 0 {
		t.Errorf("expected expired binding to return 0, got %d", got)
	}
}

// TestFailoverMaxSwitchesDefault verifies the config-driven failover limit
// accessor returns the documented default when no config is set.
func TestFailoverMaxSwitchesDefault(t *testing.T) {
	s := &HTTPServer{}
	if got := s.openAIWSFailoverMaxSwitches(); got != 2 {
		t.Errorf("expected default failover switches 2, got %d", got)
	}
	s.SetOpenAIWSPoolConfig(0, 5, 0)
	if got := s.openAIWSFailoverMaxSwitches(); got != 5 {
		t.Errorf("expected configured failover switches 5, got %d", got)
	}
}

func TestStickyTTLDefault(t *testing.T) {
	s := &HTTPServer{}
	if got := s.openAIWSStickyTTL(); got != openAIWSStickyTTL {
		t.Errorf("expected default sticky ttl, got %v", got)
	}
}

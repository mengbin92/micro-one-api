package server

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"
	coderws "github.com/coder/websocket"
	"github.com/redis/go-redis/v9"
	relaybiz "micro-one-api/internal/biz"
)

// dialCountingDialer is an openAIWSUpstreamDialer that counts how many times
// Dial was called. It returns an in-memory connection so the pool
// can reuse or close it without a network round trip.
type dialCountingDialer struct {
	calls   atomic.Int64
	failNth int64 // if >0, the Nth (1-indexed) dial returns an error
}

func (d *dialCountingDialer) Dial(ctx context.Context, wsURL string, headers http.Header) (openAIWSFrameConn, int, http.Header, error) {
	n := d.calls.Add(1)
	if d.failNth > 0 && n == d.failNth {
		return nil, 500, nil, errors.New("simulated dial failure")
	}
	return newFakeFrameConn(4), 0, nil, nil
}

func TestConnPoolReusesIdleConnection(t *testing.T) {
	pool := newOpenAIWSConnPool(2*time.Second, nil)
	pool.dialer = &dialCountingDialer{}
	defer pool.Close()

	ctx := context.Background()
	hdr := http.Header{"Authorization": []string{"Bearer x"}}

	pc1, err := pool.AcquireOrDial(ctx, "channel:1", "wss://upstream/v1/responses", hdr)
	if err != nil {
		t.Fatalf("first acquire: %v", err)
	}
	if pool.acquireCreate.Load() != 1 {
		t.Errorf("expected 1 create, got %d", pool.acquireCreate.Load())
	}

	// Release and immediately re-acquire: should reuse the same conn.
	pool.Release(pc1, false)
	pc2, err := pool.AcquireOrDial(ctx, "channel:1", "wss://upstream/v1/responses", hdr)
	if err != nil {
		t.Fatalf("second acquire: %v", err)
	}
	if pool.acquireReuse.Load() != 1 {
		t.Errorf("expected 1 reuse, got %d", pool.acquireReuse.Load())
	}
	pool.Release(pc2, false)
}

func TestConnPoolFingerprintsAreScopedToPool(t *testing.T) {
	headers := http.Header{"Authorization": {"Bearer test-credential"}}
	var conns [2]*openAIWSPooledConn
	for i := range conns {
		pool := newOpenAIWSConnPool(time.Second, nil)
		pool.dialer = &dialCountingDialer{}
		defer pool.Close()
		pc, err := pool.AcquireOrDial(t.Context(), "channel:1", "wss://upstream.example", headers)
		if err != nil {
			t.Fatal(err)
		}
		pool.Release(pc, false)
		conns[i] = pc
	}
	if conns[0].fingerprint == conns[1].fingerprint {
		t.Fatal("independent pools generated the same handshake fingerprint")
	}
}

func TestConnPoolDialsNewWhenAllLeased(t *testing.T) {
	pool := newOpenAIWSConnPool(2*time.Second, nil)
	pool.dialer = &dialCountingDialer{}
	defer pool.Close()

	ctx := context.Background()
	hdr := http.Header{}

	pc1, _ := pool.AcquireOrDial(ctx, "channel:1", "wss://u", hdr)
	// pc1 still leased -> second acquire must dial a new conn.
	pc2, err := pool.AcquireOrDial(ctx, "channel:1", "wss://u", hdr)
	if err != nil {
		t.Fatalf("second acquire: %v", err)
	}
	if pool.acquireCreate.Load() != 2 {
		t.Errorf("expected 2 creates, got %d", pool.acquireCreate.Load())
	}
	pool.Release(pc1, false)
	pool.Release(pc2, false)
}

func TestConnPoolLeasedConnectionStaysOutOfIdlePool(t *testing.T) {
	pool := newOpenAIWSConnPool(time.Second, nil)
	pool.dialer = &dialCountingDialer{}
	pool.maxConnsPerChannel = 1
	defer pool.Close()
	const target = "wss://upstream.example"
	for range 3 {
		pc, err := pool.AcquireOrDial(t.Context(), "channel:1", target, nil)
		if err != nil || pc.conn.(*fakeFrameConn).closed.Load() {
			t.Fatal("leased a closed connection", err)
		}
		pool.Release(pc, false)
	}
	pc, err := pool.AcquireOrDial(t.Context(), "channel:1", target, nil)
	if err != nil {
		t.Fatal(err)
	}
	pool.Close()
	if pc.conn.(*fakeFrameConn).closed.Load() {
		t.Fatal("pool closed a leased connection")
	}
	pool.Release(pc, false)
	if !pc.conn.(*fakeFrameConn).closed.Load() {
		t.Fatal("shutdown leaked a released connection")
	}
	if _, err := pool.AcquireOrDial(t.Context(), "channel:1", target, nil); err != errOpenAIWSPoolClosed {
		t.Fatal("acquired after shutdown", err)
	}
}

func TestConnPoolReusesUntouchedUpstreamAfterLocalRejection(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, err := coderws.Accept(w, r, nil)
		if err != nil {
			t.Error(err)
			return
		}
		defer conn.CloseNow()
		kind, data, err := conn.Read(r.Context())
		if err == nil {
			_ = conn.Write(r.Context(), kind, data)
		}
	}))
	defer srv.Close()
	pool := newOpenAIWSConnPool(time.Second, nil)
	defer pool.Close()
	ctx, cancel := context.WithTimeout(t.Context(), 3*time.Second)
	defer cancel()
	get := func() *openAIWSPooledConn {
		pc, err := pool.AcquireOrDial(ctx, "channel:1", wsURL(srv.URL), nil)
		if err != nil {
			t.Fatal(err)
		}
		return pc
	}
	first := get()
	result, exit := relayOpenAIWSFrames(ctx, newFakeFrameConn(1), first.conn, nil, openAIWSRelayOptions{
		beforeWriteUp: func(context.Context, coderws.MessageType, []byte) ([]byte, error) { return nil, errWSRoutingAdmission },
	})
	if exit == nil || exit.err != errWSRoutingAdmission || result.firstWriteAttempted {
		t.Fatal("expected a local rejection before forwarding")
	}
	pool.Release(first, result.firstWriteAttempted)
	second := get()
	defer pool.Release(second, true)
	if first != second {
		t.Fatal("dialed a second connection for an unused, healthy upstream")
	}
	if err := second.conn.WriteFrame(ctx, coderws.MessageText, []byte("test frame")); err != nil {
		t.Fatal(err)
	}
	if _, data, err := second.conn.ReadFrame(ctx); err != nil || string(data) != "test frame" {
		t.Fatalf("missing reply from reused connection: %v", err)
	}
}

func TestConnPoolExpiresIdleSocketWithoutAnotherAcquire(t *testing.T) {
	pool := newOpenAIWSConnPool(time.Second, nil)
	pool.dialer = &dialCountingDialer{}
	pool.maxIdle = 5 * time.Millisecond
	defer pool.Close()
	pc, err := pool.AcquireOrDial(t.Context(), "channel:1", "wss://upstream.example", nil)
	if err != nil {
		t.Fatal(err)
	}
	pool.Release(pc, false)
	waitFor(t, time.Second, pc.conn.(*fakeFrameConn).closed.Load)
}

func TestConnPoolUsesConfiguredIdleCapacity(t *testing.T) {
	s := &HTTPServer{}
	s.SetOpenAIWSPoolConfig(3, 0, 0)
	s.SetOpenAIWSConnPool()
	defer s.wsPool.Close()
	if s.wsPool.maxConnsPerChannel != 3 {
		t.Fatal("initial pool capacity was ignored")
	}
	s.SetOpenAIWSPoolConfig(1, 0, 0)
	if s.wsPool.maxConnsPerChannel != 1 {
		t.Fatal("configured pool capacity was ignored")
	}
}

func TestConnPoolBrokenConnNotReused(t *testing.T) {
	pool := newOpenAIWSConnPool(2*time.Second, nil)
	pool.dialer = &dialCountingDialer{}
	defer pool.Close()

	ctx := context.Background()
	pc, _ := pool.AcquireOrDial(ctx, "channel:1", "wss://u", http.Header{})
	// Release as broken: should be closed, not pooled.
	pool.Release(pc, true)

	// Next acquire must dial fresh (no reuse).
	_, err := pool.AcquireOrDial(ctx, "channel:1", "wss://u", http.Header{})
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
	pool := newOpenAIWSConnPool(2*time.Second, nil)
	pool.dialer = &dialCountingDialer{failNth: 1}
	defer pool.Close()

	_, err := pool.AcquireOrDial(context.Background(), "channel:1", "wss://u", http.Header{})
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

// TestConnPoolIsolatesNamespaces is the regression test for the cross-source
// reuse bug: channel #5 and subscription account #5 (independent ID
// namespaces) must never share a pooled connection.
func TestConnPoolIsolatesNamespaces(t *testing.T) {
	pool := newOpenAIWSConnPool(2*time.Second, nil)
	pool.dialer = &dialCountingDialer{}
	defer pool.Close()

	ctx := context.Background()
	channelHdr := http.Header{"Authorization": []string{"Bearer channel-key"}}
	subHdr := http.Header{"Authorization": []string{"Bearer oauth-token"}}

	pc, err := pool.AcquireOrDial(ctx, "channel:5", "wss://api.openai.com/v1/responses", channelHdr)
	if err != nil {
		t.Fatalf("channel acquire: %v", err)
	}
	pool.Release(pc, false)

	// Same numeric ID, different namespace: must NOT reuse the channel conn.
	pc2, err := pool.AcquireOrDial(ctx, "subscription:5", "wss://other.example.com/v1/responses", subHdr)
	if err != nil {
		t.Fatalf("subscription acquire: %v", err)
	}
	if pool.acquireReuse.Load() != 0 {
		t.Errorf("subscription acquire reused channel conn (reuse=%d)", pool.acquireReuse.Load())
	}
	if pool.acquireCreate.Load() != 2 {
		t.Errorf("expected 2 creates, got %d", pool.acquireCreate.Load())
	}
	pool.Release(pc2, false)
}

// TestConnPoolRejectsRotatedCredential verifies a conn dialed with an old
// credential is never reused after the source's key/URL changes.
func TestConnPoolRejectsRotatedCredential(t *testing.T) {
	pool := newOpenAIWSConnPool(2*time.Second, nil)
	pool.dialer = &dialCountingDialer{}
	defer pool.Close()

	ctx := context.Background()
	oldHdr := http.Header{"Authorization": []string{"Bearer old"}}
	newHdr := http.Header{"Authorization": []string{"Bearer new"}}

	pc, _ := pool.AcquireOrDial(ctx, "channel:5", "wss://u", oldHdr)
	pool.Release(pc, false)

	// Credential rotated: stale conn must be evicted, fresh dial required.
	pc2, err := pool.AcquireOrDial(ctx, "channel:5", "wss://u", newHdr)
	if err != nil {
		t.Fatalf("acquire after rotation: %v", err)
	}
	if pool.acquireReuse.Load() != 0 {
		t.Errorf("reused stale-credential conn (reuse=%d)", pool.acquireReuse.Load())
	}
	pool.Release(pc2, false)

	// Same fingerprint reuses again.
	pc3, err := pool.AcquireOrDial(ctx, "channel:5", "wss://u", newHdr)
	if err != nil {
		t.Fatalf("re-acquire: %v", err)
	}
	if pool.acquireReuse.Load() != 1 {
		t.Errorf("expected reuse with matching fingerprint, got %d", pool.acquireReuse.Load())
	}
	pool.Release(pc3, false)
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

func TestStickyStoreReplicaHonorsRemoteExpiry(t *testing.T) {
	srv := miniredis.RunT(t)
	client := redis.NewClient(&redis.Options{Addr: srv.Addr()})
	t.Cleanup(func() { _ = client.Close() })
	writer, reader := newOpenAIWSStickyStore(client), newOpenAIWSStickyStore(client)
	for _, tc := range []struct {
		name   string
		bind   func(context.Context, string, string, int64, time.Duration)
		lookup func(context.Context, string, string) int64
	}{
		{"response", writer.BindResponseChannel, reader.LookupResponseChannel},
		{"session", writer.BindSessionChannel, reader.LookupSessionChannel},
	} {
		t.Run(tc.name, func(t *testing.T) {
			tc.bind(t.Context(), "default", "shared", 7, 100*time.Millisecond)
			if id := tc.lookup(t.Context(), "default", "shared"); id != 7 {
				t.Fatalf("replica read %d before expiry", id)
			}
			srv.FastForward(100 * time.Millisecond)
			if id := tc.lookup(t.Context(), "default", "shared"); id != 0 {
				t.Fatalf("replica retained %d after Redis expiry", id)
			}
		})
	}
}

func TestStickyStoreUsesSharedSessionAfterFailover(t *testing.T) {
	srv := miniredis.RunT(t)
	client := redis.NewClient(&redis.Options{Addr: srv.Addr()})
	t.Cleanup(func() { _ = client.Close() })
	a, b := newOpenAIWSStickyStore(client), newOpenAIWSStickyStore(client)
	ctx := t.Context()
	a.BindSessionChannel(ctx, "default", "session", 3, time.Hour)
	b.BindSessionChannel(ctx, "default", "session", 7, time.Hour)
	if got := a.LookupSessionChannel(ctx, "default", "session"); got != 7 {
		t.Fatalf("former writer read %d after failover, want 7", got)
	}
	if !a.RefreshSessionTTL(ctx, "default", "session", 100*time.Millisecond) {
		t.Fatal("shared binding should be refreshed")
	}
	srv.FastForward(100 * time.Millisecond)
	if got := b.LookupSessionChannel(ctx, "default", "session"); got != 0 {
		t.Fatalf("writer kept expired binding %d", got)
	}
	a.BindSessionChannel(ctx, "default", "session", 3, time.Hour)
	b.DeleteSession(ctx, "default", "session")
	if a.RefreshSessionTTL(ctx, "default", "session", time.Hour) || a.LookupSessionChannel(ctx, "default", "session") != 0 {
		t.Fatal("remote delete must invalidate the original writer")
	}
	a.BindSessionChannel(ctx, "default", "session", 3, time.Hour)
	_ = client.Close()
	if a.LookupSessionChannel(ctx, "default", "session") != 0 {
		t.Fatal("unavailable Redis must yield a miss")
	}
}

func TestStickyStoreKeepsChannelAndSubscriptionNamespacesSeparate(t *testing.T) {
	store := newOpenAIWSStickyStore(nil)
	ctx := context.Background()

	store.BindResponseRoute(ctx, "default", "resp-channel", &relaybiz.Channel{ID: 7}, time.Hour)
	store.BindResponseRoute(ctx, "default", "resp-subscription", &relaybiz.Channel{ID: 7, SubscriptionAccountID: 7}, time.Hour)

	channelSource := store.LookupResponseRoute(ctx, "default", "resp-channel")
	if channelSource.kind != relaybiz.UpstreamRouteChannel || channelSource.id != 7 {
		t.Fatalf("channel response source = %s:%d, want channel:7", channelSource.kind.String(), channelSource.id)
	}
	subscriptionSource := store.LookupResponseRoute(ctx, "default", "resp-subscription")
	if subscriptionSource.kind != relaybiz.UpstreamRouteSubscription || subscriptionSource.id != 7 {
		t.Fatalf("subscription response source = %s:%d, want subscription:7", subscriptionSource.kind.String(), subscriptionSource.id)
	}
	if got := store.LookupResponseChannel(ctx, "default", "resp-subscription"); got != 0 {
		t.Fatalf("legacy channel lookup returned subscription id %d", got)
	}

	store.BindSessionRoute(ctx, "default", "session-channel", &relaybiz.Channel{ID: 7}, time.Hour)
	store.BindSessionChannel(ctx, "default", "session-subscription", 7, time.Hour)
	if got := store.LookupSessionRoute(ctx, "default", "session-channel"); got.kind != relaybiz.UpstreamRouteChannel || got.id != 7 {
		t.Fatalf("channel session source = %s:%d, want channel:7", got.kind.String(), got.id)
	}
	if got := store.LookupSessionRoute(ctx, "default", "session-subscription"); got.kind != relaybiz.UpstreamRouteSubscription || got.id != 7 {
		t.Fatalf("subscription session source = %s:%d, want subscription:7", got.kind.String(), got.id)
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

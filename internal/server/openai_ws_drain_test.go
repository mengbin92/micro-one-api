package server

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	coderws "github.com/coder/websocket"

	appws "micro-one-api/platform/websocket"
)

// TestWSDrain_HealthzFlipsTo503 verifies that, once DrainWSConnections is
// invoked, /healthz reports 503 + drain=true so load balancers stop sending
// traffic, and that it flips back is not part of this test (drain is terminal).
func TestWSDrain_HealthzFlipsTo503(t *testing.T) {
	s := &HTTPServer{}
	// No tracker wired: should still be healthy (200).
	if s.IsWSDraining() {
		t.Fatal("server should not be draining before SetOpenAIWSConnPool")
	}
	rec := httptest.NewRecorder()
	s.handleHealth(rec, httptest.NewRequest(http.MethodGet, "/healthz", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("pre-drain healthz = %d, want 200", rec.Code)
	}

	// Wire the tracker and start a drain.
	s.SetOpenAIWSConnPool()
	if s.wsConnTracker == nil {
		t.Fatal("wsConnTracker not initialized")
	}
	drainDone := make(chan struct{})
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		_ = s.DrainWSConnections(ctx)
		close(drainDone)
	}()

	// Drain is async; give the CAS flag time to flip.
	waitFor(t, time.Second, s.IsWSDraining)
	if !s.IsWSDraining() {
		t.Fatal("server should be draining after DrainWSConnections started")
	}

	rec = httptest.NewRecorder()
	s.handleHealth(rec, httptest.NewRequest(http.MethodGet, "/healthz", nil))
	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("draining healthz = %d, want 503", rec.Code)
	}
	var body map[string]string
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("unmarshal healthz body: %v", err)
	}
	if body["status"] != "draining" {
		t.Errorf("healthz body status = %q, want draining", body["status"])
	}
	if rec.Header().Get("Retry-After") == "" {
		t.Error("expected Retry-After header while draining")
	}

	// Let the drain finish (no connections to close -> returns immediately
	// after the 100ms ticker observes the empty map).
	<-drainDone
}

// TestWSDrain_RejectsNewUpgradesWhileDraining verifies the ingress gate: while
// the server is draining, a WebSocket upgrade against /v1/responses is
// rejected with 503 + Retry-After instead of being accepted.
func TestWSDrain_RejectsNewUpgradesWhileDraining(t *testing.T) {
	s := &HTTPServer{}
	s.SetOpenAIWSConnPool()

	// Flip the draining flag directly to simulate a drain in progress without
	// waiting for the async Drain() goroutine to notice.
	s.wsConnTracker.SetDrainingForTest(true)
	if !s.IsWSDraining() {
		t.Fatal("expected draining after SetDrainingForTest(true)")
	}

	rec := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodGet, "/v1/responses", nil)
	r.Header.Set("Upgrade", "websocket")
	r.Header.Set("Connection", "Upgrade")
	r.Header.Set("Authorization", "Bearer token")
	s.handleResponsesRelay(rec, r)
	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("upgrade while draining = %d, want 503", rec.Code)
	}
	if rec.Header().Get("Retry-After") == "" {
		t.Error("expected Retry-After on rejected upgrade")
	}
}

// TestWSDrain_ClosesTrackedConnections verifies that DrainWSConnections invokes
// the close function registered for each tracked connection, so in-flight
// relays are torn down (force-close path) once the drain timeout elapses.
func TestWSDrain_ClosesTrackedConnections(t *testing.T) {
	tracker := appws.NewConnectionTracker(&appws.DrainConfig{
		DrainTimeout:       50 * time.Millisecond,
		CloseTimeout:       100 * time.Millisecond,
		MaxConcurrentClose: 4,
	})

	var closed atomic.Int32
	// Register two connections that never close on their own; the drain must
	// force-close them after the drain timeout.
	c1 := tracker.NewConnection("conn-1", func() error { closed.Add(1); return nil })
	c2 := tracker.NewConnection("conn-2", func() error { closed.Add(1); return nil })
	_ = c1
	_ = c2

	if tracker.ActiveCount() != 2 {
		t.Fatalf("active count = %d, want 2", tracker.ActiveCount())
	}

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	if err := tracker.Drain(ctx); err != nil && err != context.DeadlineExceeded {
		t.Logf("drain returned %v (acceptable)", err)
	}

	waitFor(t, time.Second, func() bool { return closed.Load() == 2 })
	if got := closed.Load(); got != 2 {
		t.Fatalf("close func calls = %d, want 2", got)
	}
	if tracker.ActiveCount() != 0 {
		t.Fatalf("active count after drain = %d, want 0", tracker.ActiveCount())
	}
}

// TestWSDrain_HTTPServerDrainClosesTrackedConn verifies the HTTPServer-level
// path end-to-end: a connection registered via the forwarder's tracker is
// closed when DrainWSConnections runs.
func TestWSDrain_HTTPServerDrainClosesTrackedConn(t *testing.T) {
	s := &HTTPServer{}
	s.SetOpenAIWSConnPool()

	var closed atomic.Int32
	conn := s.wsConnTracker.NewConnection("relay-1", func() error { closed.Add(1); return nil })
	_ = conn
	if s.wsConnTracker.ActiveCount() != 1 {
		t.Fatalf("active count = %d, want 1", s.wsConnTracker.ActiveCount())
	}

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	_ = s.DrainWSConnections(ctx)
	waitFor(t, time.Second, func() bool { return closed.Load() == 1 })
	if got := closed.Load(); got != 1 {
		t.Fatalf("close func calls = %d, want 1", got)
	}
}

func TestWSDrain_TracksUpgradeBeforeFirstFrame(t *testing.T) {
	s := &HTTPServer{}
	s.SetOpenAIWSDrainConfig(&appws.DrainConfig{
		DrainTimeout: 20 * time.Millisecond, CloseTimeout: time.Second, MaxConcurrentClose: 1,
	})
	h := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		s.handleResponsesWebSocket(r.Context(), w, r)
	}))
	defer h.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	conn, _, err := coderws.Dial(ctx, "ws"+h.URL[len("http"):], nil)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.CloseNow()
	waitFor(t, 250*time.Millisecond, func() bool { return s.wsConnTracker.ActiveCount() == 1 })
	if got := s.wsConnTracker.ActiveCount(); got != 1 {
		t.Fatalf("tracked connections before first frame = %d, want 1", got)
	}
	if err := s.DrainWSConnections(ctx); err != nil {
		t.Fatal(err)
	}
	if _, _, err := conn.Read(ctx); err == nil || ctx.Err() != nil {
		t.Fatalf("expected drain to close the idle WebSocket, read error = %v", err)
	}
}

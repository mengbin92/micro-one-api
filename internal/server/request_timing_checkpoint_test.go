package server

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"runtime"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"micro-one-api/domain/requesttrace"
	"micro-one-api/pkg/jsonx"
)

// A short producer-pressure loop; the four-pair real SQL gate remains separate.
func TestRequestTimingCheckpointBuffer(t *testing.T) {
	if os.Getenv("TIMING_CHECKPOINT_BUFFER_PROBE") != "1" {
		t.Skip("synthetic sink-pause diagnostic is opt-in")
	}
	release := make(chan struct{})
	var once sync.Once
	unblock := func() { once.Do(func() { close(release) }) }
	exporter := requesttrace.NewExporter(func(ctx context.Context, _ *requesttrace.TimingSummary) error {
		select {
		case <-release:
			return nil
		case <-ctx.Done():
			return ctx.Err()
		}
	}, nil)
	defer func() {
		unblock()
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = exporter.Close(ctx)
	}()
	server := &HTTPServer{}
	server.SetTimingExporter(exporter)
	handler := server.observeRequestTiming(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requesttrace.BindUser(r.Context(), 7)
		time.Sleep(50 * time.Millisecond)
		_, _ = io.WriteString(w, "ok")
	}), "/v1/perf", "legacy")
	front := httptest.NewServer(handler)
	defer front.Close()
	transport := &http.Transport{MaxIdleConns: 128, MaxIdleConnsPerHost: 128}
	defer transport.CloseIdleConnections()
	client := &http.Client{Transport: transport, Timeout: time.Second}
	var backlog, heap atomic.Uint64
	timer := time.AfterFunc(3*time.Second, func() {
		stats := exporter.Stats()
		backlog.Store(uint64(stats.Accepted - stats.Exported - stats.Failed))
		var mem runtime.MemStats
		runtime.ReadMemStats(&mem)
		heap.Store(mem.HeapAlloc)
		unblock()
	})
	defer timer.Stop()
	var requests, failures atomic.Int64
	deadline := time.Now().Add(4 * time.Second)
	var wg sync.WaitGroup
	for range 64 {
		wg.Go(func() {
			for time.Now().Before(deadline) {
				response, err := client.Get(front.URL)
				requests.Add(1)
				if err != nil {
					failures.Add(1)
					continue
				}
				_, err = io.Copy(io.Discard, response.Body)
				_ = response.Body.Close()
				if err != nil || response.StatusCode != http.StatusOK {
					failures.Add(1)
				}
			}
		})
	}
	wg.Wait()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	require.NoError(t, exporter.Close(ctx))
	stats := exporter.Stats()
	encoded, err := jsonx.MarshalIndent(map[string]any{
		"scope": "synthetic sink pause; not SQL capacity admission", "pause_seconds": 3, "load_seconds": 4,
		"http_workers": 64, "fake_upstream_wait_ms": 50, "requests": requests.Load(), "http_failures": failures.Load(),
		"worker_count": requesttrace.ExporterWorkerCount, "queue_capacity": requesttrace.ExporterQueueCapacity,
		"backlog_at_release": backlog.Load(), "heap_alloc_at_release": heap.Load(),
		"accepted": stats.Accepted, "exported": stats.Exported, "failed": stats.Failed, "dropped": stats.Dropped,
	}, "", "  ")
	require.NoError(t, err)
	t.Log(string(encoded))
	if path := os.Getenv("TIMING_CHECKPOINT_BUFFER_OUTPUT"); path != "" {
		require.NoError(t, os.WriteFile(path, encoded, 0600))
	}
	require.Greater(t, requests.Load(), int64(3000), "drive sustained producer pressure")
	require.Zero(t, failures.Load(), "the stalled sink must not block HTTP")
	require.Zero(t, stats.Dropped, "the fixed buffer must absorb the diagnostic pause")
	require.Zero(t, stats.Failed)
	require.Equal(t, requests.Load(), stats.Exported)
}

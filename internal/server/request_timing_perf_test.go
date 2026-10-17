package server

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"runtime"
	"sort"
	"sync"
	"sync/atomic"
	"syscall"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/exporters/otlp/otlptrace/otlptracehttp"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/trace/noop"
	"micro-one-api/domain/requesttrace"
	"micro-one-api/domain/upstream/evidence"
	"micro-one-api/pkg/jsonx"
)

type perfSpanCounter struct{ count atomic.Int64 }

func (p *perfSpanCounter) OnStart(context.Context, sdktrace.ReadWriteSpan) {}
func (p *perfSpanCounter) OnEnd(sdktrace.ReadOnlySpan)                     { p.count.Add(1) }
func (*perfSpanCounter) Shutdown(context.Context) error                    { return nil }
func (*perfSpanCounter) ForceFlush(context.Context) error                  { return nil }

// This controlled fake-upstream comparison is an initial rollout gate, not a
// claim about supplier latency or a production throughput/capacity benchmark.
func TestRequestTimingControlledOverhead(t *testing.T) {
	if os.Getenv("TIMING_PERF_ACCEPTANCE") != "1" {
		t.Skip("controlled acceptance is opt-in")
	}
	var bytesExported atomic.Int64
	collector := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		n, _ := io.Copy(io.Discard, r.Body)
		bytesExported.Add(n)
		w.WriteHeader(200)
	}))
	defer collector.Close()
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		time.Sleep(50 * time.Millisecond)
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = io.WriteString(w, "data: {\"choices\":[{\"delta\":{\"content\":\"hello\"}}]}\n\ndata: [DONE]\n\n")
	}))
	defer upstream.Close()
	type sample struct {
		P95MS                    float64 `json:"p95_ms"`
		AllocatedBytesPerRequest float64 `json:"allocated_bytes_per_request"`
		CPUUsPerRequest          float64 `json:"cpu_us_per_request"`
		Exported                 int64   `json:"exported"`
		Dropped                  int64   `json:"dropped"`
	}
	previous := otel.GetTracerProvider()
	defer otel.SetTracerProvider(previous)
	results := map[string]sample{}
	spans := &perfSpanCounter{}
	for _, enabled := range []bool{false, true} {
		var provider *sdktrace.TracerProvider
		if enabled {
			exporter, err := otlptracehttp.New(context.Background(), otlptracehttp.WithEndpointURL(collector.URL+"/v1/traces"))
			require.NoError(t, err)
			provider = sdktrace.NewTracerProvider(sdktrace.WithSpanProcessor(spans), sdktrace.WithBatcher(exporter, sdktrace.WithBatchTimeout(100*time.Millisecond)))
			otel.SetTracerProvider(provider)
		} else {
			otel.SetTracerProvider(noop.NewTracerProvider())
		}
		server := &HTTPServer{}
		var summaries *requesttrace.Exporter
		if enabled {
			summaries = requesttrace.NewExporter(func(context.Context, *requesttrace.TimingSummary) error { return nil }, nil)
			server.SetTimingExporter(summaries)
		}
		handler := server.wrapRoute("/v1/perf", executionRoute("/v1/perf", "legacy"), http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			ctx, end := requesttrace.Phase(r.Context(), "relay.plan")
			requesttrace.BindUser(ctx, 7)
			end(nil)
			ctx = requesttrace.WithAttempt(ctx, requesttrace.Attempt{Number: 1, SourceKind: "channel"})
			requesttrace.RecordAttempt(ctx, requesttrace.TimingAttempt{Number: 1, RequestID: "controlled-perf", Outcome: "success"})
			req, _ := http.NewRequestWithContext(ctx, "GET", upstream.URL, nil)
			resp, err := evidence.Do(upstream.Client(), req, "openai_chat")
			if err != nil {
				w.WriteHeader(502)
				return
			}
			defer resp.Body.Close()
			w.Header().Set("Content-Type", "text/event-stream")
			_, _ = io.Copy(w, resp.Body)
			_ = http.NewResponseController(w).Flush()
		}))
		for range 8 {
			handler.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest("POST", "/v1/perf", nil))
		}
		runtime.GC()
		var before, after runtime.MemStats
		runtime.ReadMemStats(&before)
		var cpuBefore, cpuAfter syscall.Rusage
		require.NoError(t, syscall.Getrusage(syscall.RUSAGE_SELF, &cpuBefore))
		var mu sync.Mutex
		durations := make([]float64, 0, 160)
		jobs := make(chan struct{}, 160)
		for range 160 {
			jobs <- struct{}{}
		}
		close(jobs)
		var workers sync.WaitGroup
		for range 8 {
			workers.Go(func() {
				for range jobs {
					start := time.Now()
					handler.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest("POST", "/v1/perf", nil))
					mu.Lock()
					durations = append(durations, float64(time.Since(start))/1e6)
					mu.Unlock()
				}
			})
		}
		workers.Wait()
		if enabled {
			ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
			require.NoError(t, summaries.Close(ctx))
			require.NoError(t, provider.Shutdown(ctx))
			cancel()
		}
		runtime.ReadMemStats(&after)
		require.NoError(t, syscall.Getrusage(syscall.RUSAGE_SELF, &cpuAfter))
		cpu := func(r syscall.Rusage) int64 {
			return r.Utime.Sec*1e6 + int64(r.Utime.Usec) + r.Stime.Sec*1e6 + int64(r.Stime.Usec)
		}
		sort.Float64s(durations)
		entry := sample{P95MS: durations[len(durations)*95/100], AllocatedBytesPerRequest: float64(after.TotalAlloc-before.TotalAlloc) / 160, CPUUsPerRequest: float64(cpu(cpuAfter)-cpu(cpuBefore)) / 160}
		name := "disabled"
		if enabled {
			name = "enabled"
			entry.Exported = summaries.Stats().Exported
			entry.Dropped = summaries.Stats().Dropped
			require.Zero(t, entry.Dropped)
			require.Zero(t, summaries.Stats().Failed)
		}
		results[name] = entry
	}
	overhead := (results["enabled"].P95MS/results["disabled"].P95MS - 1) * 100
	report := map[string]any{"requests_per_mode": 160, "concurrency": 8, "fake_upstream_wait_ms": 50, "results": results, "p95_overhead_percent": overhead, "spans": spans.count.Load(), "otlp_bytes": bytesExported.Load()}
	encoded, err := jsonx.MarshalIndent(report, "", "  ")
	require.NoError(t, err)
	fmt.Println(string(encoded))
	if path := os.Getenv("TIMING_PERF_OUTPUT"); path != "" {
		require.NoError(t, os.WriteFile(path, encoded, 0600))
	}
	require.LessOrEqual(t, overhead, 5.0, "controlled initial P95 gate")
}

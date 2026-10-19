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
	"strconv"
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
	collecttracev1 "go.opentelemetry.io/proto/otlp/collector/trace/v1"
	"google.golang.org/protobuf/proto"
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
	runRequestTimingOverhead(t, nil, "")
}

func runRequestTimingOverhead(t *testing.T, sqlStack *timingSQLStack, driver string) {
	runRequestTimingOverheadDuration(t, sqlStack, driver, 30*time.Second)
}

func runRequestTimingOverheadDuration(t *testing.T, sqlStack *timingSQLStack, driver string, duration time.Duration) {
	var bytesExported, receivedSpans atomic.Int64
	collector := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		payload, err := io.ReadAll(r.Body)
		var batch collecttracev1.ExportTraceServiceRequest
		if err != nil || proto.Unmarshal(payload, &batch) != nil {
			http.Error(w, "invalid OTLP batch", http.StatusBadRequest)
			return
		}
		bytesExported.Add(int64(len(payload)))
		for _, resource := range batch.ResourceSpans {
			for _, scope := range resource.ScopeSpans {
				receivedSpans.Add(int64(len(scope.Spans)))
			}
		}
		w.WriteHeader(200)
	}))
	defer collector.Close()
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		time.Sleep(50 * time.Millisecond)
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = io.WriteString(w, "data: {\"choices\":[{\"delta\":{\"content\":\"hello\"}}]}\n\ndata: [DONE]\n\n")
	}))
	defer upstream.Close()
	upstream.Client().Transport = &http.Transport{MaxIdleConns: 64, MaxIdleConnsPerHost: 64}
	defer upstream.Client().CloseIdleConnections()
	type sample struct {
		Requests                 int     `json:"requests"`
		DurationSeconds          float64 `json:"duration_seconds"`
		P95MS                    float64 `json:"p95_ms"`
		AllocatedBytesPerRequest float64 `json:"allocated_bytes_per_request"`
		CPUUsPerRequest          float64 `json:"cpu_us_per_request"`
		GCs                      uint32  `json:"gcs"`
		DrainMS                  float64 `json:"drain_ms"`
		DrainCPUUs               int64   `json:"drain_cpu_us"`
		DrainAllocatedBytes      uint64  `json:"drain_allocated_bytes"`
		Exported                 int64   `json:"exported"`
		Dropped                  int64   `json:"dropped"`
		Failed                   int64   `json:"failed"`
		PersistenceP95MS         float64 `json:"persistence_p95_ms"`
		ExportRPCP95MS           float64 `json:"export_rpc_p95_ms"`
		QueueWaitP95MS           float64 `json:"queue_wait_p95_ms"`
		SummaryBacklogPeak       int64   `json:"summary_backlog_peak"`
		QueryP95MS               float64 `json:"query_p95_ms"`
		Queries                  int     `json:"queries"`
		QueryFailures            int64   `json:"query_failures"`
		HTTPFailures             int64   `json:"http_failures"`
	}
	const warmup, pairs = 8, 4
	concurrency := 64
	if sqlStack != nil && os.Getenv("TIMING_SQL_CONCURRENCY") != "" {
		var err error
		concurrency, err = strconv.Atoi(os.Getenv("TIMING_SQL_CONCURRENCY"))
		require.NoError(t, err)
		require.GreaterOrEqual(t, concurrency, 1)
		require.LessOrEqual(t, concurrency, 64)
	}
	previous := otel.GetTracerProvider()
	defer otel.SetTracerProvider(previous)
	spans := &perfSpanCounter{}
	rounds := make([]map[string]any, 0, pairs)
	var gateFailures []string
	cpu := func(r syscall.Rusage) int64 {
		return r.Utime.Sec*1e6 + int64(r.Utime.Usec) + r.Stime.Sec*1e6 + int64(r.Stime.Usec)
	}
	for pair := range pairs {
		results := map[string]sample{}
		order := []bool{false, true}
		if pair%2 == 1 {
			order = []bool{true, false}
		}
		for _, enabled := range order {
			var provider *sdktrace.TracerProvider
			if enabled {
				exporter, err := otlptracehttp.New(context.Background(), otlptracehttp.WithEndpointURL(collector.URL+"/v1/traces"), otlptracehttp.WithCompression(otlptracehttp.NoCompression))
				require.NoError(t, err)
				provider = sdktrace.NewTracerProvider(sdktrace.WithSpanProcessor(spans), sdktrace.WithBatcher(exporter, sdktrace.WithBatchTimeout(100*time.Millisecond)))
				otel.SetTracerProvider(provider)
			} else {
				otel.SetTracerProvider(noop.NewTracerProvider())
			}
			server := &HTTPServer{}
			var summaries *requesttrace.Exporter
			var commitMu sync.Mutex
			var commitDurations, rpcDurations, queueDurations []float64
			var backlogPeak atomic.Int64
			if enabled {
				sink := func(context.Context, *requesttrace.TimingSummary) error { return nil }
				if sqlStack != nil {
					sink = func(ctx context.Context, summary *requesttrace.TimingSummary) error {
						started := time.Now()
						err := sqlStack.ingest(ctx, summary)
						if err == nil {
							commitMu.Lock()
							rpcDurations = append(rpcDurations, float64(time.Since(started))/1e6)
							if summary.HandlerFinishedAtMS != nil {
								queueDurations = append(queueDurations, float64(max(0, started.UnixMilli()-*summary.HandlerFinishedAtMS)))
							}
							commitDurations = append(commitDurations, float64(time.Now().UnixMilli()-summary.StartedAtMS))
							commitMu.Unlock()
						}
						return err
					}
				}
				summaries = requesttrace.NewExporter(sink, func(result string) {
					if result != "queued" {
						return
					}
					stats := summaries.Stats()
					backlog := stats.Accepted - stats.Exported - stats.Failed
					for peak := backlogPeak.Load(); backlog > peak; peak = backlogPeak.Load() {
						if backlogPeak.CompareAndSwap(peak, backlog) {
							break
						}
					}
				})
				server.SetTimingExporter(summaries)
			}
			handler := server.wrapRoute("/v1/perf", executionRoute("/v1/perf", "legacy"), http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				ctx, end := requesttrace.Phase(r.Context(), "relay.plan")
				requesttrace.BindUser(ctx, 7)
				end(nil)
				ctx = requesttrace.WithAttempt(ctx, requesttrace.Attempt{Number: 1, SourceKind: "channel"})
				id := requesttrace.Capture(ctx).ObservationID
				requesttrace.Observation(ctx).Update(func(s *requesttrace.TimingSummary) { s.RootRequestID = id })
				reservation := "reservation-" + id
				requesttrace.RecordAttempt(ctx, requesttrace.TimingAttempt{Number: 1, RequestID: id, ReservationID: &reservation, Outcome: "success"})
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
			serve := func() int {
				response := httptest.NewRecorder()
				handler.ServeHTTP(response, httptest.NewRequest("POST", "/v1/perf", nil))
				return response.Code
			}
			var front *httptest.Server
			if sqlStack != nil {
				front = httptest.NewServer(handler)
				front.Client().Timeout = 5 * time.Second
				front.Client().Transport = &http.Transport{MaxIdleConns: 64, MaxIdleConnsPerHost: 64}
				serve = func() int {
					response, err := front.Client().Post(front.URL+"/v1/perf", "application/json", nil)
					if err != nil {
						return 0
					}
					_, err = io.Copy(io.Discard, response.Body)
					_ = response.Body.Close()
					if err != nil {
						return 0
					}
					return response.StatusCode
				}
			}
			var storedBefore map[string]int64
			if sqlStack != nil {
				storedBefore = sqlStack.counts(t)
			}
			for range warmup {
				require.Equal(t, http.StatusOK, serve())
			}
			runtime.GC()
			var before, after runtime.MemStats
			runtime.ReadMemStats(&before)
			var cpuBefore, cpuAfter syscall.Rusage
			require.NoError(t, syscall.Getrusage(syscall.RUSAGE_SELF, &cpuBefore))
			var mu sync.Mutex
			var failures atomic.Int64
			durations := make([]float64, 0, concurrency*int(duration/(50*time.Millisecond)))
			started := time.Now()
			deadline := started.Add(duration)
			var queries sync.WaitGroup
			queryStop := make(chan struct{})
			var queryDurations []float64
			var queryFailures int64
			if sqlStack != nil {
				queries.Go(func() {
					ticker := time.NewTicker(500 * time.Millisecond)
					defer ticker.Stop()
					for {
						select {
						case <-queryStop:
							return
						case <-ticker.C:
						}
						ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
						start := time.Now()
						_, err := sqlStack.query(ctx)
						cancel()
						queryDurations = append(queryDurations, float64(time.Since(start))/1e6)
						if err != nil {
							queryFailures++
						}
					}
				})
			}
			var workers sync.WaitGroup
			for range concurrency {
				workers.Go(func() {
					for time.Now().Before(deadline) {
						start := time.Now()
						code := serve()
						elapsed := float64(time.Since(start)) / 1e6
						if code != http.StatusOK {
							failures.Add(1)
						}
						mu.Lock()
						durations = append(durations, elapsed)
						mu.Unlock()
					}
				})
			}
			workers.Wait()
			close(queryStop)
			queries.Wait()
			elapsed := time.Since(started)
			runtime.ReadMemStats(&after)
			require.NoError(t, syscall.Getrusage(syscall.RUSAGE_SELF, &cpuAfter))
			entry := sample{Requests: len(durations), DurationSeconds: elapsed.Seconds(),
				AllocatedBytesPerRequest: float64(after.TotalAlloc-before.TotalAlloc) / float64(len(durations)),
				CPUUsPerRequest:          float64(cpu(cpuAfter)-cpu(cpuBefore)) / float64(len(durations)), GCs: after.NumGC - before.NumGC,
				HTTPFailures: failures.Load()}
			// Keep shutdown costs visible without including them in steady-state CPU/allocation.
			if enabled {
				drainStart := time.Now()
				ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
				require.NoError(t, summaries.Close(ctx))
				require.NoError(t, provider.Shutdown(ctx))
				cancel()
				commitMu.Lock()
				entry.PersistenceP95MS = timingP95(commitDurations)
				entry.ExportRPCP95MS = timingP95(rpcDurations)
				entry.QueueWaitP95MS = timingP95(queueDurations)
				entry.SummaryBacklogPeak = backlogPeak.Load()
				commitMu.Unlock()
				entry.DrainMS = float64(time.Since(drainStart)) / 1e6
				var drained runtime.MemStats
				var cpuDrained syscall.Rusage
				runtime.ReadMemStats(&drained)
				require.NoError(t, syscall.Getrusage(syscall.RUSAGE_SELF, &cpuDrained))
				entry.DrainAllocatedBytes = drained.TotalAlloc - after.TotalAlloc
				entry.DrainCPUUs = cpu(cpuDrained) - cpu(cpuAfter)
			}
			entry.P95MS = timingP95(durations)
			entry.QueryP95MS = timingP95(queryDurations)
			entry.Queries, entry.QueryFailures = len(queryDurations), queryFailures
			if queryFailures != 0 {
				gateFailures = append(gateFailures, fmt.Sprintf("pair %d: SQL query failures %d", pair+1, queryFailures))
			}
			name := "disabled"
			if enabled {
				name = "enabled"
				entry.Exported = summaries.Stats().Exported
				if sqlStack != nil {
					for table, n := range sqlStack.counts(t) {
						if n-storedBefore[table] != entry.Exported {
							gateFailures = append(gateFailures, fmt.Sprintf("pair %d: %s persisted %d != exported %d", pair+1, table, n-storedBefore[table], entry.Exported))
						}
					}
				}
				entry.Dropped = summaries.Stats().Dropped
				entry.Failed = summaries.Stats().Failed
				if entry.Exported != int64(entry.Requests+warmup) || entry.Dropped != 0 || entry.Failed != 0 {
					gateFailures = append(gateFailures, fmt.Sprintf("pair %d: summary export mismatch/loss", pair+1))
				}
			}
			if entry.HTTPFailures != 0 {
				gateFailures = append(gateFailures, fmt.Sprintf("pair %d %s: failed HTTP requests", pair+1, name))
			}
			if front != nil {
				front.Client().CloseIdleConnections()
				front.Close()
			}
			results[name] = entry
			t.Logf("pair=%d enabled=%t requests=%d p95_ms=%.3f gcs=%d", pair+1, enabled, entry.Requests, entry.P95MS, entry.GCs)
		}
		overhead := (results["enabled"].P95MS/results["disabled"].P95MS - 1) * 100
		rounds = append(rounds, map[string]any{"pair": pair + 1, "order": order, "results": results, "p95_overhead_percent": overhead})
		if overhead > 5 {
			gateFailures = append(gateFailures, fmt.Sprintf("pair %d: P95 overhead %.4f%% exceeds 5%%", pair+1, overhead))
		}
	}
	if receivedSpans.Load() != spans.count.Load() {
		gateFailures = append(gateFailures, "OTLP receiver count differs from completed sampled spans")
	}
	report := map[string]any{"concurrency": concurrency, "duration_per_mode_seconds": duration.Seconds(), "warmup_per_mode": warmup,
		"fake_upstream_wait_ms": 50, "rounds": rounds, "gate_failures": gateFailures,
		"goos": runtime.GOOS, "goarch": runtime.GOARCH, "go_version": runtime.Version(), "gomaxprocs": runtime.GOMAXPROCS(0),
		"scope": "controlled fake SSE; summary sink succeeds in memory; real local OTLP HTTP receiver; excludes log RPC/SQL",
		"spans": spans.count.Load(), "otlp_received_spans": receivedSpans.Load(), "otlp_bytes": bytesExported.Load()}
	if sqlStack != nil {
		report["scope"] = "real loopback HTTP/SSE, bounded exporter, authenticated Log gRPC, production service/usecase/repository, migrated SQL, real local OTLP receiver; fake upstream; shared process"
		report["sql_driver"] = driver
		report["sql_counts"] = sqlStack.counts(t)
	}
	encoded, err := jsonx.MarshalIndent(report, "", "  ")
	require.NoError(t, err)
	fmt.Println(string(encoded))
	if path := os.Getenv("TIMING_PERF_OUTPUT"); path != "" {
		require.NoError(t, os.WriteFile(path, encoded, 0600))
	}
	require.Empty(t, gateFailures, "controlled P95/export gate; all pairs retained")
}

func timingP95(values []float64) float64 {
	if len(values) == 0 {
		return 0
	}
	sort.Float64s(values)
	return values[len(values)*95/100]
}

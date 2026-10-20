package requesttrace_test

import (
	"context"
	"os"
	"runtime"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"google.golang.org/protobuf/proto"
	"micro-one-api/domain/requesttrace"
	timingtest "micro-one-api/domain/requesttrace/testutil"
	"micro-one-api/pkg/jsonx"
	"micro-one-api/platform/timingdto"
)

func TestMaximumTimingBoundsAndTransport(t *testing.T) {
	s := timingtest.MaximumTiming()
	require.NoError(t, s.Validate())
	encoded, err := jsonx.Marshal(s)
	require.NoError(t, err)
	p := timingdto.ToProto(s)
	wire, err := proto.Marshal(p)
	require.NoError(t, err)
	require.NoError(t, proto.Unmarshal(wire, p))
	roundtrip, err := timingdto.FromProto(p)
	require.NoError(t, err)
	require.Equal(t, s, roundtrip)
	var decoded requesttrace.TimingSummary
	require.NoError(t, jsonx.Unmarshal(encoded, &decoded))
	require.Equal(t, s, &decoded)
	t.Logf("maximum legal summary: JSON=%d protobuf=%d bytes", len(encoded), len(wire))
	for name, change := range map[string]func(*requesttrace.TimingSummary){
		"root":          func(s *requesttrace.TimingSummary) { s.RootRequestID += "x" },
		"compatibility": func(s *requesttrace.TimingSummary) { s.CompatibilityTraceID += "x" },
		"endpoint":      func(s *requesttrace.TimingSummary) { s.Endpoint += "x" },
		"path":          func(s *requesttrace.TimingSummary) { s.ExecutionPath += "x" },
		"attempts":      func(s *requesttrace.TimingSummary) { s.Attempts = append(s.Attempts, s.Attempts[0]) },
		"request":       func(s *requesttrace.TimingSummary) { s.Attempts[0].RequestID += "x" },
		"reservation":   func(s *requesttrace.TimingSummary) { *s.Attempts[0].ReservationID += "x" },
		"model":         func(s *requesttrace.TimingSummary) { s.Attempts[0].UpstreamModelID += "x" },
		"stages":        func(s *requesttrace.TimingSummary) { s.Stages = append(s.Stages, s.Stages[0]) },
		"reasons":       func(s *requesttrace.TimingSummary) { s.MissingReasons = append(s.MissingReasons, "extra") },
		"reason":        func(s *requesttrace.TimingSummary) { s.MissingReasons[0] += "x" },
	} {
		t.Run(name, func(t *testing.T) {
			invalid := s.Clone()
			change(invalid)
			require.Error(t, invalid.Validate())
			_, err := timingdto.FromProto(timingdto.ToProto(invalid))
			require.Error(t, err)
		})
	}
}

func TestMaximumTimingExporterMemory(t *testing.T) {
	if os.Getenv("TIMING_MAXIMUM_MEMORY") != "1" {
		t.Skip("maximum-input memory acceptance requires a dedicated Linux cgroup")
	}
	const budget = 768 << 20
	runtime.GC()
	baseline := timingtest.MemoryPoint(t)
	points := []map[string]uint64{baseline}
	for range 3 {
		func() {
			started, release := make(chan struct{}, requesttrace.ExporterWorkerCount), make(chan struct{})
			var once sync.Once
			unblock := func() { once.Do(func() { close(release) }) }
			exporter := requesttrace.NewExporter(func(ctx context.Context, summary *requesttrace.TimingSummary) error {
				select {
				case started <- struct{}{}:
				default:
				}
				select {
				case <-release:
					// Exercise the real conversion/serialization copies as workers drain.
					_, err := proto.Marshal(timingdto.ToProto(summary))
					return err
				case <-ctx.Done():
					return ctx.Err()
				}
			}, nil)
			defer func() {
				unblock()
				ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
				defer cancel()
				require.NoError(t, exporter.Close(ctx))
			}()
			for range requesttrace.ExporterWorkerCount {
				require.True(t, exporter.Submit(context.Background(), timingtest.MaximumTiming()))
			}
			for range requesttrace.ExporterWorkerCount {
				select {
				case <-started:
				case <-time.After(time.Second):
					t.Fatal("export worker did not start")
				}
			}
			for range requesttrace.ExporterQueueCapacity {
				require.True(t, exporter.Submit(context.Background(), timingtest.MaximumTiming()))
			}
			saturated := timingtest.MemoryPoint(t)
			points = append(points, saturated)
			start := time.Now()
			require.False(t, exporter.Submit(context.Background(), timingtest.MaximumTiming()))
			require.Less(t, time.Since(start), time.Second)
			unblock()
			ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
			defer cancel()
			require.NoError(t, exporter.Close(ctx))
			stats := exporter.Stats()
			require.EqualValues(t, requesttrace.ExporterWorkerCount+requesttrace.ExporterQueueCapacity, stats.Accepted)
			require.Equal(t, stats.Accepted, stats.Exported)
			require.Zero(t, stats.Failed)
			require.EqualValues(t, 1, stats.Dropped)
		}()
		runtime.GC()
		drained := timingtest.MemoryPoint(t)
		points = append(points, drained)
		require.LessOrEqual(t, drained["heap_alloc"], baseline["heap_alloc"]+(16<<20))
	}
	report, err := jsonx.MarshalIndent(map[string]any{"scope": "maximum summary exporter; background context; isolated admission only", "budget_bytes": budget, "points": points}, "", "  ")
	require.NoError(t, err)
	t.Log(string(report))
	if path := os.Getenv("TIMING_MEMORY_OUTPUT"); path != "" {
		require.NoError(t, os.WriteFile(path, report, 0600))
	}
	for _, point := range points {
		require.LessOrEqual(t, point["VmHWM:"], uint64(budget))
		require.LessOrEqual(t, point["memory.peak"], uint64(budget))
	}
}

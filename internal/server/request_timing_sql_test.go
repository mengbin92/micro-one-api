package server

import (
	"context"
	"errors"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"sync/atomic"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/metadata"
	"gorm.io/gorm"
	logv1 "micro-one-api/api/log/v1"
	logtest "micro-one-api/app/log/testutil"
	"micro-one-api/domain/requesttrace"
	"micro-one-api/platform/authz"
	dbtest "micro-one-api/platform/database/testutil"
	"micro-one-api/platform/grpc/xgrpc"
	"micro-one-api/platform/timingdto"
)

type timingSQLStack struct {
	db     *gorm.DB
	client logv1.LogServiceClient
}

func newTimingSQLStack(t *testing.T, driver string) *timingSQLStack {
	t.Helper()
	require.Contains(t, []string{"sqlite", "mysql", "postgres"}, driver)
	t.Setenv("SERVICE_TOKEN", "isolated-timing-legacy")
	t.Setenv("SERVICE_CALLER_TOKENS", `{"relay":"isolated-timing-relay","admin":"isolated-timing-admin"}`)
	db := dbtest.RoutingContextDB(t, driver)
	if driver == "sqlite" {
		require.NoError(t, db.Exec("PRAGMA journal_mode=WAL").Error)
		require.NoError(t, db.Exec("PRAGMA synchronous=FULL").Error)
		sqlDB, err := db.DB()
		require.NoError(t, err)
		sqlDB.SetMaxOpenConns(1)
	}
	if os.Getenv("TIMING_SQL_DIAGNOSTICS") == "1" {
		attachTimingSQLDiagnostics(t, db)
	}
	server := logtest.NewIAMGRPC(db, authz.NewLegacyClient("log"))
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	go func() { _ = server.Server.Serve(listener) }()
	t.Cleanup(func() { server.Server.Stop(); _ = listener.Close() })
	conn, err := grpc.NewClient(listener.Addr().String(), grpc.WithTransportCredentials(insecure.NewCredentials()), grpc.WithStatsHandler(xgrpc.ClientStatsHandler()))
	require.NoError(t, err)
	t.Cleanup(func() { _ = conn.Close() })
	return &timingSQLStack{db: db, client: logv1.NewLogServiceClient(conn)}
}

func (s *timingSQLStack) ingest(ctx context.Context, summary *requesttrace.TimingSummary) error {
	ctx = metadata.AppendToOutgoingContext(ctx, "authorization", "Bearer isolated-timing-relay")
	_, err := s.client.IngestLog(ctx, &logv1.IngestLogRequest{Source: requesttrace.TimingSource, UserId: summary.UserID, RootRequestId: summary.RootRequestID, RequestTiming: timingdto.ToProto(summary)})
	return err
}

func (s *timingSQLStack) query(ctx context.Context) (*logv1.ListRequestTracesResponse, error) {
	ctx = metadata.AppendToOutgoingContext(ctx, "authorization", "Bearer isolated-timing-admin", "x-operator-authorization", "verified-session")
	return s.client.ListRequestTraces(ctx, &logv1.ListRequestTracesRequest{UserId: new(int64(7)), PageSize: 20})
}

func (s *timingSQLStack) counts(t *testing.T) map[string]int64 {
	t.Helper()
	counts := map[string]int64{}
	for _, table := range []string{"logs", "log_ingest_dedupe_claims", "request_trace_attempt_refs"} {
		var n int64
		require.NoError(t, s.db.Table(table).Count(&n).Error)
		counts[table] = n
	}
	return counts
}

func sqlTimingSummary() *requesttrace.TimingSummary {
	id := uuid.NewString()
	reservation := "reservation-" + id
	return &requesttrace.TimingSummary{Version: 1, ObservationID: id, ObservationKind: "http", Protocol: "sse", Endpoint: "/v1/perf", ExecutionPath: "legacy", UserID: 7, RootRequestID: id, StartedAtMS: time.Now().UnixMilli(), Outcome: "success", CollectionState: "not_recorded", Attempts: []requesttrace.TimingAttempt{{Number: 1, RequestID: id, ReservationID: &reservation, Outcome: "success"}}}
}

func TestRequestTimingSQLFailureRecovery(t *testing.T) {
	for _, driver := range []string{"sqlite", "mysql", "postgres"} {
		t.Run(driver, func(t *testing.T) {
			s := newTimingSQLStack(t, driver)
			var fail atomic.Bool
			require.NoError(t, s.db.Callback().Create().After("gorm:create").Register("timing_storage_failure", func(tx *gorm.DB) {
				if fail.Load() && tx.Statement.Table == "request_trace_attempt_refs" {
					tx.AddError(errors.New("isolated association storage failure"))
				}
			}))
			exporter := requesttrace.NewExporter(s.ingest, nil)
			t.Cleanup(func() {
				ctx, cancel := context.WithTimeout(context.Background(), time.Second)
				defer cancel()
				_ = exporter.Close(ctx)
			})
			before := s.counts(t)
			fail.Store(true)
			require.True(t, exporter.Submit(context.Background(), sqlTimingSummary()))
			require.Eventually(t, func() bool { return exporter.Stats().Failed == 1 }, 5*time.Second, time.Millisecond)
			require.Equal(t, before, s.counts(t), "failed association must roll back summary, claim and refs")
			fail.Store(false)
			summary := sqlTimingSummary()
			require.True(t, exporter.Submit(context.Background(), summary))
			require.Eventually(t, func() bool { return exporter.Stats().Exported == 1 }, 5*time.Second, time.Millisecond)
			require.NoError(t, s.ingest(context.Background(), summary), "replay uses SQL dedupe")
			after := s.counts(t)
			for table, n := range before {
				require.Equal(t, n+1, after[table], table)
			}
			result, err := s.query(context.Background())
			require.NoError(t, err)
			require.Len(t, result.Items, 1)
			require.Equal(t, summary.ObservationID, result.Items[0].ObservationId)
			require.Equal(t, summary.Attempts[0].ReservationID, result.Items[0].Attempts[0].ReservationId)
			t.Logf("storage failure/recovery: stats=%+v counts=%v", exporter.Stats(), after)
		})
	}
}

func TestRequestTimingSQLBackpressureAndLostAck(t *testing.T) {
	s := newTimingSQLStack(t, "sqlite")
	stalled := make(chan struct{}, 1)
	release := make(chan struct{})
	var released atomic.Bool
	unblock := func() {
		if released.CompareAndSwap(false, true) {
			close(release)
		}
	}
	t.Cleanup(unblock)
	require.NoError(t, s.db.Callback().Create().Before("gorm:create").Register("timing_storage_stall", func(tx *gorm.DB) {
		if tx.Statement.Table != "logs" {
			return
		}
		select {
		case stalled <- struct{}{}:
		default:
		}
		select {
		case <-release:
		case <-tx.Statement.Context.Done():
			tx.AddError(tx.Statement.Context.Err())
		}
	}))
	entered := make(chan struct{}, requesttrace.ExporterWorkerCount)
	var loseAck atomic.Bool
	loseAck.Store(true)
	exporter := requesttrace.NewExporter(func(ctx context.Context, summary *requesttrace.TimingSummary) error {
		select {
		case entered <- struct{}{}:
		default:
		}
		err := s.ingest(ctx, summary)
		if err == nil && loseAck.CompareAndSwap(true, false) {
			return errors.New("isolated lost RPC acknowledgement")
		}
		return err
	}, nil)
	t.Cleanup(func() {
		unblock()
		ctx, cancel := context.WithTimeout(context.Background(), time.Second)
		defer cancel()
		_ = exporter.Close(ctx)
	})
	require.True(t, exporter.Submit(context.Background(), sqlTimingSummary()))
	select {
	case <-stalled:
	case <-time.After(5 * time.Second):
		t.Fatal("SQL writer never reached")
	}
	for range requesttrace.ExporterWorkerCount - 1 {
		require.True(t, exporter.Submit(context.Background(), sqlTimingSummary()))
	}
	for range requesttrace.ExporterWorkerCount {
		select {
		case <-entered:
		case <-time.After(time.Second):
			t.Fatal("export worker did not start")
		}
	}
	start := time.Now()
	total := requesttrace.ExporterWorkerCount + requesttrace.ExporterQueueCapacity + 81
	for range total - requesttrace.ExporterWorkerCount {
		exporter.Submit(context.Background(), sqlTimingSummary())
	}
	elapsed := time.Since(start)
	stats := exporter.Stats()
	require.Less(t, elapsed, time.Second, "stalled SQL must not block producer")
	require.EqualValues(t, requesttrace.ExporterWorkerCount+requesttrace.ExporterQueueCapacity, stats.Accepted, "workers plus fixed queue")
	require.EqualValues(t, total-requesttrace.ExporterWorkerCount-requesttrace.ExporterQueueCapacity, stats.Dropped)
	server := &HTTPServer{}
	server.SetTimingExporter(exporter)
	response := httptest.NewRecorder()
	handler := server.observeRequestTiming(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { _, _ = w.Write([]byte("ok")) }), "/v1/perf", "legacy")
	start = time.Now()
	handler.ServeHTTP(response, httptest.NewRequest("POST", "/v1/perf", nil))
	require.Less(t, time.Since(start), time.Second)
	require.Equal(t, http.StatusOK, response.Code)
	require.Equal(t, "ok", response.Body.String())
	require.EqualValues(t, stats.Dropped+1, exporter.Stats().Dropped, "saturated telemetry must not fail HTTP output")
	unblock()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	require.NoError(t, exporter.Close(ctx))
	stats = exporter.Stats()
	require.Zero(t, stats.Failed)
	require.Equal(t, stats.Accepted, stats.Exported)
	for table, n := range s.counts(t) {
		require.Equal(t, stats.Exported, n, table+": lost ACK retry must not duplicate")
	}
	t.Logf("backpressure recovered: submit_ms=%.3f stats=%+v", float64(elapsed)/1e6, stats)
}

func TestRequestTimingSQLOverhead(t *testing.T) {
	if os.Getenv("TIMING_SQL_ACCEPTANCE") != "1" {
		t.Skip("real SQL timing acceptance is opt-in")
	}
	driver := os.Getenv("TIMING_SQL_DRIVER")
	if driver == "" {
		driver = "sqlite"
	}
	s := newTimingSQLStack(t, driver)
	runRequestTimingOverhead(t, s, driver)
}

package server

import (
	"context"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"runtime"
	"testing"
	"time"

	coderws "github.com/coder/websocket"
	khttp "github.com/go-kratos/kratos/v3/transport/http"
	"github.com/stretchr/testify/require"
	"go.opentelemetry.io/otel"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
	billingv1 "micro-one-api/api/billing/v1"
	"micro-one-api/domain/upstream/evidence"
	relayprovider "micro-one-api/domain/upstream/provider"
	relaybiz "micro-one-api/internal/biz"
	relaydata "micro-one-api/internal/data"
	"micro-one-api/pkg/jsonx"
	"micro-one-api/platform/grpc/xgrpc"
)

type timingBillingServer struct {
	billingv1.UnimplementedBillingServiceServer
	billing *rawBillingClient
}

func (s timingBillingServer) GetRoutingCapabilities(ctx context.Context, req *billingv1.GetRoutingCapabilitiesRequest) (*billingv1.GetRoutingCapabilitiesResponse, error) {
	return s.billing.GetRoutingCapabilities(ctx, req)
}

func (s timingBillingServer) ReserveQuota(ctx context.Context, req *billingv1.ReserveQuotaRequest) (*billingv1.ReserveQuotaResponse, error) {
	return s.billing.ReserveQuota(ctx, req)
}
func (s timingBillingServer) CommitQuota(ctx context.Context, req *billingv1.CommitQuotaRequest) (*billingv1.CommitQuotaResponse, error) {
	return s.billing.CommitQuota(ctx, req)
}
func (s timingBillingServer) ReleaseQuota(ctx context.Context, req *billingv1.ReleaseQuotaRequest) (*billingv1.ReleaseQuotaResponse, error) {
	return s.billing.ReleaseQuota(ctx, req)
}
func (s timingBillingServer) GetAccountSnapshot(ctx context.Context, req *billingv1.GetAccountSnapshotRequest) (*billingv1.GetAccountSnapshotResponse, error) {
	return s.billing.GetAccountSnapshot(ctx, req)
}

type timingSessionDialer struct{ sessions chan *wsTimingSession }

func (d timingSessionDialer) Dial(ctx context.Context, url string, headers http.Header) (openAIWSFrameConn, int, http.Header, error) {
	select {
	case d.sessions <- wsTimings(ctx):
	default:
	}
	return newCoderWSUpstreamDialer().Dial(ctx, url, headers)
}

func TestWSEvidenceHistoryLimitRequiresDurableDelivery(t *testing.T) {
	for _, durable := range []bool{false, true} {
		t.Run(fmt.Sprintf("durable=%t", durable), func(t *testing.T) {
			client, upstream := newFakeFrameConn(evidence.MaxEvents+1), newFakeFrameConn(evidence.MaxEvents+1)
			for turn := range evidence.MaxEvents + 1 {
				upstream.in <- fakeFrame{msgType: coderws.MessageText, payload: []byte(fmt.Sprintf(
					`{"type":"response.completed","response":{"id":"history_%d","status":"completed","usage":{"input_tokens":1,"output_tokens":1}}}`, turn))}
			}
			close(upstream.in)
			ctx, cancel := context.WithTimeout(context.Background(), time.Second)
			defer cancel()
			completed := 0
			_, exit := relayOpenAIWSFrames(ctx, client, upstream, []byte(`{"type":"response.create","model":"test"}`), openAIWSRelayOptions{
				durableHistoryLimit: durable,
				durableRecord:       func(evidence.Record) error { return nil },
				onTurnComplete:      func(openAIWSTurnResult) { completed++ },
			})
			require.NotNil(t, exit)
			if durable {
				require.Equal(t, "durable_history_limit", exit.stage)
				require.Equal(t, evidence.MaxEvents, completed)
			} else {
				require.Equal(t, "read_upstream", exit.stage)
				require.Equal(t, evidence.MaxEvents+1, completed)
			}
		})
	}
}

func TestWSTimingMultiTurnSocket(t *testing.T) {
	t.Setenv("PROVIDER_DISABLE_SSRF_CHECK", "true")
	turns, pause := 128, time.Duration(0)
	if os.Getenv("TIMING_WS_SOAK") == "1" {
		turns, pause = 1200, 50*time.Millisecond
	}
	previous := otel.GetTracerProvider()
	provider := sdktrace.NewTracerProvider(sdktrace.WithSpanProcessor(&perfSpanCounter{}))
	otel.SetTracerProvider(provider)
	defer otel.SetTracerProvider(previous)
	defer func() { require.NoError(t, provider.Shutdown(context.Background())) }()
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, err := coderws.Accept(w, r, nil)
		if err != nil {
			t.Errorf("upstream accept: %v", err)
			return
		}
		defer conn.CloseNow()
		for turn := 0; ; turn++ {
			_, payload, err := conn.Read(ctx)
			if err != nil {
				return
			}
			var create struct{ Type, Model string }
			if err := jsonx.Unmarshal(payload, &create); err != nil || create.Type != "response.create" || create.Model != "gpt-5" {
				t.Errorf("unexpected upstream create: %s (%v)", payload, err)
				return
			}
			if pause > 0 {
				time.Sleep(pause)
			}
			id := fmt.Sprintf("resp_timing_%d", turn)
			for _, frame := range []string{
				fmt.Sprintf(`{"type":"response.created","response":{"id":%q,"status":"in_progress"}}`, id),
				fmt.Sprintf(`{"type":"response.output_text.delta","response_id":%q,"delta":"hello"}`, id),
				fmt.Sprintf(`{"type":"response.completed","response":{"id":%q,"status":"completed","usage":{"input_tokens":12,"output_tokens":8,"input_tokens_details":{"cached_tokens":3}}}}`, id),
			} {
				if err := conn.Write(ctx, coderws.MessageText, []byte(frame)); err != nil {
					t.Errorf("upstream write: %v", err)
					return
				}
			}
		}
	}))
	defer upstream.Close()
	channel := rawChannelClient{baseURL: upstream.URL + "/v1", key: "sk-upstream", getModels: "gpt-5"}
	billing := &rawBillingClient{}
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	rpc := grpc.NewServer()
	billingv1.RegisterBillingServiceServer(rpc, timingBillingServer{billing: billing})
	go func() {
		if err := rpc.Serve(listener); err != nil {
			t.Errorf("billing RPC server: %v", err)
		}
	}()
	defer rpc.Stop()
	client, err := grpc.NewClient(listener.Addr().String(), grpc.WithTransportCredentials(insecure.NewCredentials()), grpc.WithStatsHandler(xgrpc.ClientStatsHandler()))
	require.NoError(t, err)
	defer client.Close()
	uc := relaybiz.NewRelayUsecase(relaydata.NewIdentityAdapter(rawIdentityClient{}), relaydata.NewChannelAdapter(channel), nil,
		&relaybiz.RetryPolicy{MaxAttempts: 1})
	server := NewHTTPServer(rawIdentityClient{}, channel, billingv1.NewBillingServiceClient(client), relayprovider.NewProviderFactory(time.Second), uc)
	exporter, summaries := timingSink(t)
	server.SetTimingExporter(exporter)
	server.SetOpenAIWSTimeouts(time.Second, 5*time.Second, 3*time.Second, 3*time.Second)
	server.SetOpenAIWSConnPool()
	defer server.wsPool.Close()
	sessions := make(chan *wsTimingSession, 1)
	server.wsPool.dialer = timingSessionDialer{sessions: sessions}
	srv := khttp.NewServer(khttp.Timeout(0))
	server.RegisterRoutes(srv)
	done := make(chan struct{})
	gateway := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		defer close(done)
		srv.ServeHTTP(w, r)
	}))
	defer gateway.Close()
	conn, _, err := coderws.Dial(ctx, wsURL(gateway.URL)+"/v1/responses", &coderws.DialOptions{
		HTTPHeader: http.Header{"Authorization": []string{"Bearer user-token"}},
	})
	require.NoError(t, err)
	defer conn.CloseNow()
	started := time.Now()
	observations, traces, reservations := map[string]bool{}, map[string]bool{}, map[string]string{}
	var connectionID string
	var session *wsTimingSession
	var heapAt128, heapAtEnd uint64
	for turn := range turns {
		require.NoError(t, conn.Write(ctx, coderws.MessageText, []byte(`{"type":"response.create","model":"gpt-5","input":"hello"}`)))
		for {
			_, payload, err := conn.Read(ctx)
			require.NoError(t, err, "client read turn %d", turn)
			var frame struct{ Type string }
			require.NoError(t, jsonx.Unmarshal(payload, &frame))
			if frame.Type == "response.completed" {
				break
			}
		}
		summary := awaitTiming(t, summaries)
		if turn == 0 {
			connectionID = summary.ConnectionID
			require.NotEmpty(t, connectionID)
			session = <-sessions
			require.NotNil(t, session)
		}
		require.NotEmpty(t, summary.ObservationID)
		require.NotEmpty(t, summary.OTelTraceID)
		require.False(t, observations[summary.ObservationID], "each turn needs its own observation")
		require.False(t, traces[summary.OTelTraceID], "each turn needs its own trace")
		require.NotEmpty(t, summary.RootRequestID)
		require.NotContains(t, reservations, summary.RootRequestID, "each turn needs its own billing root")
		require.Len(t, summary.Attempts, 1)
		require.NotNil(t, summary.Attempts[0].ReservationID)
		require.Equal(t, "success", summary.Attempts[0].Outcome)
		require.NotNil(t, summary.SettlementSnapshot)
		require.Equal(t, "committed", summary.SettlementSnapshot.Status)
		observations[summary.ObservationID], traces[summary.OTelTraceID] = true, true
		reservations[summary.RootRequestID] = *summary.Attempts[0].ReservationID
		require.Equal(t, connectionID, summary.ConnectionID)
		require.Equal(t, "success", summary.Outcome)
		require.True(t, summary.OutputComplete)
		require.NotNil(t, summary.FirstContentMS)
		require.NotNil(t, summary.TurnDurationMS)
		phases := map[string]bool{}
		for _, stage := range summary.Stages {
			phases[stage.Phase] = true
		}
		require.True(t, phases["quota.reserve"] && phases["quota.commit"] && phases["response.stream"])
		for _, phase := range []string{"ws.accept", "ws.first_frame", "ws.upstream.dial"} {
			require.Equal(t, turn == 0, phases[phase], "%s belongs only to the initial turn", phase)
		}
		session.mu.Lock()
		pending, indexed, retained := len(session.turns), len(session.byID), len(session.completed)
		session.mu.Unlock()
		require.Zero(t, pending, "finished turns must be freed while the socket is open")
		require.Zero(t, indexed)
		require.LessOrEqual(t, retained, 64, "completed-response dedupe history must stay bounded")
		if turn == 127 || turn == turns-1 {
			runtime.GC()
			var memory runtime.MemStats
			runtime.ReadMemStats(&memory)
			if turn == 127 {
				heapAt128 = memory.HeapAlloc
			}
			heapAtEnd = memory.HeapAlloc
		}
	}
	require.NoError(t, conn.Close(coderws.StatusNormalClosure, "finished"))
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("gateway did not release the closed socket")
	}
	require.NoError(t, exporter.Close(ctx))
	select {
	case <-summaries:
		t.Fatal("socket close exported a duplicate completed turn")
	default:
	}
	require.Len(t, billing.reserveRequests, turns)
	require.Len(t, billing.commitRequests, turns)
	require.Zero(t, billing.releases)
	for turn, commit := range billing.commitRequests {
		reserve := billing.reserveRequests[turn]
		require.Equal(t, fmt.Sprintf("reservation-%d", turn+1), reservations[reserve.RootRequestId])
		require.Equal(t, reservations[reserve.RootRequestId], commit.ReservationId)
		require.EqualValues(t, 12, commit.PromptTokens)
		require.EqualValues(t, 8, commit.CompletionTokens)
		require.EqualValues(t, 3, commit.CacheReadTokens)
	}
	require.EqualValues(t, turns, exporter.Stats().Exported)
	require.Zero(t, exporter.Stats().Dropped)
	require.Zero(t, exporter.Stats().Failed)
	session.mu.Lock()
	require.Empty(t, session.turns)
	require.Empty(t, session.byID)
	session.mu.Unlock()
	report := map[string]any{"turns": turns, "duration_seconds": time.Since(started).Seconds(), "exported": exporter.Stats().Exported,
		"billing_commits": billing.commits, "completed_history_limit": 64, "heap_after_gc_at_128": heapAt128, "heap_after_gc_at_end": heapAtEnd,
		"heap_scope": "includes retained fake billing requests, assertion maps and sticky routes; no production memory claim"}
	encoded, err := jsonx.MarshalIndent(report, "", "  ")
	require.NoError(t, err)
	t.Log(string(encoded))
	if path := os.Getenv("TIMING_WS_OUTPUT"); path != "" {
		require.NoError(t, os.WriteFile(path, encoded, 0600))
	}
}

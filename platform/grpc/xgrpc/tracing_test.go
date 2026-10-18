package xgrpc

import (
	"context"
	"net"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"go.opentelemetry.io/otel"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/sdk/trace/tracetest"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/stats"
	"google.golang.org/grpc/status"
	"google.golang.org/grpc/test/bufconn"
	"google.golang.org/protobuf/proto"
	logv1 "micro-one-api/api/log/v1"
	relayv1 "micro-one-api/api/relay/v1"
	"micro-one-api/domain/requesttrace"
)

type timedRelayServer struct {
	relayv1.UnimplementedRelayServiceServer
	calls atomic.Int64
}

func (s *timedRelayServer) ChatCompletion(ctx context.Context, req *relayv1.ChatCompletionRequest) (*relayv1.ChatCompletionResponse, error) {
	defer HandlerTiming(ctx)()
	s.calls.Add(1)
	if req.Model == "encode-fail" {
		return &relayv1.ChatCompletionResponse{Model: string([]byte{255})}, nil
	}
	return &relayv1.ChatCompletionResponse{Model: "test"}, nil
}

type timedLogServer struct {
	logv1.UnimplementedLogServiceServer
}

func (timedLogServer) IngestLog(context.Context, *logv1.IngestLogRequest) (*logv1.IngestLogResponse, error) {
	return &logv1.IngestLogResponse{Id: 1}, nil
}

type malformedCodec struct{}

func (malformedCodec) Name() string                { return "proto" }
func (malformedCodec) Marshal(any) ([]byte, error) { return []byte{255, 255}, nil }
func (malformedCodec) Unmarshal(data []byte, value any) error {
	return proto.Unmarshal(data, value.(proto.Message))
}

func TestGRPCTransportSummariesIncludeDecodeRejectionAndEncodingFailure(t *testing.T) {
	t.Setenv("SERVICE_TOKEN", "timing-token")
	summaries := make(chan *requesttrace.TimingSummary, 8)
	handler := &timedRelayServer{}
	srv := grpc.NewServer(grpc.StatsHandler(ServerStatsHandler(func(_ context.Context, s *requesttrace.TimingSummary) bool { summaries <- s; return true })), grpc.UnaryInterceptor(ServiceTokenUnaryInterceptor("timing-token")))
	relayv1.RegisterRelayServiceServer(srv, handler)
	lis := bufconn.Listen(1 << 20)
	go func() { _ = srv.Serve(lis) }()
	defer srv.Stop()
	conn, err := grpc.NewClient("passthrough:///relay-timing", grpc.WithTransportCredentials(insecure.NewCredentials()), grpc.WithContextDialer(func(ctx context.Context, _ string) (net.Conn, error) { return lis.DialContext(ctx) }))
	require.NoError(t, err)
	defer conn.Close()
	client := relayv1.NewRelayServiceClient(conn)
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	auth := metadata.AppendToOutgoingContext(ctx, "authorization", "Bearer timing-token")
	_, err = client.ChatCompletion(auth, &relayv1.ChatCompletionRequest{Model: "test"})
	require.NoError(t, err)
	first := <-summaries
	require.NoError(t, first.Validate())
	require.NotNil(t, first.RPCDurationMS)
	require.NotNil(t, first.HandlerDurationMS)
	require.Nil(t, first.OutputDurationMS)
	_, err = client.ChatCompletion(ctx, &relayv1.ChatCompletionRequest{Model: "test"})
	require.Equal(t, codes.Unauthenticated, status.Code(err))
	rejected := <-summaries
	require.Nil(t, rejected.HandlerDurationMS)
	require.Nil(t, rejected.FirstContentMS)
	_, err = client.ChatCompletion(auth, &relayv1.ChatCompletionRequest{Model: "bad"}, grpc.ForceCodec(malformedCodec{}))
	require.Error(t, err)
	malformed := <-summaries
	require.Nil(t, malformed.HandlerDurationMS)
	require.NotNil(t, malformed.RPCDurationMS)
	_, err = client.ChatCompletion(auth, &relayv1.ChatCompletionRequest{Model: "encode-fail"})
	require.Error(t, err)
	encoding := <-summaries
	require.Equal(t, "failed", encoding.Outcome)
	require.Nil(t, encoding.OutputDurationMS)
	require.NotNil(t, encoding.HandlerDurationMS)
	require.EqualValues(t, 2, handler.calls.Load())
	require.NotEqual(t, first.ObservationID, rejected.ObservationID)
}

// Hold the server's transport End after its response is already readable.
type gatedServerEnd struct {
	stats.Handler
	release <-chan struct{}
}

func (h gatedServerEnd) HandleRPC(ctx context.Context, event stats.RPCStats) {
	if _, ok := event.(*stats.End); ok {
		<-h.release
	}
	h.Handler.HandleRPC(ctx, event)
}

func TestGRPCStatsPropagateTrustedW3CParentExactlyOnce(t *testing.T) {
	t.Setenv("SERVICE_TOKEN", "timing-token")
	spans := tracetest.NewSpanRecorder()
	provider := sdktrace.NewTracerProvider(sdktrace.WithSpanProcessor(spans))
	previous := otel.GetTracerProvider()
	otel.SetTracerProvider(provider)
	defer otel.SetTracerProvider(previous)
	endCtx, unblock := context.WithCancel(context.Background())
	srv := grpc.NewServer(grpc.StatsHandler(gatedServerEnd{ServerStatsHandler(nil), endCtx.Done()}), grpc.UnaryInterceptor(ServiceTokenUnaryInterceptor("timing-token")))
	logv1.RegisterLogServiceServer(srv, timedLogServer{})
	lis := bufconn.Listen(1 << 20)
	go func() { _ = srv.Serve(lis) }()
	defer func() { unblock(); srv.Stop() }()
	conn, err := grpc.NewClient("passthrough:///log-timing", grpc.WithTransportCredentials(insecure.NewCredentials()), grpc.WithStatsHandler(ClientStatsHandler()), grpc.WithContextDialer(func(ctx context.Context, _ string) (net.Conn, error) { return lis.DialContext(ctx) }))
	require.NoError(t, err)
	defer conn.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	ctx, root := otel.Tracer("test").Start(ctx, "request")
	ctx = metadata.AppendToOutgoingContext(ctx, "authorization", "Bearer timing-token")
	_, err = logv1.NewLogServiceClient(conn).IngestLog(ctx, &logv1.IngestLogRequest{})
	require.NoError(t, err)
	root.End()
	unblock()
	require.Eventually(t, func() bool {
		for _, span := range spans.Ended() {
			if span.Name() == "/api.log.v1.LogService/IngestLog" && span.SpanKind().String() == "server" {
				return true
			}
		}
		return false
	}, time.Second, time.Millisecond, "client completion can precede the server transport End")
	var client, server sdktrace.ReadOnlySpan
	for _, span := range spans.Ended() {
		if span.Name() == "/api.log.v1.LogService/IngestLog" {
			if span.SpanKind().String() == "client" {
				require.Nil(t, client)
				client = span
			} else {
				require.Nil(t, server)
				server = span
			}
		}
	}
	require.NotNil(t, client)
	require.NotNil(t, server)
	require.Equal(t, client.SpanContext().TraceID(), server.SpanContext().TraceID())
	require.Equal(t, client.SpanContext().SpanID(), server.Parent().SpanID())
	require.NotEqual(t, client.SpanContext().SpanID(), server.SpanContext().SpanID())
}

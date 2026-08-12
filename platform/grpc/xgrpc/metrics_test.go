package xgrpc

import (
	"context"
	"errors"
	"testing"

	"micro-one-api/platform/metrics"
	"micro-one-api/platform/tracing"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/testutil"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"
)

// seriesCount returns how many series of the named metric family carry
// exactly the given label values. testutil.CollectAndCount filters by metric
// name only, so we gather the default registry directly to assert on label
// values (histograms do not expose a count accessor on the Observer).
func seriesCount(t *testing.T, familyName string, labels map[string]string) int {
	t.Helper()
	mfs, err := prometheus.DefaultGatherer.Gather()
	require.NoError(t, err)
	for _, mf := range mfs {
		if mf.GetName() != familyName {
			continue
		}
		n := 0
		for _, m := range mf.GetMetric() {
			if len(m.GetLabel()) != len(labels) {
				continue
			}
			match := true
			for _, lp := range m.GetLabel() {
				if want, ok := labels[lp.GetName()]; !ok || want != lp.GetValue() {
					match = false
					break
				}
			}
			if match {
				n++
			}
		}
		return n
	}
	return 0
}

func TestWithTraceID_NoTraceID_ReturnsContextUnchanged(t *testing.T) {
	ctx := context.Background()
	got := WithTraceID(ctx)
	assert.Empty(t, xtrace.ExtractTraceID(got), "no trace ID in ctx must propagate nothing")
}

func TestWithTraceID_SetsOutgoingMetadata(t *testing.T) {
	ctx := xtrace.WithTraceID(context.Background(), "abc123")
	got := WithTraceID(ctx)

	md, ok := metadata.FromOutgoingContext(got)
	require.True(t, ok, "outgoing metadata must be present")
	assert.Equal(t, []string{"abc123"}, md.Get(traceIDKey))
}

func TestWithTraceID_PreservesExistingMetadata(t *testing.T) {
	ctx := metadata.NewOutgoingContext(context.Background(), metadata.Pairs("x-tenant", "t1"))
	ctx = xtrace.WithTraceID(ctx, "abc123")

	got := WithTraceID(ctx)
	md, ok := metadata.FromOutgoingContext(got)
	require.True(t, ok)
	assert.Equal(t, []string{"t1"}, md.Get("x-tenant"), "pre-existing metadata must survive")
	assert.Equal(t, []string{"abc123"}, md.Get(traceIDKey))
}

func TestWithTraceID_DoesNotMutateCallerMetadata(t *testing.T) {
	// Copy semantics: the interceptor must not write through into the caller's
	// metadata map (gRPC metadata is mutable).
	ctx := metadata.NewOutgoingContext(context.Background(), metadata.Pairs("x-keep", "v"))
	original, _ := metadata.FromOutgoingContext(ctx)

	ctx = xtrace.WithTraceID(ctx, "id9")
	WithTraceID(ctx)

	assert.Equal(t, []string(nil), original.Get(traceIDKey),
		"caller metadata must not be mutated by WithTraceID")
}

func TestTraceIDFromIncoming_NoMetadata_ReturnsSameContext(t *testing.T) {
	ctx := context.Background()
	got := TraceIDFromIncoming(ctx)
	assert.Empty(t, xtrace.ExtractTraceID(got), "no incoming metadata must not inject a trace ID")
}

func TestTraceIDFromIncoming_EmptyValue_ReturnsSameContext(t *testing.T) {
	ctx := metadata.NewIncomingContext(context.Background(), metadata.Pairs(traceIDKey, ""))
	got := TraceIDFromIncoming(ctx)
	assert.Equal(t, "", xtrace.ExtractTraceID(got))
}

func TestTraceIDFromIncoming_ExtractsIntoContext(t *testing.T) {
	ctx := metadata.NewIncomingContext(context.Background(), metadata.Pairs(traceIDKey, "trace-42"))
	got := TraceIDFromIncoming(ctx)
	assert.Equal(t, "trace-42", xtrace.ExtractTraceID(got))
}

func TestTraceIDFromIncoming_TakesFirstValue(t *testing.T) {
	ctx := metadata.NewIncomingContext(context.Background(),
		metadata.Pairs(traceIDKey, "first", traceIDKey, "second"))
	got := TraceIDFromIncoming(ctx)
	assert.Equal(t, "first", xtrace.ExtractTraceID(got))
}

func TestUnaryClientInterceptor_PropagatesTraceIDToInvoker(t *testing.T) {
	interceptor := UnaryClientInterceptor()
	var gotTraceID string
	invoker := func(ctx context.Context, method string, req, reply any, cc *grpc.ClientConn, opts ...grpc.CallOption) error {
		gotTraceID = xtrace.ExtractTraceID(ctx)
		md, _ := metadata.FromOutgoingContext(ctx)
		assert.Equal(t, []string{"tid-1"}, md.Get(traceIDKey))
		return nil
	}

	ctx := xtrace.WithTraceID(context.Background(), "tid-1")
	err := interceptor(ctx, "/svc.M/Get", nil, nil, nil, invoker)
	require.NoError(t, err)
	assert.Equal(t, "tid-1", gotTraceID)
}

func TestUnaryServerInterceptor_ExtractsTraceIDBeforeHandler(t *testing.T) {
	interceptor := UnaryServerInterceptor()
	var handlerGot string
	handler := func(ctx context.Context, req any) (any, error) {
		handlerGot = xtrace.ExtractTraceID(ctx)
		return "ok", nil
	}

	ctx := metadata.NewIncomingContext(context.Background(), metadata.Pairs(traceIDKey, "sid-9"))
	resp, err := interceptor(ctx, nil, &grpc.UnaryServerInfo{FullMethod: "/svc.M/Get"}, handler)
	require.NoError(t, err)
	assert.Equal(t, "ok", resp)
	assert.Equal(t, "sid-9", handlerGot)
}

func TestMetricsUnaryServerInterceptor_RecordsSuccess(t *testing.T) {
	metrics.GRPCRequestTotal.Reset()
	metrics.GRPCRequestDuration.Reset()

	interceptor := MetricsUnaryServerInterceptor("relay-gateway")
	handler := func(ctx context.Context, req any) (any, error) { return "resp", nil }

	resp, err := interceptor(context.Background(), nil,
		&grpc.UnaryServerInfo{FullMethod: "/v1.Svc/Get"}, handler)
	require.NoError(t, err)
	assert.Equal(t, "resp", resp)

	assert.Equal(t, float64(1), testutil.ToFloat64(
		metrics.GRPCRequestTotal.WithLabelValues("relay-gateway", "/v1.Svc/Get", codes.OK.String())),
		"successful call must increment the counter with status code OK")
	assert.Equal(t, 1, seriesCount(t, "micro_one_api_grpc_request_duration_seconds",
		map[string]string{"service": "relay-gateway", "method": "/v1.Svc/Get"}),
		"duration must be observed exactly once")
}

func TestMetricsUnaryServerInterceptor_RecordsErrorStatus(t *testing.T) {
	metrics.GRPCRequestTotal.Reset()
	metrics.GRPCRequestDuration.Reset()

	interceptor := MetricsUnaryServerInterceptor("identity-service")
	handler := func(ctx context.Context, req any) (any, error) {
		return nil, status.Error(codes.PermissionDenied, "no")
	}

	_, err := interceptor(context.Background(), nil,
		&grpc.UnaryServerInfo{FullMethod: "/v1.Auth/Check"}, handler)
	require.Error(t, err)

	assert.Equal(t, float64(1), testutil.ToFloat64(
		metrics.GRPCRequestTotal.WithLabelValues("identity-service", "/v1.Auth/Check", codes.PermissionDenied.String())),
		"failed call must carry the mapped status code label")
	// The failed call must still observe duration (metrics fire before the
	// error is returned to the caller).
	assert.Equal(t, 1, seriesCount(t, "micro_one_api_grpc_request_duration_seconds",
		map[string]string{"service": "identity-service", "method": "/v1.Auth/Check"}))
}

func TestMetricsUnaryServerInterceptor_PlainErrorMapsToUnknown(t *testing.T) {
	metrics.GRPCRequestTotal.Reset()

	interceptor := MetricsUnaryServerInterceptor("relay-gateway")
	handler := func(ctx context.Context, req any) (any, error) {
		return nil, errors.New("plain error")
	}

	_, err := interceptor(context.Background(), nil,
		&grpc.UnaryServerInfo{FullMethod: "/v1.Svc/Call"}, handler)
	require.Error(t, err)
	assert.Equal(t, float64(1), testutil.ToFloat64(
		metrics.GRPCRequestTotal.WithLabelValues("relay-gateway", "/v1.Svc/Call", codes.Unknown.String())),
		"non-status errors must be labelled Unknown")
}

func TestUnaryClientMetricsInterceptor_ObservesDependency(t *testing.T) {
	require.NotNil(t, metrics.ServiceDependencyLatency, "dependency latency must be registered")

	interceptor := UnaryClientMetricsInterceptor("billing-service")
	invoker := func(ctx context.Context, method string, req, reply any, cc *grpc.ClientConn, opts ...grpc.CallOption) error {
		return nil
	}

	err := interceptor(context.Background(), "/v1.Billing/Charge", nil, nil, nil, invoker)
	require.NoError(t, err)

	assert.Equal(t, 1, seriesCount(t, "micro_one_api_dependency_grpc_latency_seconds",
		map[string]string{"service": "billing-service", "method": "/v1.Billing/Charge", "status": codes.OK.String()}),
		"successful dependency call must observe latency with status OK")
}

func TestUnaryClientMetricsInterceptor_ErrorCodeLabel(t *testing.T) {
	require.NotNil(t, metrics.ServiceDependencyLatency)

	interceptor := UnaryClientMetricsInterceptor("channel-service")
	invoker := func(ctx context.Context, method string, req, reply any, cc *grpc.ClientConn, opts ...grpc.CallOption) error {
		return status.Error(codes.Unavailable, "down")
	}

	err := interceptor(context.Background(), "/v1.Channel/Get", nil, nil, nil, invoker)
	require.Error(t, err)

	assert.Equal(t, 1, seriesCount(t, "micro_one_api_dependency_grpc_latency_seconds",
		map[string]string{"service": "channel-service", "method": "/v1.Channel/Get", "status": codes.Unavailable.String()}))
}

// TestUnaryClientMetricsInterceptor_NilGuard exercises the nil-protection
// branch without touching the global (the interceptor must not panic if the
// vector is nil).
func TestUnaryClientMetricsInterceptor_NilGuard(t *testing.T) {
	original := metrics.ServiceDependencyLatency
	metrics.ServiceDependencyLatency = nil
	defer func() { metrics.ServiceDependencyLatency = original }()

	interceptor := UnaryClientMetricsInterceptor("whatever")
	invoker := func(ctx context.Context, method string, req, reply any, cc *grpc.ClientConn, opts ...grpc.CallOption) error {
		return errors.New("boom")
	}

	// Must not panic and must return the invoker error untouched.
	err := interceptor(context.Background(), "/v1.X/Y", nil, nil, nil, invoker)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "boom")
}

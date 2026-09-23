package xtrace

import (
	"context"
	"net/http"
	"net/http/httptest"
	"regexp"
	"testing"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/propagation"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/sdk/trace/tracetest"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestMiddlewareLinksCompatibilityIDAndOTel(t *testing.T) {
	previous, propagator := otel.GetTracerProvider(), otel.GetTextMapPropagator()
	recorder := tracetest.NewSpanRecorder()
	tp := sdktrace.NewTracerProvider(sdktrace.WithSpanProcessor(recorder))
	otel.SetTracerProvider(tp)
	otel.SetTextMapPropagator(propagation.TraceContext{})
	t.Cleanup(func() {
		otel.SetTracerProvider(previous)
		otel.SetTextMapPropagator(propagator)
		_ = tp.Shutdown(context.Background())
	})
	req := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", nil)
	req.Header.Set(TraceIDHeader, "client-compatible-id")
	req.Header.Set("Traceparent", "00-0123456789abcdef0123456789abcdef-0123456789abcdef-01")
	w := httptest.NewRecorder()
	Middleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("X-Request-ID", "root-request")
		require.Equal(t, "client-compatible-id", ExtractTraceID(r.Context()))
		require.Equal(t, "0123456789abcdef0123456789abcdef", OTelTraceID(r.Context()))
	})).ServeHTTP(w, req)
	require.Equal(t, "client-compatible-id", w.Header().Get(TraceIDHeader))
	require.Equal(t, "0123456789abcdef0123456789abcdef", w.Header().Get(OTelTraceIDHeader))
	spans := recorder.Ended()
	require.Len(t, spans, 1)
	attrs := map[string]string{}
	for _, a := range spans[0].Attributes() {
		attrs[string(a.Key)] = a.Value.AsString()
	}
	require.Equal(t, "root-request", attrs["request.root_id"])
	require.Equal(t, "client-compatible-id", attrs["request.compat_trace_id"])
}

func TestNormalizeOTLPEndpoint(t *testing.T) {
	tests := []struct {
		name         string
		endpoint     string
		wantHostPort string
		wantURLPath  string
		wantUseTLS   bool
	}{
		{"empty falls back to empty", "", "", "", false},
		{"bare host:port", "jaeger:4318", "jaeger:4318", "", false},
		{"http scheme", "http://jaeger:4318", "jaeger:4318", "", false},
		{"http scheme with path", "http://jaeger:4318/v1/traces", "jaeger:4318", "/v1/traces", false},
		{"https scheme with path", "https://collector.example.com:4318/v1/traces", "collector.example.com:4318", "/v1/traces", true},
		{"bare host:port with path", "jaeger:4318/v1/traces", "jaeger:4318", "/v1/traces", false},
		{"whitespace trimmed", "  https://h:4318/p  ", "h:4318", "/p", true},
		{"malformed URL falls back raw", "http://[::1", "http://[::1", "", false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			hostPort, urlPath, useTLS := normalizeOTLPEndpoint(tt.endpoint)
			assert.Equal(t, tt.wantHostPort, hostPort)
			assert.Equal(t, tt.wantURLPath, urlPath)
			assert.Equal(t, tt.wantUseTLS, useTLS)
		})
	}
}

func TestGenerateTraceID_Format(t *testing.T) {
	id := GenerateTraceID()
	assert.Len(t, id, 32, "trace ID must be 16 bytes hex-encoded")
	assert.Regexp(t, regexp.MustCompile(`^[0-9a-f]{32}$`), id)
}

func TestGenerateTraceID_Uniqueness(t *testing.T) {
	seen := map[string]bool{}
	for range 100 {
		id := GenerateTraceID()
		require.False(t, seen[id], "trace IDs must be unique")
		seen[id] = true
	}
}

func TestWithAndExtractTraceID_RoundTrip(t *testing.T) {
	ctx := WithTraceID(context.Background(), "trace-1")
	assert.Equal(t, "trace-1", ExtractTraceID(ctx))
}

func TestExtractTraceID_NotPresent_Empty(t *testing.T) {
	assert.Equal(t, "", ExtractTraceID(context.Background()))
}

func TestExtractTraceID_WrongType_Empty(t *testing.T) {
	ctx := context.WithValue(context.Background(), traceIDKey, 12345)
	assert.Equal(t, "", ExtractTraceID(ctx), "non-string values must be ignored")
}

func TestWithTraceID_Overwrites(t *testing.T) {
	ctx := WithTraceID(context.Background(), "first")
	ctx = WithTraceID(ctx, "second")
	assert.Equal(t, "second", ExtractTraceID(ctx))
}

func TestMiddleware_HeaderPresent_Preserves(t *testing.T) {
	var got string
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		got = ExtractTraceID(r.Context())
		w.WriteHeader(http.StatusOK)
	})
	req := httptest.NewRequest(http.MethodGet, "/health", nil)
	req.Header.Set(TraceIDHeader, "client-trace")
	rec := httptest.NewRecorder()

	Middleware(handler).ServeHTTP(rec, req)
	assert.Equal(t, "client-trace", got, "handler must see the client-supplied trace ID")
	assert.Equal(t, "client-trace", rec.Header().Get(TraceIDHeader), "response must echo the trace ID")
}

func TestMiddleware_HeaderMissing_Generates(t *testing.T) {
	var got string
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		got = ExtractTraceID(r.Context())
		w.WriteHeader(http.StatusOK)
	})
	req := httptest.NewRequest(http.MethodGet, "/health", nil)
	rec := httptest.NewRecorder()

	Middleware(handler).ServeHTTP(rec, req)
	assert.Len(t, got, 32, "missing header must generate a fresh trace ID")
	assert.Equal(t, got, rec.Header().Get(TraceIDHeader), "generated ID must be echoed back")
}

func TestInitTracer_Disabled_Noop(t *testing.T) {
	shutdown, err := InitTracer(Config{Enabled: false, Endpoint: "http://x:4318"})
	require.NoError(t, err)
	require.NotNil(t, shutdown)
	shutdown() // must not panic
}

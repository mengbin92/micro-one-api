package xtrace

import (
	"context"
	"net/http"
	"net/http/httptest"
	"regexp"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

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

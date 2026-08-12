package xtrace

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"net/http"
	"net/url"
	"strings"
	"time"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/exporters/otlp/otlptrace/otlptracehttp"
	"go.opentelemetry.io/otel/propagation"
	"go.opentelemetry.io/otel/sdk/resource"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	semconv "go.opentelemetry.io/otel/semconv/v1.24.0"
)

type contextKey string

const traceIDKey contextKey = "traceID"

// Config holds tracing configuration.
type Config struct {
	Enabled    bool    `yaml:"enabled"`
	Endpoint   string  `yaml:"endpoint"`    // e.g. "http://jaeger:4318/v1/traces"
	Service    string  `yaml:"service"`     // service name
	SampleRate float64 `yaml:"sample_rate"` // 0.0 - 1.0
}

// InitTracer initializes OpenTelemetry tracing with OTLP HTTP exporter.
// Returns a shutdown function that must be called on application exit.
func InitTracer(cfg Config) (func(), error) {
	if !cfg.Enabled {
		return func() {}, nil
	}

	if cfg.SampleRate <= 0 {
		cfg.SampleRate = 1.0
	}

	// Normalize the endpoint. The OTLP HTTP exporter's WithEndpoint expects a
	// bare "host:port" (no scheme, no path); the path must be supplied via
	// WithURLPath. Configs that provide a full URL like
	// "http://jaeger:4318/v1/traces" previously produced malformed gRPC-style
	// dial targets that silently failed to export (platform-L3).
	hostPort, urlPath, useTLS := normalizeOTLPEndpoint(cfg.Endpoint)

	opts := []otlptracehttp.Option{
		otlptracehttp.WithEndpoint(hostPort),
	}
	if urlPath != "" {
		opts = append(opts, otlptracehttp.WithURLPath(urlPath))
	}
	if !useTLS {
		opts = append(opts, otlptracehttp.WithInsecure())
	}

	ctx := context.Background()

	exporter, err := otlptracehttp.New(ctx, opts...)
	if err != nil {
		return nil, err
	}

	res, err := resource.Merge(
		resource.Default(),
		resource.NewWithAttributes(
			semconv.SchemaURL,
			semconv.ServiceName(cfg.Service),
		),
	)
	if err != nil {
		return nil, err
	}

	tp := sdktrace.NewTracerProvider(
		sdktrace.WithBatcher(exporter),
		sdktrace.WithResource(res),
		sdktrace.WithSampler(sdktrace.TraceIDRatioBased(cfg.SampleRate)),
	)

	otel.SetTracerProvider(tp)
	otel.SetTextMapPropagator(propagation.NewCompositeTextMapPropagator(
		propagation.TraceContext{},
		propagation.Baggage{},
	))

	shutdown := func() {
		// Bound shutdown so a stuck exporter cannot hang process exit
		// (platform-L3): the original ctx was context.Background() with no
		// deadline.
		shutCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = tp.Shutdown(shutCtx)
	}

	return shutdown, nil
}

// normalizeOTLPEndpoint parses a user-supplied OTLP endpoint into the
// (host:port, urlPath, useTLS) tuple expected by the OTLP HTTP exporter options.
// It accepts all of:
//   - "jaeger:4318"                       → ("jaeger:4318", "", false)
//   - "jaeger:4318/v1/traces"             → ("jaeger:4318", "/v1/traces", false)
//   - "http://jaeger:4318"                → ("jaeger:4318", "", false)
//   - "http://jaeger:4318/v1/traces"      → ("jaeger:4318", "/v1/traces", false)
//   - "https://collector.example.com:4318/v1/traces" → (…, "/v1/traces", true)
//
// A bare string without a scheme is treated as host:port over plaintext.
func normalizeOTLPEndpoint(endpoint string) (hostPort, urlPath string, useTLS bool) {
	endpoint = strings.TrimSpace(endpoint)
	if endpoint == "" {
		return "", "", false
	}

	// No scheme → assume already "host:port" (with optional path).
	if !strings.Contains(endpoint, "://") {
		if u, p, found := strings.Cut(endpoint, "/"); found {
			// Keep the path form consistent with the scheme branch below:
			// otlptracehttp.WithURLPath expects a path beginning with "/",
			// otherwise the exporter builds a malformed URL (platform-L3
			// regression family).
			return u, "/" + p, false
		}
		return endpoint, "", false
	}

	u, err := url.Parse(endpoint)
	if err != nil || u.Host == "" {
		// Malformed: fall back to the raw string as host:port rather than
		// silently dropping tracing.
		return endpoint, "", false
	}
	hostPort = u.Host
	urlPath = u.Path
	useTLS = u.Scheme == "https"
	return hostPort, urlPath, useTLS
}

// GenerateTraceID creates a new random 16-byte hex trace ID.
func GenerateTraceID() string {
	b := make([]byte, 16)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
}

// WithTraceID stores a trace ID in the context.
func WithTraceID(ctx context.Context, traceID string) context.Context {
	return context.WithValue(ctx, traceIDKey, traceID)
}

// ExtractTraceID retrieves the trace ID from context. Returns empty string if not present.
func ExtractTraceID(ctx context.Context) string {
	if v, ok := ctx.Value(traceIDKey).(string); ok {
		return v
	}
	return ""
}

// TraceIDHeader is the HTTP header used to propagate trace IDs.
const TraceIDHeader = "X-Trace-ID"

// Middleware returns an HTTP middleware that extracts or generates a trace ID.
func Middleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		traceID := r.Header.Get(TraceIDHeader)
		if traceID == "" {
			traceID = GenerateTraceID()
		}
		ctx := WithTraceID(r.Context(), traceID)
		w.Header().Set(TraceIDHeader, traceID)
		next.ServeHTTP(w, r.WithContext(ctx))
	})
}

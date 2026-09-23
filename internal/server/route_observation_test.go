package server

import (
	"bufio"
	"context"
	"net"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	khttp "github.com/go-kratos/kratos/v3/transport/http"
	"github.com/prometheus/client_golang/prometheus/testutil"
	"github.com/stretchr/testify/require"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/propagation"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/sdk/trace/tracetest"

	relaybiz "micro-one-api/internal/biz"
	"micro-one-api/platform/metrics"
	appmiddleware "micro-one-api/platform/middleware"
	xtrace "micro-one-api/platform/tracing"
)

type headerSequenceWriter struct {
	optionalInterfaceWriter
	statuses []int
}

func (w *headerSequenceWriter) WriteHeader(status int) { w.statuses = append(w.statuses, status) }

func TestRelayObservationInformationalResponse(t *testing.T) {
	w := &headerSequenceWriter{optionalInterfaceWriter: optionalInterfaceWriter{header: make(http.Header)}}
	observed := &relayObservationWriter{ResponseWriter: w}
	observed.WriteHeader(http.StatusEarlyHints)
	observed.WriteHeader(http.StatusBadGateway)
	if observed.status != http.StatusBadGateway || len(w.statuses) != 2 {
		t.Fatalf("final status=%d forwarded=%v, want 502 and [103 502]", observed.status, w.statuses)
	}
}

func TestRegisteredRouteCorrelatesTraceThroughSelectionOutcome(t *testing.T) {
	t.Setenv("RELAY_ROUTING_CONTEXT_V2", "false")
	oldProvider, oldPropagator := otel.GetTracerProvider(), otel.GetTextMapPropagator()
	spans := tracetest.NewSpanRecorder()
	provider := sdktrace.NewTracerProvider(sdktrace.WithSpanProcessor(spans))
	otel.SetTracerProvider(provider)
	otel.SetTextMapPropagator(propagation.TraceContext{})
	t.Cleanup(func() {
		_ = provider.Shutdown(context.Background())
		otel.SetTracerProvider(oldProvider)
		otel.SetTextMapPropagator(oldPropagator)
	})
	recorder := &selectionTestRecorder{}
	uc := relaybiz.NewRelayUsecase(orchestratorIdentityClient{}, &selectionTestChannelClient{fallback: &relaybiz.Channel{ID: 7}}, nil, nil)
	uc.SetSelectionRecorder(recorder)
	s := NewHTTPServer(nil, nil, nil, nil, nil)
	s.UseRouteMiddleware(appmiddleware.RequestID)
	srv := khttp.NewServer()
	s.handleFunc(srv, "/v1/trace-test", executionRoute("/v1/trace-test", relayExecutionPathLegacy), func(w http.ResponseWriter, r *http.Request) {
		plan, err := uc.Plan(r.Context(), relaybiz.RelayRequest{Token: "test", Model: "gpt-4o-mini"})
		require.NoError(t, err)
		relaybiz.FinalizeSelectionResult(recorder, *plan.SelectionEvent, "success", "", false, time.Millisecond)
		w.WriteHeader(http.StatusOK)
	})
	req := httptest.NewRequest(http.MethodPost, "/v1/trace-test", nil)
	req.Header.Set("X-Request-ID", "root-test")
	req.Header.Set("X-Trace-ID", "compat-test")
	req.Header.Set("traceparent", "00-00112233445566778899aabbccddeeff-0011223344556677-01")
	rec := httptest.NewRecorder()
	srv.ServeHTTP(rec, req)
	require.Equal(t, http.StatusOK, rec.Code)
	require.Equal(t, "root-test", rec.Header().Get("X-Request-ID"))
	require.Equal(t, "compat-test", rec.Header().Get(xtrace.TraceIDHeader))
	require.Len(t, recorder.events, 2)
	for _, event := range recorder.events {
		require.Equal(t, "root-test", event.RootRequestID)
		require.Equal(t, "compat-test", event.TraceID)
		require.Equal(t, "00112233445566778899aabbccddeeff", event.OTelTraceID)
	}
	require.Len(t, spans.Ended(), 1)
	require.Equal(t, rec.Header().Get(xtrace.OTelTraceIDHeader), spans.Ended()[0].SpanContext().TraceID().String())
}

func TestRelayObservationPanicRecordsFailure(t *testing.T) {
	endpoint := "/v1/panic-test"
	counter := metrics.RelayExecutorRequestsTotal.WithLabelValues(endpoint, relayStreamUnknown, relayExecutionPathRaw, "500", "error")
	before := testutil.ToFloat64(counter)
	func() {
		defer func() {
			if recover() == nil {
				t.Error("panic was swallowed")
			}
		}()
		serveObservedRelay(httptest.NewRecorder(), httptest.NewRequest(http.MethodPost, endpoint, nil), endpoint, relayExecutionPathRaw, relayStreamUnknown, func(http.ResponseWriter, *http.Request) { panic("handler failed") })
	}()
	if got := testutil.ToFloat64(counter); got != before+1 {
		t.Fatalf("panic observations=%v, want %v", got, before+1)
	}
}

func TestRegisteredRouteObservesInternalBudgetTimeout(t *testing.T) {
	t.Setenv("RELAY_REQUEST_TIMEOUT", "1ms")
	s := NewHTTPServer(nil, nil, nil, nil, nil)
	endpoint := "/v1/internal-budget-test"
	counter := metrics.RelayExecutorRequestsTotal.WithLabelValues(endpoint, relayStreamUnknown, relayExecutionPathRaw, "504", "timeout")
	before := testutil.ToFloat64(counter)
	h := s.wrapRoute(endpoint, executionRoute(endpoint, relayExecutionPathRaw), http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		applyRequestBudget(r, []byte(`{}`))
		select {
		case <-r.Context().Done():
		case <-time.After(time.Second):
			t.Fatal("request budget was not applied")
		}
		w.WriteHeader(http.StatusGatewayTimeout)
	}))
	h.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodPost, endpoint, nil))
	if got := testutil.ToFloat64(counter); got != before+1 {
		t.Fatalf("budget timeout observations=%v, want %v", got, before+1)
	}
}

func TestRouteDeclarationRequiresObservationCategory(t *testing.T) {
	s := NewHTTPServer(nil, nil, nil, nil, nil)
	defer func() {
		if recover() == nil {
			t.Fatal("route without an observation category did not fail registration")
		}
	}()
	s.wrapRoute("/v1/missing-declaration", routeDeclaration{}, http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))
}

func TestRegisteredExecutionRoutesObserveExactlyOnce(t *testing.T) {
	s := NewHTTPServer(nil, nil, nil, nil, nil)
	srv := khttp.NewServer()
	s.RegisterRoutes(srv)

	tests := []struct {
		name          string
		method        string
		path          string
		endpoint      string
		executionPath string
		status        string
	}{
		{name: "chat method rejection", method: http.MethodGet, path: relayEndpointChatCompletions, endpoint: relayEndpointChatCompletions, executionPath: relayExecutionPathLegacy, status: "405"},
		{name: "raw auth rejection", method: http.MethodPost, path: "/v1/embeddings", endpoint: "/v1/embeddings", executionPath: relayExecutionPathRaw, status: "401"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			counter := metrics.RelayExecutorRequestsTotal.WithLabelValues(tt.endpoint, relayStreamUnknown, tt.executionPath, tt.status, "error")
			before := testutil.ToFloat64(counter)
			rec := httptest.NewRecorder()
			srv.ServeHTTP(rec, httptest.NewRequest(tt.method, tt.path, nil))
			if got := testutil.ToFloat64(counter); got != before+1 {
				t.Fatalf("execution terminal observations = %v, want %v", got, before+1)
			}
		})
	}
}

func TestNonExecutionRoutesDoNotFabricateExecutionOutcomes(t *testing.T) {
	s := NewHTTPServer(nil, nil, nil, nil, nil)
	srv := khttp.NewServer()
	s.RegisterRoutes(srv)

	counter := metrics.RelayExecutorRequestsTotal.WithLabelValues("/v1/images/edits", relayStreamUnknown, relayExecutionPathRaw, "501", "error")
	before := testutil.ToFloat64(counter)
	rec := httptest.NewRecorder()
	srv.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/v1/images/edits", nil))
	if rec.Code != http.StatusNotImplemented {
		t.Fatalf("status = %d, want 501", rec.Code)
	}
	if got := testutil.ToFloat64(counter); got != before {
		t.Fatalf("unsupported route fabricated execution outcome: before=%v after=%v", before, got)
	}
}

func TestRelayExecutionObservationDoesNotCountCORSPreflight(t *testing.T) {
	counter := metrics.RelayExecutorRequestsTotal.WithLabelValues("/v1/preflight-test", relayStreamUnknown, relayExecutionPathRaw, "204", "success")
	before := testutil.ToFloat64(counter)
	req := httptest.NewRequest(http.MethodOptions, "/v1/preflight-test", nil)
	req.Header.Set("Access-Control-Request-Method", http.MethodPost)
	rec := httptest.NewRecorder()

	observeRelayExecution(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusNoContent)
	}), "/v1/preflight-test", relayExecutionPathRaw, relayStreamUnknown).ServeHTTP(rec, req)

	if got := testutil.ToFloat64(counter); got != before {
		t.Fatalf("CORS preflight fabricated execution outcome: before=%v after=%v", before, got)
	}
}

func TestRelayExecutionObservationClassifiesCancellation(t *testing.T) {
	counter := metrics.RelayExecutorRequestsTotal.WithLabelValues("/v1/cancel-test", relayStreamUnknown, relayExecutionPathRaw, "200", "canceled")
	before := testutil.ToFloat64(counter)
	ctx, cancel := context.WithCancel(context.Background())
	req := httptest.NewRequest(http.MethodPost, "/v1/cancel-test", nil).WithContext(ctx)
	rec := httptest.NewRecorder()

	observeRelayExecution(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		cancel()
		w.WriteHeader(http.StatusOK)
	}), "/v1/cancel-test", relayExecutionPathRaw, relayStreamUnknown).ServeHTTP(rec, req)

	if got := testutil.ToFloat64(counter); got != before+1 {
		t.Fatalf("canceled observations = %v, want %v", got, before+1)
	}
}

type optionalInterfaceWriter struct {
	header  http.Header
	flushed bool
}

func (w *optionalInterfaceWriter) Header() http.Header {
	return w.header
}

func (*optionalInterfaceWriter) Write(p []byte) (int, error) {
	return len(p), nil
}

func (*optionalInterfaceWriter) WriteHeader(int) {}

func (w *optionalInterfaceWriter) Flush() {
	w.flushed = true
}

func (*optionalInterfaceWriter) Hijack() (net.Conn, *bufio.ReadWriter, error) {
	server, client := net.Pipe()
	_ = client.Close()
	return server, bufio.NewReadWriter(bufio.NewReader(server), bufio.NewWriter(server)), nil
}

func TestRelayExecutionObservationPreservesOptionalWriterInterfaces(t *testing.T) {
	s := NewHTTPServer(nil, nil, nil, nil, nil)
	s.UseRouteMiddleware(appmiddleware.NewHTTPMetricsMiddleware("route-observation-capabilities"))
	var hasFlusher, hasHijacker bool
	handler := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		flusher, ok := w.(http.Flusher)
		hasFlusher = ok
		if ok {
			flusher.Flush()
		}
		hijacker, ok := w.(http.Hijacker)
		hasHijacker = ok
		if ok {
			conn, _, err := hijacker.Hijack()
			if err != nil {
				t.Errorf("Hijack() error = %v", err)
				return
			}
			_ = conn.Close()
		}
	})
	h := s.wrapRoute("/v1/capabilities", executionRoute("/v1/capabilities", relayExecutionPathRaw), handler)
	w := &optionalInterfaceWriter{header: make(http.Header)}
	h.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/v1/capabilities", nil))

	if !hasFlusher || !hasHijacker || !w.flushed {
		t.Fatalf("optional interfaces: flusher=%t hijacker=%t flushed=%t", hasFlusher, hasHijacker, w.flushed)
	}
}

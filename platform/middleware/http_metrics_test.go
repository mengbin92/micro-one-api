package middleware

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/testutil"

	"micro-one-api/platform/metrics"
)

// v0.19 P3-0 regression: the HTTP request-level metrics middleware must
// capture the final status code (including codes written by inner
// middlewares), normalize variable path segments to keep the label
// low-cardinality, and emit counter + latency histogram observations.

func TestHTTPMetricsMiddleware_statusCapture(t *testing.T) {
	svc := "test-relay"
	mw := NewHTTPMetricsMiddleware(svc)

	cases := []struct {
		name     string
		handler  http.HandlerFunc
		wantCode string
	}{
		{"explicit writeheader", func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusTooManyRequests) }, "429"},
		{"bare write implies 200", func(w http.ResponseWriter, _ *http.Request) { _, _ = w.Write([]byte("ok")) }, "200"},
		{"inner middleware rewrites code", func(w http.ResponseWriter, _ *http.Request) {
			w.WriteHeader(http.StatusOK)
			w.Header().Set("X-Test", "1")
			w.WriteHeader(http.StatusBadGateway)
		}, "502"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			before := testutil.ToFloat64(metrics.HTTPRequestTotal.WithLabelValues(svc, http.MethodGet, "/v1/chat/completions", tc.wantCode))
			rec := httptest.NewRecorder()
			req := httptest.NewRequest(http.MethodGet, "http://relay/v1/chat/completions", nil)
			mw(tc.handler).ServeHTTP(rec, req)
			after := testutil.ToFloat64(metrics.HTTPRequestTotal.WithLabelValues(svc, http.MethodGet, "/v1/chat/completions", tc.wantCode))
			if after != before+1 {
				t.Fatalf("status=%s counter not incremented: before=%v after=%v", tc.wantCode, before, after)
			}
		})
	}
}

func TestHTTPMetricsMiddleware_normalizedPath(t *testing.T) {
	svc := "test-relay"
	mw := NewHTTPMetricsMiddleware(svc)

	cases := []struct {
		rawPath string
		want    string
	}{
		{"/v1/chat/completions", "/v1/chat/completions"},
		{"/v1/models", "/v1/models"},
		{"/v1/messages", "/v1/messages"},
		{"/v1/responses/123e4567-e89b-12d3-a456-426614174000", "/v1/responses/{id}"},
		{"/v1/responses/42", "/v1/responses/{id}"},
		{"/v1/fine_tuning/alpha/graders/abcDef1234567890abcdef1234567890abcd", "/v1/fine_tuning/alpha/graders/{id}"},
	}
	handler := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusOK) })
	for _, tc := range cases {
		t.Run(tc.rawPath, func(t *testing.T) {
			before := testutil.ToFloat64(metrics.HTTPRequestTotal.WithLabelValues(svc, http.MethodPost, tc.want, "200"))
			rec := httptest.NewRecorder()
			req := httptest.NewRequest(http.MethodPost, "http://relay"+tc.rawPath, nil)
			mw(handler).ServeHTTP(rec, req)
			after := testutil.ToFloat64(metrics.HTTPRequestTotal.WithLabelValues(svc, http.MethodPost, tc.want, "200"))
			if after != before+1 {
				t.Fatalf("path=%q want label=%q got no increment (before=%v after=%v)", tc.rawPath, tc.want, before, after)
			}
		})
	}
}

func TestHTTPMetricsMiddleware_durationObserved(t *testing.T) {
	svc := "test-relay"
	mw := NewHTTPMetricsMiddleware(svc)
	handler := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusOK) })

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "http://relay/v1/messages", nil)
	mw(handler).ServeHTTP(rec, req)

	// At least one duration series must exist for the observed route —
	// Collect the HistogramVec and count series: a count >= 1 proves
	// Observe() ran (testutil.ToFloat64 would return NaN for histograms).
	ch := make(chan prometheus.Metric, 16)
	metrics.HTTPRequestDuration.Collect(ch)
	close(ch)
	series := 0
	for range ch {
		series++
	}
	if series < 1 {
		t.Fatalf("duration histogram has no series after observation")
	}
}

func TestNormalizePath(t *testing.T) {
	cases := []struct{ in, want string }{
		{"", ""},
		{"/v1/chat/completions", "/v1/chat/completions"},
		{"/v1/responses/123e4567-e89b-12d3-a456-426614174000", "/v1/responses/{id}"},
		{"/v1/responses/123", "/v1/responses/{id}"},
		{"/v1/models/gpt-4o", "/v1/models/gpt-4o"}, // model name is not a numeric/uuid segment
	}
	for _, tc := range cases {
		if got := normalizePath(tc.in); got != tc.want {
			t.Errorf("normalizePath(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}
}

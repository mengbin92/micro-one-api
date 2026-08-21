package middleware

import (
	"net/http"
	"regexp"
	"strconv"
	"strings"
	"time"

	"micro-one-api/platform/metrics"
)

// v0.19 P3-0: HTTP request-level observability for the relay-gateway entry
// point. `metrics.HTTPRequestTotal` / `metrics.HTTPRequestDuration` were
// registered but never wired to any HTTP server; this middleware closes that
// gap so P3 trigger conditions ("502/429 sustained threshold", latency) are
// queryable in Prometheus (see docs/design/v0.19-p3-gate-assessment.md).

var (
	uuidSegRe = regexp.MustCompile(`^[0-9a-fA-F]{8}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{12}$`)
	numSegRe  = regexp.MustCompile(`^[0-9]+$`)
)

// normalizePath collapses variable path segments (uuids, numeric ids, long
// random segments) into a fixed "{id}" placeholder so the "path" label stays
// low-cardinality. Example: /v1/responses/123e4567-e89b-12d3-a456-426614174000
// -> /v1/responses/{id}. Fixed routes (/v1/chat/completions) pass through.
func normalizePath(p string) string {
	segs := strings.Split(p, "/")
	for i, s := range segs {
		if s == "" || s == "v1" {
			continue
		}
		if uuidSegRe.MatchString(s) || numSegRe.MatchString(s) || len(s) > 32 {
			segs[i] = "{id}"
		}
	}
	return strings.Join(segs, "/")
}

// statusRecorder captures the final status code written to the response,
// including codes written by downstream middleware wrapping this one.
type statusRecorder struct {
	http.ResponseWriter
	status int
}

func (r *statusRecorder) WriteHeader(code int) {
	r.status = code
	r.ResponseWriter.WriteHeader(code)
}

func (r *statusRecorder) Write(b []byte) (int, error) {
	// A handler that writes a body without an explicit WriteHeader implies
	// 200; record it so a bare write still produces a status.
	if r.status == 0 {
		r.status = http.StatusOK
	}
	return r.ResponseWriter.Write(b)
}

// Flush preserves SSE/streaming support through the metrics wrapper.
func (r *statusRecorder) Flush() {
	if f, ok := r.ResponseWriter.(http.Flusher); ok {
		f.Flush()
	}
}

// Unwrap lets http.ResponseController reach optional interfaces implemented
// by the underlying response writer.
func (r *statusRecorder) Unwrap() http.ResponseWriter {
	return r.ResponseWriter
}

// NewHTTPMetricsMiddleware returns a middleware that records per-request
// counters and latency histograms into the shared platform metrics.
//   - service: label value, e.g. "relay-gateway".
//   - Mount it as the OUTERMOST route middleware so it observes the final
//     status code after inner middlewares (ratelimit/idempotency/audit) run.
//   - Healthz/metrics endpoints are usually registered outside the
//     middleware chain and are intentionally not counted (no scrape noise).
func NewHTTPMetricsMiddleware(service string) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			start := time.Now()
			rw := &statusRecorder{ResponseWriter: w}
			next.ServeHTTP(rw, r)
			if rw.status == 0 {
				rw.status = http.StatusOK
			}
			path := normalizePath(r.URL.Path)
			metrics.HTTPRequestTotal.WithLabelValues(service, r.Method, path, strconv.Itoa(rw.status)).Inc()
			metrics.HTTPRequestDuration.WithLabelValues(service, r.Method, path).Observe(time.Since(start).Seconds())
		})
	}
}

package metrics

import (
	"net/http"
	"strconv"
	"time"
)

// responseWriter wraps http.ResponseWriter to capture status code.
type responseWriter struct {
	http.ResponseWriter
	statusCode int
}

func (rw *responseWriter) WriteHeader(code int) {
	rw.statusCode = code
	rw.ResponseWriter.WriteHeader(code)
}

// Flush forwards to the underlying http.Flusher if present so streaming
// handlers proxied through the metrics middleware can flush incremental data
// (platform-L4: without this, w.(http.Flusher) assertions fail for SSE).
func (rw *responseWriter) Flush() {
	if f, ok := rw.ResponseWriter.(http.Flusher); ok {
		f.Flush()
	}
}

// Unwrap exposes the underlying ResponseWriter so optional interfaces
// (http.Hijacker, http.Pusher, …) remain accessible through the wrapper.
func (rw *responseWriter) Unwrap() http.ResponseWriter {
	return rw.ResponseWriter
}

// HTTPMiddleware returns an HTTP middleware that records request metrics.
func HTTPMiddleware(service string) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			start := time.Now()
			ActiveRequests.WithLabelValues(service).Inc()
			defer ActiveRequests.WithLabelValues(service).Dec()

			rw := &responseWriter{ResponseWriter: w, statusCode: http.StatusOK}
			next.ServeHTTP(rw, r)

			duration := time.Since(start).Seconds()
			status := strconv.Itoa(rw.statusCode)

			// Prefer the matched route pattern (e.g. "/v1/chat/completions") over
			// the raw request path to avoid an unbounded-cardinality label blowup:
			// every distinct path segment (IDs, prompts) would otherwise create a
			// new time series (platform-L4). Fall back to the path when no pattern
			// is registered (e.g. net/http without a mux that sets RoutePattern).
			route := r.Pattern
			if route == "" {
				route = r.URL.Path
			}

			HTTPRequestTotal.WithLabelValues(service, r.Method, route, status).Inc()
			HTTPRequestDuration.WithLabelValues(service, r.Method, route).Observe(duration)
		})
	}
}

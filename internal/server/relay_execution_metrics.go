package server

import (
	"bufio"
	"context"
	"net"
	"net/http"
	"strconv"
	"sync"
	"time"

	"micro-one-api/platform/metrics"
)

const (
	relayEndpointChatCompletions = "/v1/chat/completions"
	relayEndpointResponses       = "/v1/responses"
	relayEndpointMessages        = "/v1/messages"

	relayExecutionPathLegacy       = "legacy"
	relayExecutionPathOrchestrator = "orchestrator"
	relayStreamUnknown             = "unknown"
)

type relayExecutionObservationKey struct{}

type relayExecutionObservation struct {
	mu       sync.RWMutex
	endpoint string
	stream   string
	path     string
	result   string
}

func (o *relayExecutionObservation) setStream(stream bool) {
	if o == nil {
		return
	}
	o.mu.Lock()
	o.stream = strconv.FormatBool(stream)
	o.mu.Unlock()
}

func (o *relayExecutionObservation) setPath(path string) {
	if o == nil {
		return
	}
	o.mu.Lock()
	o.path = path
	o.mu.Unlock()
}

func (o *relayExecutionObservation) setResult(result string) {
	if o == nil {
		return
	}
	o.mu.Lock()
	o.result = result
	o.mu.Unlock()
}

func (o *relayExecutionObservation) snapshot() (endpoint, stream, path, result string) {
	if o == nil {
		return "", relayStreamUnknown, "", ""
	}
	o.mu.RLock()
	defer o.mu.RUnlock()
	return o.endpoint, o.stream, o.path, o.result
}

type relayObservationWriter struct {
	http.ResponseWriter
	status      int
	observation *relayExecutionObservation
}

func (w *relayObservationWriter) WriteHeader(status int) {
	if w.status != 0 {
		return
	}
	w.status = status
	w.ResponseWriter.WriteHeader(status)
}

func (w *relayObservationWriter) Write(p []byte) (int, error) {
	if w.status == 0 {
		w.status = http.StatusOK
	}
	n, err := w.ResponseWriter.Write(p)
	if err != nil && w.observation != nil {
		_, stream, _, _ := w.observation.snapshot()
		result := "error"
		if stream == "true" {
			result = "stream_error"
		}
		w.observation.setResult(result)
	}
	return n, err
}

func (w *relayObservationWriter) Flush() {
	if w.status == 0 {
		w.status = http.StatusOK
	}
	if flusher, ok := w.ResponseWriter.(http.Flusher); ok {
		flusher.Flush()
	}
}

func (w *relayObservationWriter) Unwrap() http.ResponseWriter {
	return w.ResponseWriter
}

func (w *relayObservationWriter) Hijack() (net.Conn, *bufio.ReadWriter, error) {
	conn, readWriter, err := http.NewResponseController(w.ResponseWriter).Hijack()
	if err == nil && w.status == 0 {
		w.status = http.StatusSwitchingProtocols
	}
	return conn, readWriter, err
}

func serveObservedRelay(w http.ResponseWriter, r *http.Request, endpoint, path, stream string, handler http.HandlerFunc) {
	startedAt := time.Now()
	observation := &relayExecutionObservation{endpoint: endpoint, stream: stream, path: path}
	observedWriter := &relayObservationWriter{ResponseWriter: w, observation: observation}
	ctx := context.WithValue(r.Context(), relayExecutionObservationKey{}, observation)
	handler(observedWriter, r.WithContext(ctx))
	status := observedWriter.status
	if status == 0 {
		status = http.StatusOK
	}
	observedEndpoint, observedStream, observedPath, result := observation.snapshot()
	if result == "" {
		result = "success"
		if status >= http.StatusBadRequest {
			result = "error"
		}
	}
	metrics.RelayExecutorRequestsTotal.WithLabelValues(observedEndpoint, observedStream, observedPath, strconv.Itoa(status), result).Inc()
	metrics.RelayExecutorRequestDuration.WithLabelValues(observedEndpoint, observedStream, observedPath).Observe(time.Since(startedAt).Seconds())
}

func setRelayObservationStream(ctx context.Context, stream bool) {
	if observation, ok := ctx.Value(relayExecutionObservationKey{}).(*relayExecutionObservation); ok {
		observation.setStream(stream)
	}
}

func setRelayObservationPath(ctx context.Context, path string) {
	if observation, ok := ctx.Value(relayExecutionObservationKey{}).(*relayExecutionObservation); ok {
		observation.setPath(path)
	}
}

func setRelayObservationResult(ctx context.Context, result string) {
	if observation, ok := ctx.Value(relayExecutionObservationKey{}).(*relayExecutionObservation); ok {
		observation.setResult(result)
	}
}

func recordRelayQuotaOutcome(ctx context.Context, outcome string) {
	observation, ok := ctx.Value(relayExecutionObservationKey{}).(*relayExecutionObservation)
	if !ok {
		return
	}
	endpoint, stream, path, _ := observation.snapshot()
	metrics.RelayExecutorQuotaOutcomeTotal.WithLabelValues(endpoint, stream, path, outcome).Inc()
}

func recordRelayFailover(ctx context.Context, result, reason string) {
	observation, ok := ctx.Value(relayExecutionObservationKey{}).(*relayExecutionObservation)
	if !ok {
		return
	}
	endpoint, stream, path, _ := observation.snapshot()
	if reason == "" {
		reason = "unknown"
	}
	metrics.RelayExecutorFailoverTotal.WithLabelValues(endpoint, stream, path, result, reason).Inc()
}

func recordRelayRetryOutcome(ctx context.Context, fallback bool, err error, reason string) {
	if !fallback {
		return
	}
	result := "switched"
	if err != nil {
		result = "exhausted"
	}
	recordRelayFailover(ctx, result, reason)
}

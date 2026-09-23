package server

import (
	"bufio"
	"context"
	"errors"
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
	if status >= 100 && status < 200 && status != http.StatusSwitchingProtocols {
		w.ResponseWriter.WriteHeader(status)
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
	_ = w.FlushError()
}

func (w *relayObservationWriter) FlushError() error {
	if w.status == 0 {
		w.status = http.StatusOK
	}
	err := http.NewResponseController(w.ResponseWriter).Flush()
	if err != nil {
		w.observation.setResult("stream_error")
	}
	return err
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
	if observation, ok := r.Context().Value(relayExecutionObservationKey{}).(*relayExecutionObservation); ok {
		observation.setPath(path)
		if stream != relayStreamUnknown {
			observation.mu.Lock()
			observation.stream = stream
			observation.mu.Unlock()
		}
		if _, alreadyWrapped := w.(*relayObservationWriter); alreadyWrapped {
			handler(w, r)
			return
		}
		handler(&relayObservationWriter{ResponseWriter: w, observation: observation}, r)
		return
	}
	startedAt := time.Now()
	observation := &relayExecutionObservation{endpoint: endpoint, stream: stream, path: path}
	observedWriter := &relayObservationWriter{ResponseWriter: w, observation: observation}
	ctx := context.WithValue(r.Context(), relayExecutionObservationKey{}, observation)
	completed := false
	defer func() {
		status := observedWriter.status
		if status == 0 {
			status = http.StatusOK
			if !completed {
				status = http.StatusInternalServerError
			}
		}
		observedEndpoint, observedStream, observedPath, result := observation.snapshot()
		if !completed {
			result = "error"
		}
		if result == "" {
			switch {
			case errors.Is(r.Context().Err(), context.Canceled):
				result = "canceled"
			case errors.Is(r.Context().Err(), context.DeadlineExceeded):
				result = "timeout"
			case status >= http.StatusBadRequest:
				result = "error"
			default:
				result = "success"
			}
		}
		metrics.RelayExecutorRequestsTotal.WithLabelValues(observedEndpoint, observedStream, observedPath, strconv.Itoa(status), result).Inc()
		metrics.RelayExecutorRequestDuration.WithLabelValues(observedEndpoint, observedStream, observedPath).Observe(time.Since(startedAt).Seconds())
	}()
	handler(observedWriter, r.WithContext(ctx))
	completed = true
}

func observeRelayExecution(next http.Handler, endpoint, path, stream string) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodOptions && r.Header.Get("Access-Control-Request-Method") != "" {
			next.ServeHTTP(w, r)
			return
		}
		serveObservedRelay(w, r, endpoint, path, stream, func(w http.ResponseWriter, r *http.Request) {
			next.ServeHTTP(w, r)
		})
	})
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

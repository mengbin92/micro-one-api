package server

import (
	"bufio"
	"context"
	"errors"
	"net"
	"net/http"
	"strings"
	"time"

	"go.opentelemetry.io/otel/propagation"
	"micro-one-api/domain/requesttrace"
	xtrace "micro-one-api/platform/tracing"
)

func (s *HTTPServer) SetTimingExporter(exporter *requesttrace.Exporter) { s.timingExporter = exporter }

func (s *HTTPServer) observeRequestTiming(next http.Handler, endpoint, path string) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if s.timingExporter == nil || r.Method == http.MethodOptions || strings.EqualFold(r.Header.Get("Upgrade"), "websocket") {
			xtrace.Middleware(next).ServeHTTP(w, r)
			return
		}
		parent := propagation.TraceContext{}.Extract(r.Context(), propagation.HeaderCarrier(r.Header))
		ctx, recorder := requesttrace.StartObservation(parent, "http", endpoint, "http", path, true)
		compat := r.Header.Get(xtrace.TraceIDHeader)
		if len(compat) > 128 || strings.IndexFunc(compat, func(r rune) bool { return r < 32 || r == 127 }) >= 0 {
			compat = ""
		}
		if compat == "" {
			compat = xtrace.OTelTraceID(ctx)
		}
		if compat == "" {
			compat = xtrace.GenerateTraceID()
		}
		ctx = xtrace.WithTraceID(ctx, compat)
		recorder.Update(func(summary *requesttrace.TimingSummary) { summary.CompatibilityTraceID = compat })
		w.Header().Set(xtrace.TraceIDHeader, compat)
		w.Header().Set("X-Observation-ID", requesttrace.Capture(ctx).ObservationID)
		if id := xtrace.OTelTraceID(ctx); id != "" {
			w.Header().Set(xtrace.OTelTraceIDHeader, id)
		}
		writer := &timingResponseWriter{ResponseWriter: w, recorder: recorder}
		completed := false
		defer func() {
			outcome := "success"
			if !completed || writer.status >= 500 {
				outcome = "failed"
			} else if writer.status >= 400 {
				outcome = "rejected"
			}
			if r.Context().Err() != nil {
				outcome = requesttrace.Outcome(r.Context().Err())
			}
			if observation, ok := ctx.Value(relayExecutionObservationKey{}).(*relayExecutionObservation); ok {
				_, _, actual, result := observation.snapshot()
				recorder.Update(func(summary *requesttrace.TimingSummary) { summary.ExecutionPath = actual })
				if result == "stream_error" {
					outcome = "incomplete"
				}
			}
			s.timingExporter.Submit(ctx, recorder.Finish(outcome))
		}()
		next.ServeHTTP(writer, r.WithContext(ctx))
		completed = true
	})
}

type timingResponseWriter struct {
	http.ResponseWriter
	recorder *requesttrace.Recorder
	status   int
	probe    requesttrace.ContentProbe
	wrote    bool
}

func (w *timingResponseWriter) Unwrap() http.ResponseWriter { return w.ResponseWriter }
func (w *timingResponseWriter) WriteHeader(status int) {
	if status >= 100 && status < 200 && status != 101 {
		w.ResponseWriter.WriteHeader(status)
		return
	}
	if w.status == 0 {
		var parseErr error
		if status >= 400 {
			parseErr = errors.New("rejected")
		}
		w.recorder.EndParse(parseErr)
		w.status = status
		w.ResponseWriter.WriteHeader(status)
	}
}
func (w *timingResponseWriter) Write(data []byte) (int, error) {
	w.recorder.EndParse(nil)
	if w.status == 0 {
		w.status = 200
	}
	started := time.Now()
	if strings.Contains(w.Header().Get("Content-Type"), "text/event-stream") {
		w.recorder.BeginOutput(started)
	}
	n, err := w.ResponseWriter.Write(data)
	w.recorder.Write(n, err)
	if n > 0 {
		w.wrote = true
	}
	if strings.Contains(w.Header().Get("Content-Type"), "text/event-stream") {
		w.recorder.Update(func(s *requesttrace.TimingSummary) { s.Protocol = "sse" })
		w.probe.Feed(data[:n])
		if w.probe.Truncated {
			w.recorder.Missing("output_frame_limit")
		}
	}
	return n, err
}
func (w *timingResponseWriter) Flush() { _ = w.FlushError() }
func (w *timingResponseWriter) FlushError() error {
	err := http.NewResponseController(w.ResponseWriter).Flush()
	if w.wrote {
		w.recorder.Write(1, err)
	}
	if err == nil {
		if w.probe.Kind != "" {
			w.recorder.Content(w.probe.Kind)
		}
		if w.probe.Terminal {
			w.recorder.TerminalOutcome(w.probe.TerminalOutcome())
		}
	}
	return err
}
func (w *timingResponseWriter) Hijack() (net.Conn, *bufio.ReadWriter, error) {
	return http.NewResponseController(w.ResponseWriter).Hijack()
}

// The outer recorder is shared even though handler contexts are derived later.
func timingHandlerFinished(ctx context.Context, started time.Time) {
	requesttrace.Observation(ctx).HandlerFinished(started)
}

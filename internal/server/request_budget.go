package server

import (
	"context"
	"net/http"
	"time"

	"micro-one-api/domain/requesttrace"
	provider "micro-one-api/domain/upstream/provider"
	"micro-one-api/pkg/jsonx"
)

type requestBudgetKey struct{}
type requestBudget struct {
	start             time.Time
	nonstream, stream time.Duration
	cancel            context.CancelFunc
	applied           bool
}

// The body reader selects the budget once the stream flag is known. Keeping
// one context at the HTTP boundary makes retries and backoff spend the same
// remaining budget. A shorter caller deadline always wins.
func (s *HTTPServer) withRequestBudget(next http.Handler) http.Handler {
	nonstream, err := provider.TimeoutFromEnv("RELAY_REQUEST_TIMEOUT", s.providerFactory.DefaultTimeout(), false)
	if err != nil {
		panic(err)
	}
	stream, err := provider.TimeoutFromEnv("RELAY_STREAM_TOTAL_TIMEOUT", 0, true)
	if err != nil {
		panic(err)
	}
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		state := &requestBudget{start: time.Now(), nonstream: nonstream, stream: stream}
		ctx := context.WithValue(r.Context(), requestBudgetKey{}, state)
		trace := requesttrace.FromContext(ctx)
		if trace.RootRequestID == "" {
			trace.RootRequestID = w.Header().Get("X-Request-ID")
		}
		if trace.RootRequestID == "" {
			trace.RootRequestID = generateRequestID()
		}
		w.Header().Set("X-Request-ID", trace.RootRequestID)
		ctx = requesttrace.WithAttempt(ctx, trace)
		defer func() {
			if state.cancel != nil {
				state.cancel()
			}
		}()
		next.ServeHTTP(w, r.WithContext(ctx))
	})
}
func applyRequestBudget(r *http.Request, body []byte) {
	state, _ := r.Context().Value(requestBudgetKey{}).(*requestBudget)
	if state == nil || state.applied {
		return
	}
	state.applied = true
	var probe struct {
		Stream bool `json:"stream"`
	}
	_ = jsonx.Unmarshal(body, &probe)
	duration := state.nonstream
	if probe.Stream {
		duration = state.stream
	}
	if duration <= 0 {
		return
	}
	ctx, cancel := context.WithDeadlineCause(r.Context(), state.start.Add(duration), provider.ErrRequestBudget)
	state.cancel = cancel
	*r = *r.WithContext(ctx)
}
func rootRequestID(r *http.Request) string {
	if id := requesttrace.FromContext(r.Context()).RootRequestID; id != "" {
		return id
	}
	return generateRequestID()
}

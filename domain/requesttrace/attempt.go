// Package requesttrace carries non-sensitive execution identity across relay
// attempts. RequestID remains the attempt-level billing idempotency key.
package requesttrace

import "context"

type Attempt struct {
	HealthProbeID   string
	RootRequestID   string
	Number          int32
	SourceKind      string
	SourceID        int64
	UpstreamModelID string
}

type contextKey struct{}

func WithAttempt(ctx context.Context, attempt Attempt) context.Context {
	BindRoot(ctx, attempt.RootRequestID)
	if attempt.Number > 0 {
		ctx = EnsureAttempt(ctx, attempt.Number)
		RecordAttempt(ctx, TimingAttempt{Number: attempt.Number, SourceKind: attempt.SourceKind, SourceID: attempt.SourceID, UpstreamModelID: attempt.UpstreamModelID})
	}
	return context.WithValue(ctx, contextKey{}, attempt)
}

func FromContext(ctx context.Context) Attempt {
	attempt, _ := ctx.Value(contextKey{}).(Attempt)
	return attempt
}

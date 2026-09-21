// Package requesttrace carries non-sensitive execution identity across relay
// attempts. RequestID remains the attempt-level billing idempotency key.
package requesttrace

import "context"

type Attempt struct {
	RootRequestID   string
	Number          int32
	SourceKind      string
	UpstreamModelID string
}

type contextKey struct{}

func WithAttempt(ctx context.Context, attempt Attempt) context.Context {
	return context.WithValue(ctx, contextKey{}, attempt)
}

func FromContext(ctx context.Context) Attempt {
	attempt, _ := ctx.Value(contextKey{}).(Attempt)
	return attempt
}

package server

import (
	"context"
	"errors"

	relaybiz "micro-one-api/internal/biz"
)

// A stream has already started: callers must neither replay it nor append a
// JSON error response. Preserve the cause for cancellation/timeout reporting.
var errRelayStreamInterrupted = errors.New("relay stream interrupted")

func relayStreamInterrupted(ctx context.Context, cause error) error {
	if err := ctx.Err(); err != nil {
		cause = err
	}
	result := "stream_error"
	switch {
	case errors.Is(cause, context.Canceled):
		result = "canceled"
	case errors.Is(cause, context.DeadlineExceeded):
		result = "timeout"
	}
	setRelayObservationResult(ctx, result)
	return relaybiz.MarkPostForwardError(errors.Join(errRelayStreamInterrupted, cause))
}

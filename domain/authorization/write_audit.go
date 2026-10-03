package authorization

import (
	"context"
	"errors"
)

type writeReasonKey struct{}

// WithWriteReason carries only a human justification; resource payloads and
// credentials must never be put into the write audit context.
func WithWriteReason(ctx context.Context, reason string) context.Context {
	return context.WithValue(ctx, writeReasonKey{}, reason)
}
func WriteReason(ctx context.Context) string {
	reason, _ := ctx.Value(writeReasonKey{}).(string)
	return reason
}

type expectedResourceRevisionKey struct{}

// WithExpectedResourceRevision carries the caller's resource CAS expectation.
// Zero denotes creation; authorization policy revisions remain independent.
func WithExpectedResourceRevision(ctx context.Context, revision uint64) context.Context {
	return context.WithValue(ctx, expectedResourceRevisionKey{}, revision)
}
func ExpectedResourceRevision(ctx context.Context) (uint64, bool) {
	revision, ok := ctx.Value(expectedResourceRevisionKey{}).(uint64)
	return revision, ok
}

// ErrWriteStorageUnavailable prevents an IAM mutation from reporting success
// when its repository cannot persist the mandatory write audit.
var ErrWriteStorageUnavailable = errors.New("durable resource write storage unavailable")

func RequireDurableWrite(ctx context.Context, operations ...string) error {
	for _, op := range operations {
		if _, iam := QueryScopeFromContext(ctx, op); iam {
			return ErrWriteStorageUnavailable
		}
	}
	return nil
}

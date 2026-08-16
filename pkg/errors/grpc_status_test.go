package errors

import (
	"testing"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

// TestError_GRPCStatus pins the wire contract for routing dead-ends: they
// must cross gRPC as NON-retryable codes (NotFound / FailedPrecondition),
// never Unavailable or Unknown — relay-gateway's circuit breaker ignores
// non-retryable codes when accounting upstream health, so a storm of
// "no available channel" results cannot trip it again (2026-08-16 incident).
// Unlisted reasons keep the historical codes.Unknown.
func TestError_GRPCStatus(t *testing.T) {
	cases := []struct {
		name   string
		reason string
		want   codes.Code
	}{
		{"channel not found is NotFound", ReasonChannelNotFound, codes.NotFound},
		{"route dead end is FailedPrecondition", ReasonRouteDeadEnd, codes.FailedPrecondition},
		{"unknown reason stays Unknown", ReasonUnknown, codes.Unknown},
		{"unlisted reason stays Unknown", ReasonModelNotFound, codes.Unknown},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := &Error{Reason: tc.reason, Message: "boom"}
			st, ok := status.FromError(err)
			if !ok {
				t.Fatalf("status.FromError ok = false for %v", err)
			}
			if st.Code() != tc.want {
				t.Fatalf("code = %v, want %v", st.Code(), tc.want)
			}
			if st.Message() != "boom" {
				t.Fatalf("message = %q, want %q", st.Message(), "boom")
			}
		})
	}
}

// TestError_GRPCStatus_Wrapped ensures status.FromError still resolves the
// code when the structured error is wrapped (relay biz layers wrap errors
// with fmt.Errorf %w before they reach the HTTP boundary).
func TestError_GRPCStatus_Wrapped(t *testing.T) {
	base := &Error{Reason: ReasonChannelNotFound, Message: "no available channel"}
	wrapped := &wrapError{err: base}
	st, ok := status.FromError(wrapped)
	if !ok || st.Code() != codes.NotFound {
		t.Fatalf("wrapped: code = %v ok = %v, want NotFound", st.Code(), ok)
	}
}

type wrapError struct{ err error }

func (w *wrapError) Error() string { return "wrapped: " + w.err.Error() }
func (w *wrapError) Unwrap() error { return w.err }

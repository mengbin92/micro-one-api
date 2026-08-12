package grpc

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/sony/gobreaker"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

func TestIsRetryableError_Table(t *testing.T) {
	tests := []struct {
		name string
		err  error
		want bool
	}{
		{"nil is not retryable", nil, false},
		{"plain error is retryable", errors.New("boom"), true},
		{"OK not retryable", status.Error(codes.OK, ""), false},
		{"Canceled not retryable", status.Error(codes.Canceled, ""), false},
		{"InvalidArgument not retryable", status.Error(codes.InvalidArgument, ""), false},
		{"NotFound not retryable", status.Error(codes.NotFound, ""), false},
		{"AlreadyExists not retryable", status.Error(codes.AlreadyExists, ""), false},
		{"PermissionDenied not retryable", status.Error(codes.PermissionDenied, ""), false},
		{"Unauthenticated not retryable", status.Error(codes.Unauthenticated, ""), false},
		{"ResourceExhausted not retryable", status.Error(codes.ResourceExhausted, ""), false},
		{"FailedPrecondition not retryable", status.Error(codes.FailedPrecondition, ""), false},
		{"OutOfRange not retryable", status.Error(codes.OutOfRange, ""), false},
		{"Unimplemented not retryable", status.Error(codes.Unimplemented, ""), false},
		{"DataLoss not retryable", status.Error(codes.DataLoss, ""), false},
		{"DeadlineExceeded retryable", status.Error(codes.DeadlineExceeded, ""), true},
		{"Aborted retryable", status.Error(codes.Aborted, ""), true},
		{"Unavailable retryable", status.Error(codes.Unavailable, ""), true},
		{"Unknown retryable", status.Error(codes.Unknown, ""), true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.want, isRetryableError(tt.err))
		})
	}
}

func TestTypedRejectFallback_SentinelMatchable(t *testing.T) {
	// relay-H1: callers branch with errors.Is on ErrCircuitBreakerOpen.
	fb := TypedRejectFallback[any]()
	cause := errors.New("underlying breaker error")
	_, err := fb(context.Background(), cause)
	require.Error(t, err)
	assert.True(t, errors.Is(err, ErrCircuitBreakerOpen), "sentinel must be matchable with errors.Is")
	assert.True(t, errors.Is(err, cause), "original error must be preserved via errors.Join")
}

func TestStateToGauge(t *testing.T) {
	assert.Equal(t, 0.0, stateToGauge(gobreaker.StateClosed))
	assert.Equal(t, 1.0, stateToGauge(gobreaker.StateHalfOpen))
	assert.Equal(t, 2.0, stateToGauge(gobreaker.StateOpen))
	assert.Equal(t, 0.0, stateToGauge(gobreaker.State(99)))
}

func TestDefaultReadyToTrip(t *testing.T) {
	assert.False(t, DefaultReadyToTrip(gobreaker.Counts{Requests: 4, TotalFailures: 4}), "under 5 requests must not trip")
	assert.False(t, DefaultReadyToTrip(gobreaker.Counts{Requests: 5, TotalFailures: 2}), "low failure ratio must not trip")
	assert.True(t, DefaultReadyToTrip(gobreaker.Counts{Requests: 5, TotalFailures: 3}), "5 requests with >= 0.6 failure ratio trips")
}

// newTripFastClient builds a ResilientClient whose breaker trips on the FIRST
// retryable failure. NewResilientClient hardwires the platform IsSuccessful
// policy (non-retryable errors count as successes — platform-H1), so this is
// exactly what production uses; no breaker override is needed.
//
// openTimeout is the open→half-open wait. Tests that assert "fn must not run
// while open" pass a LONG timeout so the breaker stays open for the whole test
// even on a slow CI runner; the recovery test passes a short one.
func newTripFastClient(name string, openTimeout time.Duration, fallback FallbackFunc[any]) *ResilientClient[any] {
	return NewResilientClient[any](nil, &BreakerConfig{
		Name:        name,
		MaxRequests: 1,
		Timeout:     openTimeout,
		ReadyToTrip: func(counts gobreaker.Counts) bool {
			return counts.Requests >= 1 && counts.TotalFailures >= 1
		},
	}, time.Second, fallback)
}

func TestResilientClient_NonRetryableErrorsDoNotTrip(t *testing.T) {
	// platform-H1: a wave of bad API keys (Unauthenticated) must not trip the
	// breaker, otherwise all traffic to identity is rejected.
	rc := newTripFastClient("identity", 10*time.Minute, TypedRejectFallback[any]())

	for i := 0; i < 10; i++ {
		_, err := rc.Execute(context.Background(), func(ctx context.Context, client any) (any, error) {
			return nil, status.Error(codes.Unauthenticated, "bad key")
		})
		require.Error(t, err)
	}
	assert.Equal(t, gobreaker.StateClosed, rc.State(),
		"non-retryable client errors must not trip the breaker")
}

func TestResilientClient_RetryableFailuresTripAndFallback(t *testing.T) {
	rc := newTripFastClient("billing", 10*time.Minute, TypedRejectFallback[any]())

	// First failure trips the breaker (retryable Unavailable).
	_, err := rc.Execute(context.Background(), func(ctx context.Context, client any) (any, error) {
		return nil, status.Error(codes.Unavailable, "down")
	})
	require.Error(t, err)
	require.True(t, errors.Is(err, ErrCircuitBreakerOpen),
		"while open the fallback must reject with the sentinel (relay-H1)")

	// The breaker is now open: subsequent calls go straight to fallback and
	// must never invoke fn.
	_, err = rc.Execute(context.Background(), func(ctx context.Context, client any) (any, error) {
		t.Fatal("fn must not be invoked while the breaker is open")
		return nil, nil
	})
	require.Error(t, err)
	assert.True(t, errors.Is(err, ErrCircuitBreakerOpen))
}

func TestResilientClient_SuccessRecovers(t *testing.T) {
	// Short open timeout so the breaker moves to half-open within the test.
	rc := newTripFastClient("relay", 20*time.Millisecond, TypedRejectFallback[any]())

	// Trip it.
	_, err := rc.Execute(context.Background(), func(ctx context.Context, client any) (any, error) {
		return nil, status.Error(codes.Unavailable, "down")
	})
	require.True(t, errors.Is(err, ErrCircuitBreakerOpen))

	// After the open timeout (~20ms), a successful call recovers to closed.
	require.Eventually(t, func() bool {
		_, err := rc.Execute(context.Background(), func(ctx context.Context, client any) (any, error) {
			return "ok", nil
		})
		return err == nil && rc.State() == gobreaker.StateClosed
	}, 2*time.Second, 5*time.Millisecond)
}

func TestResilientClient_NoFallback_FormatsOpenError(t *testing.T) {
	rc := newTripFastClient("notify", 10*time.Minute, nil) // no fallback

	_, err := rc.Execute(context.Background(), func(ctx context.Context, client any) (any, error) {
		return nil, status.Error(codes.Unavailable, "down")
	})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "circuit breaker open for notify")
}

func TestFallbackStrategyLabel_Default(t *testing.T) {
	rc := NewResilientClient[any](nil, &BreakerConfig{Name: "x"}, time.Second, nil)
	assert.Equal(t, FallbackReject, rc.fallbackStrategyLabel(), "empty strategy defaults to reject")
	rc2 := NewResilientClient[any](nil, &BreakerConfig{Name: "x", FallbackStrategy: FallbackCache}, time.Second, nil)
	assert.Equal(t, FallbackCache, rc2.fallbackStrategyLabel())
	assert.Equal(t, "x", rc2.Name())
}

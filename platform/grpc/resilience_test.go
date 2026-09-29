package grpc

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/prometheus/client_golang/prometheus/testutil"
	"github.com/sony/gobreaker"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"micro-one-api/platform/metrics"
)

func TestIsBreakerRejection(t *testing.T) {
	assert.True(t, isBreakerRejection(gobreaker.ErrOpenState))
	assert.True(t, isBreakerRejection(gobreaker.ErrTooManyRequests))
	assert.True(t, isBreakerRejection(fmt.Errorf("wrapped: %w", gobreaker.ErrOpenState)))
	assert.False(t, isBreakerRejection(nil))
	assert.False(t, isBreakerRejection(errors.New("boom")))
	assert.False(t, isBreakerRejection(status.Error(codes.Unavailable, "down")))
}

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
		{"Unknown not retryable (application error, not an upstream-health signal)", status.Error(codes.Unknown, ""), false},
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

	for range 10 {
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

func TestResilientClient_OpenRejectionsCountedAsRejected(t *testing.T) {
	// O5: rejections while the breaker is open never reach the wire and
	// produce no dependency-grpc samples; they must stay visible as the
	// "rejected" outcome instead of inflating "failure".
	rc := newTripFastClient("o5-reject-execute", 10*time.Minute, TypedRejectFallback[any]())
	t.Cleanup(func() {
		metrics.CircuitBreakerRequests.DeleteLabelValues("o5-reject-execute", "rejected")
		metrics.CircuitBreakerRequests.DeleteLabelValues("o5-reject-execute", "failure")
		metrics.CircuitBreakerFailures.DeleteLabelValues("o5-reject-execute")
	})
	rejected := metrics.CircuitBreakerRequests.WithLabelValues("o5-reject-execute", "rejected")

	_, err := rc.Execute(context.Background(), func(ctx context.Context, client any) (any, error) {
		return nil, status.Error(codes.Unavailable, "down")
	})
	require.Error(t, err)
	require.Equal(t, gobreaker.StateOpen, rc.State())
	assert.Equal(t, float64(0), testutil.ToFloat64(rejected),
		"the tripping call reached the wire; it is a failure, not a rejection")

	for range 3 {
		_, err = rc.Execute(context.Background(), func(ctx context.Context, client any) (any, error) {
			t.Fatal("fn must not be invoked while the breaker is open")
			return nil, nil
		})
		require.ErrorIs(t, err, ErrCircuitBreakerOpen)
	}
	assert.Equal(t, float64(3), testutil.ToFloat64(rejected))
	assert.Equal(t, float64(1), testutil.ToFloat64(
		metrics.CircuitBreakerRequests.WithLabelValues("o5-reject-execute", "failure")))
	assert.Equal(t, float64(1), testutil.ToFloat64(
		metrics.CircuitBreakerFailures.WithLabelValues("o5-reject-execute")),
		"rejections must not inflate the failure counter")
}

func TestUnaryClientInterceptor_OpenRejectionsCountedAsRejected(t *testing.T) {
	t.Cleanup(func() {
		metrics.CircuitBreakerRequests.DeleteLabelValues("o5-reject-interceptor", "rejected")
		metrics.CircuitBreakerRequests.DeleteLabelValues("o5-reject-interceptor", "failure")
		metrics.CircuitBreakerFailures.DeleteLabelValues("o5-reject-interceptor")
	})
	breaker := gobreaker.NewCircuitBreaker(gobreaker.Settings{
		Name:        "o5-reject-interceptor",
		MaxRequests: 1,
		Timeout:     10 * time.Minute,
		ReadyToTrip: func(counts gobreaker.Counts) bool {
			return counts.Requests >= 1 && counts.TotalFailures >= 1
		},
		IsSuccessful: func(err error) bool { return err == nil || !isRetryableError(err) },
	})
	interceptor := UnaryClientInterceptor("o5-reject-interceptor", breaker)
	invocations := 0
	invoker := func(ctx context.Context, method string, req, reply any, cc *grpc.ClientConn, opts ...grpc.CallOption) error {
		invocations++
		return status.Error(codes.Unavailable, "down")
	}

	require.Error(t, interceptor(context.Background(), "/test/Method", nil, nil, nil, invoker))
	require.Equal(t, gobreaker.StateOpen, breaker.State())
	for range 2 {
		err := interceptor(context.Background(), "/test/Method", nil, nil, nil, invoker)
		require.ErrorIs(t, err, gobreaker.ErrOpenState)
	}
	assert.Equal(t, 1, invocations, "an open breaker must not invoke the RPC")
	assert.Equal(t, float64(2), testutil.ToFloat64(
		metrics.CircuitBreakerRequests.WithLabelValues("o5-reject-interceptor", "rejected")))
	assert.Equal(t, float64(1), testutil.ToFloat64(
		metrics.CircuitBreakerRequests.WithLabelValues("o5-reject-interceptor", "failure")))
	assert.Equal(t, float64(1), testutil.ToFloat64(
		metrics.CircuitBreakerFailures.WithLabelValues("o5-reject-interceptor")))
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

func TestServiceBreakerOverridesAndRecovery(t *testing.T) {
	for _, service := range []string{"identity", "channel", "billing", "log"} {
		t.Run(service, func(t *testing.T) {
			prefix := "GRPC_" + strings.ToUpper(service) + "_"
			t.Setenv(prefix+"BREAKER_MIN_REQUESTS", "2")
			t.Setenv(prefix+"BREAKER_FAILURE_RATIO", "1")
			t.Setenv(prefix+"BREAKER_COOLDOWN", "20ms")
			t.Setenv(prefix+"TIMEOUT", "50ms")
			cfg, budget, err := ServiceBreakerConfig(service, time.Second)
			require.NoError(t, err)
			require.Equal(t, 50*time.Millisecond, budget)
			require.Equal(t, FallbackReject, cfg.FallbackStrategy)
			cfg.MaxRequests = 1
			rc := NewResilientClient(struct{}{}, cfg, budget, TypedRejectFallback[struct{}]())
			failure := func(context.Context, struct{}) (any, error) { return nil, status.Error(codes.Unavailable, "outage") }
			for range 2 {
				_, _ = rc.Execute(context.Background(), failure)
			}
			require.Equal(t, gobreaker.StateOpen, rc.State())
			_, err = rc.Execute(context.Background(), failure)
			require.ErrorIs(t, err, ErrCircuitBreakerOpen)
			time.Sleep(30 * time.Millisecond)
			_, err = rc.Execute(context.Background(), func(context.Context, struct{}) (any, error) { return "recovered", nil })
			require.NoError(t, err)
			require.Equal(t, gobreaker.StateClosed, rc.State())
			for range 4 {
				ctx, cancel := context.WithCancel(context.Background())
				cancel()
				_, _ = rc.Execute(ctx, func(ctx context.Context, _ struct{}) (any, error) { return nil, ctx.Err() })
			}
			require.Equal(t, gobreaker.StateClosed, rc.State(), "caller cancellation must not trip dependency")
		})
	}
}
func TestServiceBreakerRejectsInvalidOverrides(t *testing.T) {
	for _, tc := range []struct{ key, value string }{{"BREAKER_MIN_REQUESTS", "0"}, {"BREAKER_FAILURE_RATIO", "NaN"}, {"BREAKER_COOLDOWN", "2h"}, {"TIMEOUT", "0s"}} {
		t.Run(tc.key, func(t *testing.T) {
			t.Setenv("GRPC_IDENTITY_"+tc.key, tc.value)
			require.Error(t, ValidateServiceBreakerEnvironment())
		})
	}
}

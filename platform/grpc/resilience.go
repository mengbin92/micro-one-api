package grpc

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"time"

	"github.com/sony/gobreaker"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"micro-one-api/platform/metrics"
)

// BreakerConfig holds circuit breaker configuration for a service.
type BreakerConfig struct {
	Name             string
	MaxRequests      uint32        // max requests allowed when half-open
	Interval         time.Duration // cyclic period of closed state
	Timeout          time.Duration // open → half-open wait time
	ReadyToTrip      ReadyToTripFunc
	OnStateChange    StateChangeCallback
	FallbackStrategy FallbackStrategy
}

// ReadyToTripFunc is called when a request fails in closed state.
// If it returns true, the circuit breaker trips to open state.
type ReadyToTripFunc func(counts gobreaker.Counts) bool

// StateChangeCallback is called when the circuit breaker state changes.
type StateChangeCallback func(name string, from gobreaker.State, to gobreaker.State)

// FallbackStrategy defines the fallback behavior when the breaker is open.
type FallbackStrategy string

const (
	FallbackCache  FallbackStrategy = "cache"  // Use cached data
	FallbackAsync  FallbackStrategy = "async"  // Use async mode
	FallbackNoOp   FallbackStrategy = "noop"   // Do nothing
	FallbackReject FallbackStrategy = "reject" // Reject immediately
)

// DefaultBreakerConfig returns the default circuit breaker configuration.
func DefaultBreakerConfig(name string) *BreakerConfig {
	return &BreakerConfig{
		Name:             name,
		MaxRequests:      3,
		Interval:         60 * time.Second,
		Timeout:          30 * time.Second,
		ReadyToTrip:      DefaultReadyToTrip,
		FallbackStrategy: FallbackCache,
	}
}

// DefaultReadyToTrip trips the breaker after 5 consecutive failures.
func DefaultReadyToTrip(counts gobreaker.Counts) bool {
	failureRatio := float64(counts.TotalFailures) / float64(counts.Requests)
	return counts.Requests >= 5 && failureRatio >= 0.6
}

// FallbackFunc is called when the circuit breaker is open.
type FallbackFunc[T any] func(ctx context.Context, err error) (T, error)

// ErrCircuitBreakerOpen is the sentinel returned by RejectFallback when the
// circuit breaker for a downstream service is open. Callers can branch on it
// with errors.Is to degrade gracefully instead of receiving a wrapped,
// message-bearing "circuit breaker open for <svc>" error (relay-H1: the four
// production ResilientClients previously passed nil as the fallback, so the
// only signal was an opaque formatted string).
var ErrCircuitBreakerOpen = errors.New("circuit breaker open")

// TypedRejectFallback returns a FallbackFunc that rejects the call with the typed
// ErrCircuitBreakerOpen sentinel, preserving the original breaker error via
// errors.Join so it is still available for logging while callers match on the
// sentinel (relay-H1).
func TypedRejectFallback[T any]() FallbackFunc[T] {
	return func(ctx context.Context, err error) (T, error) {
		var zero T
		return zero, errors.Join(ErrCircuitBreakerOpen, err)
	}
}

// ResilientClient wraps a gRPC client with circuit breaker, timeout, and fallback.
type ResilientClient[T any] struct {
	client           T
	breaker          *gobreaker.CircuitBreaker
	timeout          time.Duration
	fallback         FallbackFunc[T]
	serviceName      string
	fallbackStrategy FallbackStrategy
	mu               sync.RWMutex
}

// NewResilientClient creates a new resilient gRPC client wrapper.
func NewResilientClient[T any](
	client T,
	cfg *BreakerConfig,
	timeout time.Duration,
	fallback FallbackFunc[T],
) *ResilientClient[T] {
	if cfg == nil {
		cfg = DefaultBreakerConfig("default")
	}

	settings := gobreaker.Settings{
		Name:        cfg.Name,
		MaxRequests: cfg.MaxRequests,
		Interval:    cfg.Interval,
		Timeout:     cfg.Timeout,
		ReadyToTrip: cfg.ReadyToTrip,
		// platform-H1: a non-retryable error (invalid token, not found, bad
		// request) is a client problem, not an upstream-health signal. Counting
		// it as a breaker failure lets a wave of bad API keys trip the identity
		// breaker and reject ALL traffic. gobreaker's IsSuccessful gates its own
		// afterRequest(onFailure) — returning true for non-retryable errors means
		// only retryable failures (network, deadline, unavailable) move the
		// breaker toward open.
		IsSuccessful: func(err error) bool {
			return err == nil || !isRetryableError(err)
		},
		OnStateChange: func(name string, from gobreaker.State, to gobreaker.State) {
			// Update metrics
			state := stateToGauge(to)
			metrics.CircuitBreakerState.WithLabelValues(name).Set(state)
			// platform-L5: a trip is a transition INTO open, not every request
			// rejected while open (which inflated the metric hugely).
			if to == gobreaker.StateOpen {
				metrics.CircuitBreakerTrips.WithLabelValues(name).Inc()
			}

			if cfg.OnStateChange != nil {
				cfg.OnStateChange(name, from, to)
			}
		},
	}

	breaker := gobreaker.NewCircuitBreaker(settings)

	return &ResilientClient[T]{
		client:           client,
		breaker:          breaker,
		timeout:          timeout,
		fallback:         fallback,
		serviceName:      cfg.Name,
		fallbackStrategy: cfg.FallbackStrategy,
	}
}

// Execute runs the given function with circuit breaker protection.
func (rc *ResilientClient[T]) Execute(
	ctx context.Context,
	fn func(ctx context.Context, client T) (any, error),
) (any, error) {
	// Record breaker state before execution
	state := rc.breaker.State()
	metrics.CircuitBreakerState.WithLabelValues(rc.serviceName).Set(stateToGauge(state))

	result, err := rc.breaker.Execute(func() (any, error) {
		// Apply timeout if configured
		if rc.timeout > 0 {
			var cancel context.CancelFunc
			ctx, cancel = context.WithTimeout(ctx, rc.timeout)
			defer cancel()
		}

		// Execute the function. gobreaker decides success/failure for breaker
		// accounting via the IsSuccessful setting (platform-H1): non-retryable
		// errors are treated as successes so client-error storms cannot trip the
		// breaker. The outcome metrics are recorded below from the breaker result.
		resp, err := fn(ctx, rc.client)
		return resp, err
	})

	if err != nil {
		// platform-L5: distinguish client (non-retryable) errors from real
		// upstream failures so the "failure" outcome only reflects breaker-
		// relevant failures.
		if !isRetryableError(err) {
			metrics.CircuitBreakerRequests.WithLabelValues(rc.serviceName, "client_error").Inc()
		} else {
			metrics.CircuitBreakerRequests.WithLabelValues(rc.serviceName, "failure").Inc()
			metrics.CircuitBreakerFailures.WithLabelValues(rc.serviceName).Inc()
		}

		// Check if breaker is open
		if rc.breaker.State() == gobreaker.StateOpen {
			// Try fallback
			if rc.fallback != nil {
				metrics.FallbackActivation.WithLabelValues(rc.serviceName, string(rc.fallbackStrategyLabel())).Inc()
				return rc.fallback(ctx, err)
			}

			return nil, fmt.Errorf("circuit breaker open for %s: %w", rc.serviceName, err)
		}

		return nil, err
	}

	return result, nil
}

// stateToGauge converts gobreaker.State to metric gauge value.
func stateToGauge(state gobreaker.State) float64 {
	switch state {
	case gobreaker.StateClosed:
		return 0
	case gobreaker.StateHalfOpen:
		return 1
	case gobreaker.StateOpen:
		return 2
	default:
		return 0
	}
}

// fallbackStrategyLabel returns the configured fallback strategy for this
// client (platform-L5: previously hardcoded to FallbackCache for every service).
func (rc *ResilientClient[T]) fallbackStrategyLabel() FallbackStrategy {
	if rc.fallbackStrategy != "" {
		return rc.fallbackStrategy
	}
	return FallbackReject
}

// State returns the current state of the circuit breaker.
func (rc *ResilientClient[T]) State() gobreaker.State {
	return rc.breaker.State()
}

// Name returns the service name of this resilient client.
func (rc *ResilientClient[T]) Name() string {
	return rc.serviceName
}

// isRetryableError checks if an error should be considered for circuit breaker.
func isRetryableError(err error) bool {
	if err == nil {
		return false
	}

	st, ok := status.FromError(err)
	if !ok {
		// Non-gRPC errors are considered retryable
		return true
	}

	switch st.Code() {
	case codes.OK:
		return false
	case codes.Canceled:
		return false
	case codes.InvalidArgument:
		return false
	case codes.NotFound:
		return false
	case codes.AlreadyExists:
		return false
	case codes.PermissionDenied:
		return false
	case codes.Unauthenticated:
		return false
	case codes.ResourceExhausted:
		return false // Rate limiting is retryable
	case codes.FailedPrecondition:
		return false
	case codes.OutOfRange:
		return false
	case codes.Unimplemented:
		return false
	case codes.DeadlineExceeded:
		return true
	case codes.Aborted:
		return true
	case codes.Unavailable:
		return true
	case codes.DataLoss:
		return false
	case codes.Unknown:
		// Application-level errors from our own services historically cross
		// gRPC as codes.Unknown (e.g. "no available channel" routing
		// dead-ends). They are not upstream-health signals: counting them let
		// a storm of unroutable-model requests trip the breaker and reject
		// ALL traffic to the service (2026-08-16 incident). Genuine transport
		// failures arrive as Unavailable/DeadlineExceeded, which stay
		// retryable below.
		//
		// NOTE: this is a GLOBAL semantic change, not channel-service-only.
		// Every service whose errors cross gRPC as Unknown (identity, billing,
		// config, ...) is now treated as "not an upstream-health signal" and
		// never trips the breaker. That matches platform-H1's intent (client-
		// side problems must not trip breakers); real transport failures still
		// surface as Unavailable/DeadlineExceeded and remain protected. Do not
		// flip this back to retryable without auditing every Unknown producer.
		return false
	default:
		return true
	}
}

// UnaryClientInterceptor returns a grpc.UnaryClientInterceptor with circuit breaker.
func UnaryClientInterceptor(serviceName string, breaker *gobreaker.CircuitBreaker) grpc.UnaryClientInterceptor {
	return func(
		ctx context.Context,
		method string,
		req, reply any,
		cc *grpc.ClientConn,
		invoker grpc.UnaryInvoker,
		opts ...grpc.CallOption,
	) error {
		// Record breaker state
		state := breaker.State()
		metrics.CircuitBreakerState.WithLabelValues(serviceName).Set(stateToGauge(state))

		// Execute with breaker protection. The breaker must be constructed with an
		// IsSuccessful that treats non-retryable errors as successes (platform-H1);
		// here we only record outcome metrics consistently (platform-L5).
		_, err := breaker.Execute(func() (any, error) {
			err := invoker(ctx, method, req, reply, cc, opts...)
			return nil, err
		})

		if err != nil {
			if isRetryableError(err) {
				metrics.CircuitBreakerFailures.WithLabelValues(serviceName).Inc()
				metrics.CircuitBreakerRequests.WithLabelValues(serviceName, "failure").Inc()
			} else {
				metrics.CircuitBreakerRequests.WithLabelValues(serviceName, "client_error").Inc()
			}
		} else {
			metrics.CircuitBreakerRequests.WithLabelValues(serviceName, "success").Inc()
		}
		// platform-L5: trips are counted in the breaker's OnStateChange, not here.

		return err
	}
}

// NewCircuitBreaker creates a new circuit breaker with default settings.
func NewCircuitBreaker(name string) *gobreaker.CircuitBreaker {
	cfg := DefaultBreakerConfig(name)
	settings := gobreaker.Settings{
		Name:        cfg.Name,
		MaxRequests: cfg.MaxRequests,
		Interval:    cfg.Interval,
		Timeout:     cfg.Timeout,
		ReadyToTrip: cfg.ReadyToTrip,
		OnStateChange: func(name string, from gobreaker.State, to gobreaker.State) {
			metrics.CircuitBreakerState.WithLabelValues(name).Set(stateToGauge(to))
		},
	}
	return gobreaker.NewCircuitBreaker(settings)
}

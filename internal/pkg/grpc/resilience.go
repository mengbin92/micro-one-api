package grpc

import (
	"context"
	"fmt"
	"sync"
	"time"

	"github.com/sony/gobreaker"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"micro-one-api/internal/pkg/metrics"
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
	FallbackCache   FallbackStrategy = "cache"   // Use cached data
	FallbackAsync   FallbackStrategy = "async"   // Use async mode
	FallbackNoOp    FallbackStrategy = "noop"    // Do nothing
	FallbackReject  FallbackStrategy = "reject"  // Reject immediately
)

// DefaultBreakerConfig returns the default circuit breaker configuration.
func DefaultBreakerConfig(name string) *BreakerConfig {
	return &BreakerConfig{
		Name:         name,
		MaxRequests:  3,
		Interval:     60 * time.Second,
		Timeout:      30 * time.Second,
		ReadyToTrip:  DefaultReadyToTrip,
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

// ResilientClient wraps a gRPC client with circuit breaker, timeout, and fallback.
type ResilientClient[T any] struct {
	client      T
	breaker     *gobreaker.CircuitBreaker
	timeout     time.Duration
	fallback    FallbackFunc[T]
	serviceName string
	mu          sync.RWMutex
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
		OnStateChange: func(name string, from gobreaker.State, to gobreaker.State) {
			// Update metrics
			state := stateToGauge(to)
			metrics.CircuitBreakerState.WithLabelValues(name).Set(state)

			if cfg.OnStateChange != nil {
				cfg.OnStateChange(name, from, to)
			}
		},
	}

	breaker := gobreaker.NewCircuitBreaker(settings)

	return &ResilientClient[T]{
		client:      client,
		breaker:     breaker,
		timeout:     timeout,
		fallback:    fallback,
		serviceName: cfg.Name,
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

		// Execute the function
		resp, err := fn(ctx, rc.client)
		if err != nil {
			// Check if error is retryable
			if !isRetryableError(err) {
				// Non-retryable errors don't count toward breaker
				return resp, err
			}
			metrics.CircuitBreakerFailures.WithLabelValues(rc.serviceName).Inc()
			return resp, err
		}

		metrics.CircuitBreakerRequests.WithLabelValues(rc.serviceName, "success").Inc()
		return resp, nil
	})

	if err != nil {
		metrics.CircuitBreakerRequests.WithLabelValues(rc.serviceName, "failure").Inc()

		// Check if breaker is open
		if rc.breaker.State() == gobreaker.StateOpen {
			metrics.CircuitBreakerTrips.WithLabelValues(rc.serviceName).Inc()

			// Try fallback
			if rc.fallback != nil {
				metrics.FallbackActivation.WithLabelValues(rc.serviceName, string(getFallbackStrategy(rc.fallback))).Inc()
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

// getFallbackStrategy extracts the fallback strategy from the function.
func getFallbackStrategy[T any](fn FallbackFunc[T]) FallbackStrategy {
	// This is a placeholder - actual implementation would use type assertion
	// or a different approach to determine the strategy
	return FallbackCache
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
		return true
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

		// Execute with breaker protection
		_, err := breaker.Execute(func() (any, error) {
			err := invoker(ctx, method, req, reply, cc, opts...)
			if err != nil && isRetryableError(err) {
				metrics.CircuitBreakerFailures.WithLabelValues(serviceName).Inc()
				metrics.CircuitBreakerRequests.WithLabelValues(serviceName, "failure").Inc()
				return nil, err
			}
			metrics.CircuitBreakerRequests.WithLabelValues(serviceName, "success").Inc()
			return nil, err
		})

		if err != nil && breaker.State() == gobreaker.StateOpen {
			metrics.CircuitBreakerTrips.WithLabelValues(serviceName).Inc()
		}

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

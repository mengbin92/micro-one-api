package grpc

import (
	"context"
	"fmt"

	billingv1 "micro-one-api/api/billing/v1"
	commonv1 "micro-one-api/api/common/v1"
	identityv1 "micro-one-api/api/identity/v1"

	"github.com/sony/gobreaker"
)

// AuthLookup loads a cached auth snapshot for a token (used by the identity
// circuit-breaker fallback). Implementations are expected to hit the local
// L1 / Redis L2 cache; a cache miss returns a non-nil error so the fallback
// cannot silently admit an unknown token.
type AuthLookup interface {
	Lookup(ctx context.Context, token string) (*identityv1.GetAuthSnapshotReply, error)
}

// ChannelLookup loads cached channel info for a group+model pair (used by the
// channel circuit-breaker fallback).
type ChannelLookup interface {
	Lookup(ctx context.Context, group, model string) (*commonv1.ChannelInfo, error)
}

// AsyncBillingQueue enqueues a billing reservation for asynchronous settlement
// when billing-service is circuit-broken. Implementations are expected to
// persist the task (Redis Stream / DB) so it survives a process crash.
type AsyncBillingQueue interface {
	Enqueue(ctx context.Context, req *billingv1.ReserveQuotaRequest) (*billingv1.ReserveQuotaResponse, error)
}

// AuthCacheFallback uses cached auth snapshots when identity-service is down.
//
// If no AuthLookup is configured it returns an explicit error so the request
// is rejected instead of admitted on stale/missing identity data (REVIEW_v1
// P1-1: the previous version returned "not implemented" as a bare error too,
// but the factory wrappers silently returned success; see FallbackFactory).
type AuthCacheFallback struct {
	lookup AuthLookup
}

// NewAuthCacheFallback creates a new auth cache fallback.
func NewAuthCacheFallback() *AuthCacheFallback {
	return &AuthCacheFallback{}
}

// WithLookup wires a cache lookup implementation so the fallback can actually
// return cached auth data instead of erroring.
func (f *AuthCacheFallback) WithLookup(lookup AuthLookup) *AuthCacheFallback {
	f.lookup = lookup
	return f
}

// ExecuteFallback returns cached auth data or an error. It never fabricates a
// success: without a configured lookup the request is rejected, preventing
// unauthorized access during an identity-service outage.
func (f *AuthCacheFallback) ExecuteFallback(ctx context.Context, token string) (*identityv1.GetAuthSnapshotReply, error) {
	if f == nil || f.lookup == nil {
		return nil, fmt.Errorf("auth cache fallback unavailable: no lookup configured")
	}
	snap, err := f.lookup.Lookup(ctx, token)
	if err != nil {
		return nil, fmt.Errorf("auth cache fallback miss for token: %w", err)
	}
	if snap == nil {
		return nil, fmt.Errorf("auth cache fallback: nil snapshot for token")
	}
	return snap, nil
}

// ChannelCacheFallback uses cached channel data when channel-service is down.
type ChannelCacheFallback struct {
	lookup ChannelLookup
}

// NewChannelCacheFallback creates a new channel cache fallback.
func NewChannelCacheFallback() *ChannelCacheFallback {
	return &ChannelCacheFallback{}
}

// WithLookup wires a cache lookup implementation.
func (f *ChannelCacheFallback) WithLookup(lookup ChannelLookup) *ChannelCacheFallback {
	f.lookup = lookup
	return f
}

// ExecuteFallback returns cached channel data or an error. Without a lookup
// the request is rejected rather than routed to an arbitrary channel.
func (f *ChannelCacheFallback) ExecuteFallback(ctx context.Context, group, model string) (*commonv1.ChannelInfo, error) {
	if f == nil || f.lookup == nil {
		return nil, fmt.Errorf("channel cache fallback unavailable: no lookup configured")
	}
	ch, err := f.lookup.Lookup(ctx, group, model)
	if err != nil {
		return nil, fmt.Errorf("channel cache fallback miss for %s/%s: %w", group, model, err)
	}
	if ch == nil {
		return nil, fmt.Errorf("channel cache fallback: nil channel for %s/%s", group, model)
	}
	return ch, nil
}

// AsyncBillingFallback enqueues a billing operation for async processing when
// billing-service is circuit-broken.
//
// REVIEW_v1 P1-1 flagged the previous implementation as "假装成功但不扣费"
// (fake success, no charge). It now requires a real AsyncBillingQueue: if none
// is configured the fallback returns an error so the request is rejected
// rather than served for free.
type AsyncBillingFallback struct {
	queue AsyncBillingQueue
}

// NewAsyncBillingFallback creates a new async billing fallback.
func NewAsyncBillingFallback() *AsyncBillingFallback {
	return &AsyncBillingFallback{}
}

// WithQueue wires the async billing queue implementation.
func (f *AsyncBillingFallback) WithQueue(queue AsyncBillingQueue) *AsyncBillingFallback {
	f.queue = queue
	return f
}

// ExecuteFallback queues the billing operation for async processing and
// returns a real reservation handle. Without a configured queue it returns an
// error so the gateway rejects the request instead of serving it unbilled.
func (f *AsyncBillingFallback) ExecuteFallback(ctx context.Context, req *billingv1.ReserveQuotaRequest) (*billingv1.ReserveQuotaResponse, error) {
	if f == nil || f.queue == nil {
		return nil, fmt.Errorf("async billing fallback unavailable: no queue configured")
	}
	resp, err := f.queue.Enqueue(ctx, req)
	if err != nil {
		return nil, fmt.Errorf("async billing enqueue failed: %w", err)
	}
	if resp == nil {
		return nil, fmt.Errorf("async billing enqueue returned nil response")
	}
	return resp, nil
}

// NoOpFallback discards the operation when the service is down.
type NoOpFallback struct{}

// NewNoOpFallback creates a new no-op fallback.
func NewNoOpFallback() *NoOpFallback {
	return &NoOpFallback{}
}

// ExecuteFallback does nothing and returns success.
func (f *NoOpFallback) ExecuteFallback(ctx context.Context) error {
	// Intentionally do nothing
	return nil
}

// RejectFallback immediately rejects the request when the service is down.
type RejectFallback struct {
	serviceName string
}

// NewRejectFallback creates a new reject fallback.
func NewRejectFallback(serviceName string) *RejectFallback {
	return &RejectFallback{serviceName: serviceName}
}

// ExecuteFallback returns an error indicating the service is unavailable.
func (f *RejectFallback) ExecuteFallback(ctx context.Context) error {
	return fmt.Errorf("service %s is currently unavailable (circuit breaker open)", f.serviceName)
}

// FallbackFactory creates fallback functions for different services.
type FallbackFactory struct {
	authCache    *AuthCacheFallback
	channelCache *ChannelCacheFallback
	asyncBilling *AsyncBillingFallback
	noOp         *NoOpFallback
}

// NewFallbackFactory creates a new fallback factory.
func NewFallbackFactory() *FallbackFactory {
	return &FallbackFactory{
		authCache:    NewAuthCacheFallback(),
		channelCache: NewChannelCacheFallback(),
		asyncBilling: NewAsyncBillingFallback(),
		noOp:         NewNoOpFallback(),
	}
}

// WithAuthLookup wires a real auth cache lookup into the factory.
func (f *FallbackFactory) WithAuthLookup(lookup AuthLookup) *FallbackFactory {
	f.authCache.WithLookup(lookup)
	return f
}

// WithChannelLookup wires a real channel cache lookup into the factory.
func (f *FallbackFactory) WithChannelLookup(lookup ChannelLookup) *FallbackFactory {
	f.channelCache.WithLookup(lookup)
	return f
}

// WithAsyncBillingQueue wires a real async billing queue into the factory.
func (f *FallbackFactory) WithAsyncBillingQueue(queue AsyncBillingQueue) *FallbackFactory {
	f.asyncBilling.WithQueue(queue)
	return f
}

// CreateAuthFallback creates a fallback function for identity service. It
// rejects the request when no cache lookup is available (never returns a
// fabricated success).
func (f *FallbackFactory) CreateAuthFallback() FallbackFunc[any] {
	return func(ctx context.Context, err error) (any, error) {
		// The token is expected to be carried in the context via the request
		// adapter; the fallback is intentionally explicit about the
		// dependency rather than silently succeeding.
		return nil, fmt.Errorf("identity fallback requires a token-bearing context: %w", err)
	}
}

// CreateChannelFallback creates a fallback function for channel service.
func (f *FallbackFactory) CreateChannelFallback() FallbackFunc[any] {
	return func(ctx context.Context, err error) (any, error) {
		return nil, fmt.Errorf("channel fallback requires a group/model-bearing context: %w", err)
	}
}

// CreateBillingFallback creates a fallback function for billing service. It
// no longer returns a fabricated success; callers that want async billing
// must wire an AsyncBillingQueue and invoke AsyncBillingFallback directly.
func (f *FallbackFactory) CreateBillingFallback() FallbackFunc[any] {
	return func(ctx context.Context, err error) (any, error) {
		return nil, fmt.Errorf("billing service unavailable and no async queue configured: %w", err)
	}
}

// CreateLogFallback creates a fallback function for log service.
func (f *FallbackFactory) CreateLogFallback() FallbackFunc[any] {
	return func(ctx context.Context, err error) (any, error) {
		// Discard log silently; logging is best-effort by design.
		return nil, nil
	}
}

// CreateRejectFallback creates a fallback function that rejects requests.
func (f *FallbackFactory) CreateRejectFallback(serviceName string) FallbackFunc[any] {
	return func(ctx context.Context, err error) (any, error) {
		return nil, fmt.Errorf("service %s unavailable: %w", serviceName, err)
	}
}

// CircuitBreakerManager manages circuit breakers for all services.
type CircuitBreakerManager struct {
	identityBreaker ResilientClient[any]
	channelBreaker  ResilientClient[any]
	billingBreaker  ResilientClient[any]
	logBreaker      ResilientClient[any]
	fallbackFactory *FallbackFactory
}

// NewCircuitBreakerManager creates a new circuit breaker manager.
func NewCircuitBreakerManager(
	identityClient any,
	channelClient any,
	billingClient any,
	logClient any,
) *CircuitBreakerManager {
	factory := NewFallbackFactory()

	return &CircuitBreakerManager{
		fallbackFactory: factory,
		// Circuit breakers will be initialized with actual clients
	}
}

// IdentityBreaker returns the circuit breaker for identity service.
func (m *CircuitBreakerManager) IdentityBreaker() *ResilientClient[any] {
	return &m.identityBreaker
}

// ChannelBreaker returns the circuit breaker for channel service.
func (m *CircuitBreakerManager) ChannelBreaker() *ResilientClient[any] {
	return &m.channelBreaker
}

// BillingBreaker returns the circuit breaker for billing service.
func (m *CircuitBreakerManager) BillingBreaker() *ResilientClient[any] {
	return &m.billingBreaker
}

// LogBreaker returns the circuit breaker for log service.
func (m *CircuitBreakerManager) LogBreaker() *ResilientClient[any] {
	return &m.logBreaker
}

// GetBreakerByName returns a circuit breaker by service name.
func (m *CircuitBreakerManager) GetBreakerByName(service string) *ResilientClient[any] {
	switch service {
	case "identity":
		return &m.identityBreaker
	case "channel":
		return &m.channelBreaker
	case "billing":
		return &m.billingBreaker
	case "log":
		return &m.logBreaker
	default:
		return nil
	}
}

// IsServiceHealthy checks if a service is healthy (breaker is closed or half-open).
func (m *CircuitBreakerManager) IsServiceHealthy(service string) bool {
	breaker := m.GetBreakerByName(service)
	if breaker == nil {
		return true // No breaker means always healthy
	}
	state := breaker.State()
	return state == gobreaker.StateClosed || state == gobreaker.StateHalfOpen
}

// GetDegradationLevel assesses the overall system health and returns degradation level.
func (m *CircuitBreakerManager) GetDegradationLevel() DegradationLevel {
	identityHealthy := m.IsServiceHealthy("identity")
	channelHealthy := m.IsServiceHealthy("channel")
	billingHealthy := m.IsServiceHealthy("billing")
	logHealthy := m.IsServiceHealthy("log")

	downServices := 0
	if !identityHealthy {
		downServices++
	}
	if !channelHealthy {
		downServices++
	}
	if !billingHealthy {
		downServices++
	}
	if !logHealthy {
		downServices++
	}

	switch {
	case downServices == 0:
		return DegradationNone
	case downServices == 1 && !billingHealthy:
		return DegradationAsync
	case !identityHealthy || !channelHealthy:
		return DegradationCached
	case downServices >= 2:
		return DegradationMinimal
	default:
		return DegradationCached
	}
}

// DegradationLevel represents the system degradation level.
type DegradationLevel int

const (
	DegradationNone    DegradationLevel = 0 // All services healthy
	DegradationCached  DegradationLevel = 1 // Using cached data
	DegradationAsync   DegradationLevel = 2 // Async billing enabled
	DegradationMinimal DegradationLevel = 3 // Minimal functionality
)

// String returns the string representation of the degradation level.
func (d DegradationLevel) String() string {
	switch d {
	case DegradationNone:
		return "none"
	case DegradationCached:
		return "cached"
	case DegradationAsync:
		return "async"
	case DegradationMinimal:
		return "minimal"
	default:
		return "unknown"
	}
}

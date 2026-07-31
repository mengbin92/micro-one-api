package grpc

import (
	"context"
	"fmt"

	billingv1 "micro-one-api/api/billing/v1"
	commonv1 "micro-one-api/api/common/v1"
	identityv1 "micro-one-api/api/identity/v1"
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

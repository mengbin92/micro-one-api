package biz

import (
	"context"
	"errors"
	"fmt"
	"math"
	"strings"
	"time"
)

// RetryPolicy defines the retry behavior for upstream provider calls.
type RetryPolicy struct {
	MaxAttempts     int
	InitialInterval time.Duration
	MaxInterval     time.Duration
	Multiplier      float64
	RetryableStatus map[int]bool
}

// DefaultRetryPolicy returns a sensible default retry policy.
func DefaultRetryPolicy() *RetryPolicy {
	return &RetryPolicy{
		MaxAttempts:     3,
		InitialInterval: 500 * time.Millisecond,
		MaxInterval:     5 * time.Second,
		Multiplier:      2.0,
		RetryableStatus: map[int]bool{
			429: true,
			500: true,
			502: true,
			503: true,
		},
	}
}

// RetryableError marks an error as retryable with an associated HTTP status.
type RetryableError struct {
	Status int
	Err    error
}

func (e *RetryableError) Error() string {
	return fmt.Sprintf("status=%d, %v", e.Status, e.Err)
}

func (e *RetryableError) Unwrap() error {
	return e.Err
}

// IsRetryable checks whether an error should trigger a retry attempt.
func (p *RetryPolicy) IsRetryable(err error) bool {
	if err == nil {
		return false
	}

	// Check for RetryableError
	var re *RetryableError
	if AsRetryableError(err, &re) {
		return p.RetryableStatus[re.Status]
	}

	// Check error message for upstream HTTP status patterns
	msg := err.Error()
	if status := extractStatus(msg); status > 0 {
		return p.RetryableStatus[status]
	}

	// Network errors are always retryable
	networkErrors := []string{"connection refused", "timeout", "EOF", "dial tcp"}
	for _, pattern := range networkErrors {
		if strings.Contains(msg, pattern) {
			return true
		}
	}

	return false
}

// BackoffDuration calculates the sleep duration for the given attempt (0-indexed).
func (p *RetryPolicy) BackoffDuration(attempt int) time.Duration {
	d := time.Duration(float64(p.InitialInterval) * math.Pow(p.Multiplier, float64(attempt)))
	if d > p.MaxInterval {
		d = p.MaxInterval
	}
	return d
}

// AsRetryableError is a helper to unwrap RetryableError from the error chain.
func AsRetryableError(err error, target **RetryableError) bool {
	for err != nil {
		if re, ok := err.(*RetryableError); ok {
			*target = re
			return true
		}
		err = unwrap(err)
	}
	return false
}

func unwrap(err error) error {
	type unwrapper interface {
		Unwrap() error
	}
	if u, ok := err.(unwrapper); ok {
		return u.Unwrap()
	}
	return nil
}

func extractStatus(msg string) int {
	if idx := strings.Index(msg, "status="); idx >= 0 {
		statusStr := msg[idx+7:]
		var status int
		for _, c := range statusStr {
			if c >= '0' && c <= '9' {
				status = status*10 + int(c-'0')
			} else {
				break
			}
		}
		return status
	}
	return 0
}

// UpstreamStatus extracts the HTTP status code from an error.
func UpstreamStatus(err error) int {
	if err == nil {
		return 0
	}
	var re *RetryableError
	if AsRetryableError(err, &re) {
		return re.Status
	}
	return extractStatus(err.Error())
}

// ChannelSelector is the interface used by RetryExecutor to select channels.
// It mirrors ChannelClient but adds the excludeFirstPriority parameter for fallback.
type ChannelSelector interface {
	SelectChannel(ctx context.Context, group, model string, excludeFirstPriority bool) (*Channel, error)
	RecordChannelHealth(ctx context.Context, channelID int64, success bool, err string, responseTime int64) error
	// RecordSubscriptionAccountHealth feeds a known subscription-account
	// outcome into the selector. accountID is 0 for ordinary API-key channels.
	RecordSubscriptionAccountHealth(ctx context.Context, accountID int64, success bool) error
}

// RoutingFallbackSelector resolves a fallback source across both source
// namespaces (API-key channels + subscription accounts), excluding every
// source that already failed in the current request. RelayUsecase implements
// it via SelectFallbackRoutingSource.
type RoutingFallbackSelector interface {
	SelectFallbackRoutingSource(ctx context.Context, group, clientModel, resolvedModel string, excluded map[RoutingSourceIdentity]bool) (*Channel, error)
}

// RetryExecutor orchestrates retry attempts with channel fallback.
// It is a biz-layer concern: each retry re-selects a channel (excluding the
// first-priority tier) before calling the upstream provider.
type RetryExecutor struct {
	policy   *RetryPolicy
	selector ChannelSelector
	// fallback, when wired, is the unified cross-namespace fallback used after
	// the request-scoped candidate list is exhausted (sub2api #2). It replaces
	// the coarse excludeFirstPriority tier-skip with per-candidate exclusion.
	fallback RoutingFallbackSelector
}

// NewRetryExecutor creates a RetryExecutor with the given policy and channel selector.
func NewRetryExecutor(policy *RetryPolicy, selector ChannelSelector) *RetryExecutor {
	return &RetryExecutor{
		policy:   policy,
		selector: selector,
	}
}

// WithFallbackSelector wires the unified cross-namespace fallback selector.
// Returns the executor for chaining.
func (e *RetryExecutor) WithFallbackSelector(f RoutingFallbackSelector) *RetryExecutor {
	if e != nil {
		e.fallback = f
	}
	return e
}

// ExecuteResult holds the outcome of a retried operation.
type ExecuteResult struct {
	Channel *Channel
	Err     error
	Attempt int
	// Fallback is true when the executor switched to a DIFFERENT source
	// after the first attempt failed (not merely retried the same source).
	// This is the authoritative signal for routing_fallback_total; callers
	// must NOT infer fallback from Attempt > 0 alone.
	Fallback bool
	// FallbackReason is the low-cardinality cause of the FIRST failure that
	// triggered the source switch ("upstream_5xx", "rate_limited", "network",
	// ...). It is set even when the retried attempt succeeds, so the metric
	// reflects why the switch happened, not the terminal error.
	FallbackReason string
	// FirstErr is the error from the first attempt that triggered the
	// fallback. Nil when no fallback occurred. Used to classify the reason
	// even when later attempts succeed.
	FirstErr error
}

// Execute runs the provided function with retry and channel fallback.
// On each retry (after the first failure), it re-selects a channel with
// excludeFirstPriority=true to avoid hitting the same failing tier.
// The fn receives the selected channel and should return an error if the
// upstream call fails.
func (e *RetryExecutor) Execute(
	ctx context.Context,
	group, model string,
	fn func(ctx context.Context, ch *Channel) error,
) *ExecuteResult {
	return e.ExecuteWithInitialChannel(ctx, group, model, nil, fn)
}

// ExecuteWithAccountHealth runs the provided function with retry and channel
// fallback. It records health for the initial subscription account, whose id
// is known to the caller. Later generic channel fallbacks are not attributed to
// that account. accountID<=0 degrades to plain ExecuteWithInitialChannel.
func (e *RetryExecutor) ExecuteWithAccountHealth(
	ctx context.Context,
	group, model string,
	initialChannel *Channel,
	accountID int64,
	fn func(ctx context.Context, ch *Channel) error,
) *ExecuteResult {
	// The caller only knows the account id for initialChannel. A retry selected
	// through ChannelSelector may be an ordinary API-key channel, so attributing
	// that later result to the original subscription account would corrupt its
	// health score. Account-aware adaptor failover records each selected account
	// separately; this generic path records only the known initial attempt.
	wrapped := func(ctx context.Context, ch *Channel) error {
		err := fn(ctx, ch)
		if ch == initialChannel {
			e.RecordAccountHealth(ctx, accountID, err == nil)
		}
		return err
	}
	return e.ExecuteWithInitialChannel(ctx, group, model, initialChannel, wrapped)
}

// ExecuteWithCandidates runs the retry loop against a RelayPlan, consuming the
// request-scoped candidate list built at Plan time (sub2api #2). Each retry
// first walks the precomputed ordered list with the accumulated exclusion set;
// only when the list is exhausted does it fall back to the unified
// cross-namespace selection (or the legacy tier-skip when no fallback selector
// is wired). accountID health recording matches ExecuteWithAccountHealth.
func (e *RetryExecutor) ExecuteWithCandidates(
	ctx context.Context,
	plan *RelayPlan,
	accountID int64,
	fn func(ctx context.Context, ch *Channel) error,
) *ExecuteResult {
	if plan == nil {
		return &ExecuteResult{Err: fmt.Errorf("relay plan is nil")}
	}
	group, model := "", plan.BaseModel()
	if plan.Auth != nil {
		group = plan.Auth.Group
	}
	initialChannel := plan.Channel
	wrapped := func(ctx context.Context, ch *Channel) error {
		err := fn(ctx, ch)
		if ch == initialChannel {
			e.RecordAccountHealth(ctx, accountID, err == nil)
		}
		return err
	}
	return e.execute(ctx, group, model, initialChannel, plan.Candidates, wrapped)
}

func (e *RetryExecutor) ExecuteWithInitialChannel(
	ctx context.Context,
	group, model string,
	initialChannel *Channel,
	fn func(ctx context.Context, ch *Channel) error,
) *ExecuteResult {
	return e.execute(ctx, group, model, initialChannel, nil, fn)
}

func (e *RetryExecutor) execute(
	ctx context.Context,
	group, model string,
	initialChannel *Channel,
	candidates *RoutingCandidateList,
	fn func(ctx context.Context, ch *Channel) error,
) *ExecuteResult {
	maxAttempts := e.policy.MaxAttempts
	if maxAttempts <= 0 {
		maxAttempts = 1
	}

	var lastErr error
	var lastChannel *Channel
	var firstErr error // first failure that triggered a source switch
	switched := false  // true when executor selected a DIFFERENT channel
	// excluded accumulates every source that failed in this request so retries
	// never re-select it (sub2api #2 request-level exclusion set).
	excluded := make(map[RoutingSourceIdentity]bool)

	for attempt := 0; attempt < maxAttempts; attempt++ {
		// On retry, wait with backoff
		if attempt > 0 {
			wait := e.policy.BackoffDuration(attempt - 1)
			select {
			case <-ctx.Done():
				return &ExecuteResult{Channel: lastChannel, Err: ctx.Err(), Attempt: attempt, Fallback: switched, FirstErr: firstErr, FallbackReason: ClassifyRetryFallbackReason(firstErr)}
			case <-time.After(wait):
			}

			if lastChannel != nil {
				excluded[RoutingSourceIdentityForChannel(lastChannel)] = true
			}
			previous := lastChannel
			ch := e.selectNextForRetry(ctx, group, model, candidates, excluded, initialChannel)
			if ch != nil {
				// A genuine source switch: the selector returned a different
				// channel than the one that just failed. Only this counts as
				// a fallback for routing_fallback_total.
				if !SameRoutingSource(ch, previous) && firstErr == nil {
					firstErr = lastErr
					switched = true
				}
				lastChannel = ch
			} else if lastChannel == nil {
				return &ExecuteResult{Err: lastErr, Attempt: attempt, Fallback: switched, FirstErr: firstErr, FallbackReason: ClassifyRetryFallbackReason(firstErr)}
			}
			// If no alternative found, lastChannel stays the same — this is a
			// same-source retry, NOT a fallback.
		} else if initialChannel != nil {
			lastChannel = initialChannel
		} else {
			// First attempt: select channel normally
			ch, selErr := e.selector.SelectChannel(ctx, group, model, false)
			if selErr != nil {
				return &ExecuteResult{Err: selErr, Attempt: attempt}
			}
			lastChannel = ch
		}

		startedAt := time.Now()
		err := fn(ctx, lastChannel)
		responseTime := time.Since(startedAt).Milliseconds()
		if err == nil {
			e.recordHealth(ctx, lastChannel, true, "", responseTime)
			return &ExecuteResult{
				Channel:        lastChannel,
				Attempt:        attempt,
				Fallback:       switched,
				FirstErr:       firstErr,
				FallbackReason: ClassifyRetryFallbackReason(firstErr),
			}
		}

		lastErr = err
		e.recordHealth(ctx, lastChannel, false, err.Error(), responseTime)

		// If not retryable, fail immediately
		if !e.policy.IsRetryable(err) {
			return &ExecuteResult{
				Channel:        lastChannel,
				Err:            err,
				Attempt:        attempt,
				Fallback:       switched,
				FirstErr:       firstErr,
				FallbackReason: ClassifyRetryFallbackReason(firstErr),
			}
		}
	}

	return &ExecuteResult{
		Channel:        lastChannel,
		Err:            lastErr,
		Attempt:        maxAttempts,
		Fallback:       switched,
		FirstErr:       firstErr,
		FallbackReason: ClassifyRetryFallbackReason(firstErr),
	}
}

// selectNextForRetry picks the next source for a retry attempt, in order:
//  1. walk the request-scoped precomputed candidate list with the accumulated
//     exclusion set (no selection RPC);
//  2. the unified cross-namespace fallback with per-candidate exclusion;
//  3. the legacy excludeFirstPriority tier-skip when no fallback selector is
//     wired.
//
// Returns nil when no alternative exists — the caller then performs a
// same-source retry, which is NOT a fallback.
func (e *RetryExecutor) selectNextForRetry(
	ctx context.Context,
	group, model string,
	candidates *RoutingCandidateList,
	excluded map[RoutingSourceIdentity]bool,
	initialChannel *Channel,
) *Channel {
	// HIGH-1 fix: HTTP transport retry closures build the upstream provider
	// directly from ch.Key (providerFactory.CreateProviderWithConfig). A
	// subscription-account projection carries an empty Key by design
	// (credentials are resolved via the adaptor/credential store, which the
	// HTTP closures do not invoke), so a cross-namespace retry onto a
	// subscription projection would forward an empty key upstream → 401 that
	// is NOT retryable, leaking straight to the client. Lock retry to the
	// INITIAL source namespace: an API-key plan only ever fails over to other
	// API-key channels, and a subscription plan only to other subscription
	// accounts. This lock applies only to the candidate-list path
	// (ExecuteWithCandidates, used by the 4 HTTP handlers); the legacy
	// ExecuteWithInitialChannel path (candidates == nil) keeps cross-namespace
	// behaviour for its existing callers and tests. WS/Responses transport is
	// unaffected — it routes through SelectFallbackRoutingSource directly
	// (credential-resolving path).
	var initialKind UpstreamRouteKind
	if candidates != nil {
		initialKind = UpstreamRouteChannel
		if initialChannel != nil && initialChannel.SubscriptionAccountID > 0 {
			initialKind = UpstreamRouteSubscription
		}
	}

	// 1. Precomputed candidate list (sub2api #2): stable per-request ordering,
	// no re-selection RPC per retry. Skip cross-namespace candidates so the
	// namespace lock holds (see HIGH-1 note above).
	if candidates != nil {
		for _, c := range candidates.Candidates {
			if c.Channel == nil {
				continue
			}
			if excluded[c.Identity] {
				continue
			}
			kind := UpstreamRouteChannel
			if c.Channel.SubscriptionAccountID > 0 {
				kind = UpstreamRouteSubscription
			}
			if kind != initialKind {
				continue
			}
			return c.Channel
		}
	}
	// 2. Fallback selection.
	//    - Candidate-list path (ExecuteWithCandidates): namespace-scoped.
	//      API-key initial → channel-only via SelectChannelExcluding (same
	//      namespace, no subscription projection); subscription initial →
	//      SelectFallbackRoutingSource (the transports using this path that
	//      start on a subscription source resolve credentials via the adaptor).
	//    - Legacy path (ExecuteWithInitialChannel, candidates == nil): keep
	//      the original cross-namespace tier-skip behaviour so existing
	//      callers and tests are unaffected.
	if candidates == nil {
		if e.fallback != nil {
			if ch, err := e.fallback.SelectFallbackRoutingSource(ctx, group, model, model, excluded); err == nil && ch != nil {
				return ch
			}
			return nil
		}
		ch, selErr := e.selector.SelectChannel(ctx, group, model, true)
		if selErr == nil && ch != nil {
			return ch
		}
		return nil
	}
	if initialKind == UpstreamRouteChannel {
		if excl, ok := e.selector.(interface {
			SelectChannelExcluding(ctx context.Context, group, model string, excluded map[int64]bool) (*Channel, error)
		}); ok {
			excludedChannels := make(map[int64]bool)
			for source, blocked := range excluded {
				if blocked && source.Kind == UpstreamRouteChannel && source.ID > 0 {
					excludedChannels[source.ID] = true
				}
			}
			if ch, err := excl.SelectChannelExcluding(ctx, group, model, excludedChannels); err == nil && ch != nil {
				return ch
			}
			return nil
		}
		// No excluding API on the selector: legacy tier-skip, channel-only.
		ch, selErr := e.selector.SelectChannel(ctx, group, model, true)
		if selErr == nil && ch != nil {
			return ch
		}
		return nil
	}
	// Subscription initial: unified fallback (credential-resolving transports).
	if e.fallback != nil {
		if ch, err := e.fallback.SelectFallbackRoutingSource(ctx, group, model, model, excluded); err == nil && ch != nil {
			return ch
		}
	}
	return nil
}

// ClassifyRetryFallbackReason maps the first-attempt error to a low-cardinality
// fallback reason label. This is the authoritative reason for
// routing_fallback_total — it reflects WHY the source switch happened, not the
// terminal error. Returns "" when no fallback occurred (firstErr == nil).
func ClassifyRetryFallbackReason(firstErr error) string {
	if firstErr == nil {
		return ""
	}
	status := UpstreamStatus(firstErr)
	switch {
	case status == 429:
		return "rate_limited"
	case status >= 500:
		return "upstream_5xx"
	case status == 0 && (errors.Is(firstErr, context.DeadlineExceeded) || isTimeoutError(firstErr)):
		return "timeout"
	case status == 0:
		return "network"
	default:
		return "other"
	}
}

// SameRoutingSource compares routing identities across the separate channel
// and subscription-account ID namespaces.
func SameRoutingSource(left, right *Channel) bool {
	if left == nil || right == nil {
		return left == right
	}
	if left.SubscriptionAccountID > 0 || right.SubscriptionAccountID > 0 {
		return left.SubscriptionAccountID > 0 &&
			right.SubscriptionAccountID > 0 &&
			left.SubscriptionAccountID == right.SubscriptionAccountID
	}
	return left.ID == right.ID
}

func isTimeoutError(err error) bool {
	if err == nil {
		return false
	}
	msg := strings.ToLower(err.Error())
	return strings.Contains(msg, "timeout") || strings.Contains(msg, "deadline exceeded")
}

func (e *RetryExecutor) recordHealth(ctx context.Context, ch *Channel, success bool, message string, responseTime int64) {
	if e.selector == nil || ch == nil || ch.ID <= 0 || ch.SubscriptionAccountID > 0 {
		return
	}
	_ = e.selector.RecordChannelHealth(ctx, ch.ID, success, message, responseTime)
}

// RecordAccountHealth feeds a known subscription-account upstream outcome
// into the selector. accountID<=0 is a no-op for ordinary API-key channels.
func (e *RetryExecutor) RecordAccountHealth(ctx context.Context, accountID int64, success bool) {
	if e == nil || e.selector == nil || accountID <= 0 {
		return
	}
	_ = e.selector.RecordSubscriptionAccountHealth(ctx, accountID, success)
}

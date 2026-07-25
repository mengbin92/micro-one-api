package biz

import (
	"context"
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
	// RecordSubscriptionAccountHealth feeds a relay outcome into the P2 #7
	// SubscriptionAccountSelector (🟡#8). accountID is the id of the
	// subscription account that served the request (0 for ordinary API-key
	// channels, in which case recording is a no-op). Called by RetryExecutor
	// after every attempt so the selector's healthFactor/circuit-breaker
	// tracks live relay results instead of staying inert.
	RecordSubscriptionAccountHealth(ctx context.Context, accountID int64, success bool) error
}

// RetryExecutor orchestrates retry attempts with channel fallback.
// It is a biz-layer concern: each retry re-selects a channel (excluding the
// first-priority tier) before calling the upstream provider.
type RetryExecutor struct {
	policy   *RetryPolicy
	selector ChannelSelector
}

// NewRetryExecutor creates a RetryExecutor with the given policy and channel selector.
func NewRetryExecutor(policy *RetryPolicy, selector ChannelSelector) *RetryExecutor {
	return &RetryExecutor{
		policy:   policy,
		selector: selector,
	}
}

// ExecuteResult holds the outcome of a retried operation.
type ExecuteResult struct {
	Channel *Channel
	Err     error
	Attempt int
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
// fallback, AND feeds each attempt's outcome into the P2 #7
// SubscriptionAccountSelector via RecordAccountHealth (🟡#8). Use this for
// subscription-account channels so the selector's healthFactor / circuit
// breaker tracks live relay results. accountID<=0 degrades to plain
// ExecuteWithInitialChannel (ordinary API-key channels have no selector state).
func (e *RetryExecutor) ExecuteWithAccountHealth(
	ctx context.Context,
	group, model string,
	initialChannel *Channel,
	accountID int64,
	fn func(ctx context.Context, ch *Channel) error,
) *ExecuteResult {
	// Wrap fn so each attempt's outcome is recorded for the account.
	wrapped := func(ctx context.Context, ch *Channel) error {
		err := fn(ctx, ch)
		e.RecordAccountHealth(ctx, accountID, err == nil)
		return err
	}
	return e.ExecuteWithInitialChannel(ctx, group, model, initialChannel, wrapped)
}

func (e *RetryExecutor) ExecuteWithInitialChannel(
	ctx context.Context,
	group, model string,
	initialChannel *Channel,
	fn func(ctx context.Context, ch *Channel) error,
) *ExecuteResult {
	maxAttempts := e.policy.MaxAttempts
	if maxAttempts <= 0 {
		maxAttempts = 1
	}

	var lastErr error
	var lastChannel *Channel

	for attempt := 0; attempt < maxAttempts; attempt++ {
		// On retry, wait with backoff
		if attempt > 0 {
			wait := e.policy.BackoffDuration(attempt - 1)
			select {
			case <-ctx.Done():
				return &ExecuteResult{Channel: lastChannel, Err: ctx.Err(), Attempt: attempt}
			case <-time.After(wait):
			}

			// Re-select channel, excluding first-priority tier.
			// 🟡#5: the failover path uses excludeFirstPriority=true to
			// avoid the just-failed tier. If that returns no channel we
			// must NOT widen to excludeFirstPriority=false: that would
			// bypass the "failover does not fall back to catch-all"
			// contract (docs §11.2) and could re-select the catch-all
			// channel that just failed. Instead, surface the last error
			// so the caller sees the real failure rather than silently
			// retrying the same catch-all channel.
			ch, selErr := e.selector.SelectChannel(ctx, group, model, true)
			if selErr != nil {
				return &ExecuteResult{Channel: lastChannel, Err: lastErr, Attempt: attempt}
			}
			lastChannel = ch
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
			return &ExecuteResult{Channel: lastChannel, Attempt: attempt}
		}

		lastErr = err
		e.recordHealth(ctx, lastChannel, false, err.Error(), responseTime)

		// If not retryable, fail immediately
		if !e.policy.IsRetryable(err) {
			return &ExecuteResult{Channel: lastChannel, Err: err, Attempt: attempt}
		}
	}

	return &ExecuteResult{Channel: lastChannel, Err: lastErr, Attempt: maxAttempts}
}

func (e *RetryExecutor) recordHealth(ctx context.Context, ch *Channel, success bool, message string, responseTime int64) {
	if e.selector == nil || ch == nil || ch.ID <= 0 {
		return
	}
	_ = e.selector.RecordChannelHealth(ctx, ch.ID, success, message, responseTime)
}

// RecordAccountHealth feeds a subscription-account relay outcome into the P2
// #7 selector (🟡#8). The server layer calls this after each subscription-
// account attempt (it has plan.Account.ID, which the *Channel type does not
// carry). accountID<=0 is a no-op (ordinary API-key channels). This is the
// single wiring point that makes the selector's healthFactor / circuit
// breaker non-inert: without it, healthFactor is always 100 and circuits
// never trip, contradicting docs §12.2.
func (e *RetryExecutor) RecordAccountHealth(ctx context.Context, accountID int64, success bool) {
	if e == nil || e.selector == nil || accountID <= 0 {
		return
	}
	_ = e.selector.RecordSubscriptionAccountHealth(ctx, accountID, success)
}

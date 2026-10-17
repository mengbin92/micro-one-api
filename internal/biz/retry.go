package biz

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"github.com/google/uuid"
	"math"
	"math/rand/v2"
	"micro-one-api/domain/requesttrace"
	"micro-one-api/domain/upstream/contract"
	"strings"
	"time"

	relayprovider "micro-one-api/domain/upstream/provider"
	"micro-one-api/pkg/jsonx"
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
	// Retryable overrides the status policy when a protocol adaptor has
	// classified the failure. Nil preserves the default policy.
	Retryable *bool
	// NoUpstreamAttempt keeps local admission failures out of upstream health.
	NoUpstreamAttempt bool
}

func IsLocalAdmissionError(err error) bool {
	var target *RetryableError
	return errors.As(err, &target) && target.NoUpstreamAttempt
}

func (e *RetryableError) Error() string {
	return fmt.Sprintf("status=%d, %v", e.Status, e.Err)
}

func (e *RetryableError) Unwrap() error {
	return e.Err
}

func (e *RetryableError) UpstreamStatusCode() int { return e.Status }

// PostForwardError marks a local lifecycle failure that happened after the
// upstream already returned a response. Retrying such an error would duplicate
// the upstream request, and it must not count against upstream channel health.
type PostForwardError struct {
	Err                error
	UnverifiedResponse bool
}

func (e *PostForwardError) Error() string {
	return e.Err.Error()
}

func (e *PostForwardError) Unwrap() error {
	return e.Err
}

// MarkPostForwardError preserves the original error while making its retry
// and channel-health disposition explicit.
func MarkPostForwardError(err error) error {
	if err == nil || IsPostForwardError(err) {
		return err
	}
	return &PostForwardError{Err: err}
}

// A conversion failure cannot prove completed inference, even after HTTP 2xx.
func MarkUnverifiedResponseError(err error) error {
	if err == nil {
		return nil
	}
	return &PostForwardError{Err: err, UnverifiedResponse: true}
}

// IsPostForwardError reports whether the upstream call already completed
// before the wrapped local lifecycle failure occurred.
func IsPostForwardError(err error) bool {
	var target *PostForwardError
	return errors.As(err, &target)
}

// ProtocolCapabilityError marks an upstream response as evidence that the
// selected source cannot serve the requested protocol. This is terminal even
// when a custom status policy would otherwise permit a retry.
type ProtocolCapabilityError struct {
	Err error
}

func (e *ProtocolCapabilityError) Error() string {
	return e.Err.Error()
}

func (e *ProtocolCapabilityError) Unwrap() error {
	return e.Err
}

// MarkProtocolCapabilityError preserves the typed upstream error while adding
// the endpoint-aware retry classification.
func MarkProtocolCapabilityError(err error) error {
	if err == nil || IsProtocolCapabilityMismatch(err) {
		return err
	}
	return &ProtocolCapabilityError{Err: err}
}

// IsRetryable checks whether an error should trigger a retry attempt.
func (p *RetryPolicy) IsRetryable(err error) bool {
	if err == nil {
		return false
	}
	if IsPostForwardError(err) || IsProtocolCapabilityMismatch(err) || relayprovider.IsPreForwardError(err) {
		return false
	}
	var retryable *RetryableError
	if errors.As(err, &retryable) && retryable.Retryable != nil {
		return *retryable.Retryable
	}

	// Prefer typed RetryableError / UpstreamHTTPError values. UpstreamStatus
	// retains string parsing only as a compatibility fallback for legacy callers.
	if status := UpstreamStatus(err); status > 0 {
		return p.RetryableStatus[status]
	}

	// Network errors are always retryable
	return isRetryableNetworkError(err)
}

func isRetryableNetworkError(err error) bool {
	if err == nil {
		return false
	}
	msg := err.Error()
	// "no such host" / "temporary failure in name resolution" cover DNS
	// failures surfaced at provider construction (validateBaseURL resolves the
	// hostname eagerly), which never reach the dialer and so never match the
	// dial patterns above.
	for _, pattern := range []string{"connection refused", "timeout", "EOF", "dial tcp", "no such host", "temporary failure in name resolution", "server misbehaving"} {
		if strings.Contains(msg, pattern) {
			return true
		}
	}
	return false
}

// IsProtocolCapabilityMismatch identifies request failures caused by an
// upstream protocol limitation rather than invalid client input. Keep the 400
// match deliberately narrow: other bad requests must still fail immediately.
func IsProtocolCapabilityMismatch(err error) bool {
	var capability *relayprovider.CapabilityError
	if errors.As(err, &capability) {
		return true
	}
	if _, ok := errors.AsType[*ProtocolCapabilityError](err); ok {
		return true
	}
	status := UpstreamStatus(err)
	if status != 400 {
		return false
	}
	return strings.Contains(strings.ToLower(err.Error()), "reasoning_content in the thinking mode must be passed back")
}

// UpstreamHealthDisposition is shared by HTTP, subscription and WebSocket paths.
func UpstreamHealthDisposition(ctx context.Context, err error) relayprovider.HealthOutcome {
	if IsLocalAdmissionError(err) || IsProtocolCapabilityMismatch(err) || isUpstreamModelUnavailable(err) || isUpstreamPolicyRejection(err) {
		return relayprovider.HealthNeutral
	}
	var postForward *PostForwardError
	if errors.As(err, &postForward) {
		if postForward.UnverifiedResponse {
			return relayprovider.HealthNeutral
		}
		// The successful upstream response is known even if local settlement fails.
		return relayprovider.HealthSuccess
	}
	return relayprovider.HealthDisposition(ctx, err)
}

func isUpstreamModelUnavailable(err error) bool {
	var upstreamErr *relayprovider.UpstreamHTTPError
	if !errors.As(err, &upstreamErr) {
		return false
	}
	// A concrete API-shaped upstream 404 means this route cannot serve the
	// requested model. Only an API error body (JSON, or a body naming the
	// model) counts: a proxy/HTML 404 from a misconfigured base_url is
	// channel-level breakage, and recording it as model unavailability would
	// mark every model on the channel red while channel health stays green.
	if upstreamErr.StatusCode == 404 {
		return upstream404IndicatesModel(upstreamErr.Body)
	}
	body := strings.ToLower(string(upstreamErr.Body))
	return strings.Contains(body, "service do not have healthy model") ||
		strings.Contains(body, "no healthy model")
}

// upstream404IndicatesModel reports whether a 404 body is an API error about
// the model rather than an unrelated not-found page.
func upstream404IndicatesModel(body []byte) bool {
	trimmed := bytes.TrimSpace(body)
	if len(trimmed) == 0 {
		return false
	}
	if jsonx.Valid(trimmed) {
		return true
	}
	return strings.Contains(strings.ToLower(string(trimmed)), "model")
}

func isUpstreamPolicyRejection(err error) bool {
	var upstreamErr *relayprovider.UpstreamHTTPError
	if !errors.As(err, &upstreamErr) {
		return false
	}
	body := strings.ToLower(string(upstreamErr.Body))
	return strings.Contains(body, "sensitive_words_detected") || strings.Contains(body, "cyber_policy") || strings.Contains(body, "cyber safety")
}

// BackoffDuration calculates the sleep duration for the given attempt (0-indexed).
func (p *RetryPolicy) BackoffDuration(attempt int) time.Duration {
	d := min(time.Duration(float64(p.InitialInterval)*math.Pow(p.Multiplier, float64(attempt))), p.MaxInterval)
	return d
}

func JitteredRetryDelay(delay time.Duration) time.Duration {
	if delay <= 0 {
		return 0
	}
	return time.Duration(rand.Int64N(int64(delay))) // #nosec G404 -- retry jitter is not a credential or security decision.
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
	if _, after, ok := strings.Cut(msg, "status="); ok {
		statusStr := after
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
//
// Order of precedence (relay-M1): a typed *RetryableError wins, then a typed
// *relayprovider.UpstreamHTTPError (returned by every provider's non-2xx path
// and by the internal adaptors). Only when neither typed check matches do we
// fall back to parsing the "status=N" substring out of the error message — a
// deliberately last-resort path, since any wording change in an error string
// would silently break status-based channel failover.
func UpstreamStatus(err error) int {
	var rejection *contract.Rejection
	if errors.As(err, &rejection) {
		if errors.Is(err, contract.ErrQuarantined) {
			return 503
		}
		return 400
	}
	if IsExplicitCapabilityError(err) {
		return 501
	}
	if err == nil {
		return 0
	}
	var re *RetryableError
	if AsRetryableError(err, &re) {
		return re.Status
	}
	if upstreamErr, ok := errors.AsType[*relayprovider.UpstreamHTTPError](err); ok {
		return upstreamErr.StatusCode
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

// ModelHealthRecorder is an optional extension implemented by production
// channel clients. Keeping it separate preserves compatibility with selector
// fakes while passive model monitoring rolls out independently of routing.
type ModelHealthRecorder interface {
	RecordModelHealth(ctx context.Context, sourceKind string, sourceID int64, modelID, upstreamModelID string, success bool, err string, responseTime int64) error
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
	executionAdmission func(context.Context, *Channel) (*Channel, error)
	budget             *retryBudget
	deferStreamHealth  bool
	policy             *RetryPolicy
	selector           ChannelSelector
	// fallback, when wired, is the unified cross-namespace fallback used after
	// the request-scoped candidate list is exhausted (sub2api #2). It replaces
	// the coarse excludeFirstPriority tier-skip with per-candidate exclusion.
	fallback                   RoutingFallbackSelector
	authorize                  func(context.Context, string, string, string, *Channel) error
	crossSourceFallback        bool
	externalSubscriptionHealth bool
}

// WithCrossSourceFallback opts in only callers that materialize subscription
// accounts and resolve their credentials before forwarding each attempt.
func (e *RetryExecutor) WithCrossSourceFallback() *RetryExecutor {
	e.crossSourceFallback = true
	return e
}

// WithExternalSubscriptionHealth avoids duplicate health samples when an
// adaptor already records each subscription attempt, including local retries.
func (e *RetryExecutor) WithExternalSubscriptionHealth() *RetryExecutor {
	e.externalSubscriptionHealth = true
	return e
}

func (e *RetryExecutor) WithDeferredStreamHealth() *RetryExecutor {
	e.deferStreamHealth = true
	return e
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

// WithRouteAuthorization rechecks persisted candidates and same-source retries
// immediately before a new upstream attempt. Normal attempt zero was selected
// by the channel authority; a retry may outlive the grant that selected it.
func (e *RetryExecutor) WithRouteAuthorization(check func(context.Context, string, string, string, *Channel) error) *RetryExecutor {
	e.authorize = check
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
		if e.deferStreamHealth && err == nil {
			return nil
		}
		if outcome := UpstreamHealthDisposition(ctx, err); ch == initialChannel && outcome != relayprovider.HealthNeutral {
			e.RecordAccountHealth(ctx, accountID, outcome == relayprovider.HealthSuccess)
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
// is wired). Health follows the executed subscription identity; accountID is
// retained for legacy initial-channel plans without an explicit account ID.
func (e *RetryExecutor) ExecuteWithCandidates(
	ctx context.Context,
	plan *RelayPlan,
	accountID int64,
	fn func(ctx context.Context, ch *Channel) error,
) *ExecuteResult {
	if plan == nil {
		return &ExecuteResult{Err: fmt.Errorf("relay plan is nil")}
	}
	if trace := requesttrace.FromContext(ctx); trace.RootRequestID == "" && plan.SelectionEvent != nil && plan.SelectionEvent.RootRequestID != "" {
		trace.RootRequestID = plan.SelectionEvent.RootRequestID
		ctx = requesttrace.WithAttempt(ctx, trace)
	}
	group, model := "", plan.BaseModel()
	if plan.Auth != nil {
		group = plan.Auth.Group
	}
	initialChannel := plan.Channel
	wrapped := func(ctx context.Context, ch *Channel) error {
		if err := RecheckRoutingAdmission(ctx, plan.Auth, plan.ClientModel); err != nil {
			return err
		}
		err := fn(ctx, ch)
		if outcome := UpstreamHealthDisposition(ctx, err); !e.externalSubscriptionHealth && !(e.deferStreamHealth && err == nil) && ch.UpstreamConfigurationRevision == "" && outcome != relayprovider.HealthNeutral {
			id := ch.SubscriptionAccountID
			if id <= 0 && ch == initialChannel {
				id = accountID
			}
			e.RecordAccountHealth(ctx, id, outcome == relayprovider.HealthSuccess)
		}
		return err
	}
	return e.execute(ctx, group, model, plan.ClientModel, plan.ModelHealthID(), initialChannel, plan.Candidates, wrapped)
}

func (e *RetryExecutor) ExecuteWithInitialChannel(
	ctx context.Context,
	group, model string,
	initialChannel *Channel,
	fn func(ctx context.Context, ch *Channel) error,
) *ExecuteResult {
	return e.execute(ctx, group, model, model, model, initialChannel, nil, fn)
}

func (e *RetryExecutor) execute(
	ctx context.Context,
	group, model, clientModel, healthModel string,
	initialChannel *Channel,
	candidates *RoutingCandidateList,
	fn func(ctx context.Context, ch *Channel) error,
) *ExecuteResult {
	maxAttempts := e.policy.MaxAttempts
	trace := requesttrace.FromContext(ctx)
	if trace.RootRequestID == "" {
		trace.RootRequestID = uuid.NewString()
	}
	if maxAttempts <= 0 {
		maxAttempts = 1
	}

	var lastErr error
	var lastChannel *Channel
	var firstErr error // first failure that triggered a source switch
	switched := false  // true when executor selected a DIFFERENT channel
	primaryRecorded := false
	// excluded accumulates every source that failed in this request so retries
	// never re-select it (sub2api #2 request-level exclusion set).
	excluded := make(map[RoutingSourceIdentity]bool)
	type healthOutcome struct {
		channel        *Channel
		channelOutcome relayprovider.HealthOutcome
		err            error
		startedAt      time.Time
		modelRecord    bool
		modelOK        bool
		message        string
		responseTime   int64
	}
	var pendingHealth *healthOutcome
	flushHealth := func() {
		if pendingHealth == nil {
			return
		}
		observed := false
		if !e.externalSubscriptionHealth || pendingHealth.channel.SubscriptionAccountID <= 0 {
			ch := *pendingHealth.channel
			ch.UpstreamModelID = ResolveChannelModel(&ch, model)
			observed = recordUpstreamObservation(ctx, e.selector, &ch, pendingHealth.err, pendingHealth.responseTime, pendingHealth.startedAt)
		}
		e.recordHealth(ctx, healthModel, model, pendingHealth.channel, pendingHealth.channelOutcome, pendingHealth.modelRecord, pendingHealth.modelOK, pendingHealth.message, pendingHealth.responseTime, observed)
		pendingHealth = nil
	}
	queueHealth := func(ch *Channel, err error, responseTime int64, startedAt time.Time) {
		if IsLocalAdmissionError(err) {
			flushHealth()
			if ch != nil && ch.HealthProbeID != "" {
				recordUpstreamObservation(ctx, e.selector, ch, err, responseTime, startedAt)
			}
			return
		}
		channelOutcome := UpstreamHealthDisposition(ctx, err)
		modelRecord, modelOK := modelHealthDisposition(ctx, err)
		message := ""
		if err != nil {
			message = err.Error()
		}
		if pendingHealth != nil && SameRoutingSource(pendingHealth.channel, ch) {
			// Same-source retries are one logical channel outcome. Accumulate only
			// upstream time (backoff is intentionally excluded) and let the latest
			// attempt determine the terminal result.
			pendingHealth.channelOutcome = channelOutcome
			pendingHealth.err = err
			pendingHealth.startedAt = startedAt
			pendingHealth.modelRecord = modelRecord
			pendingHealth.modelOK = modelOK
			pendingHealth.message = message
			pendingHealth.responseTime += responseTime
			return
		}
		flushHealth()
		pendingHealth = &healthOutcome{channel: ch, channelOutcome: channelOutcome, err: err, startedAt: startedAt, modelRecord: modelRecord, modelOK: modelOK, message: message, responseTime: responseTime}
	}

	for attempt := 0; attempt < maxAttempts; attempt++ {
		// On retry, wait with backoff
		if attempt > 0 {
			if lastChannel != nil && lastChannel.UpstreamConfigurationRevision != "" && e.budget != nil && !IsLocalAdmissionError(lastErr) && !e.budget.allow() {
				flushHealth()
				return &ExecuteResult{Channel: lastChannel, Err: lastErr, Attempt: attempt - 1, Fallback: switched, FirstErr: firstErr, FallbackReason: ClassifyRetryFallbackReason(firstErr)}
			}
			wait := JitteredRetryDelay(e.policy.BackoffDuration(attempt - 1))
			_, endBackoff := requesttrace.Phase(ctx, "retry.backoff")
			select {
			case <-ctx.Done():
				endBackoff(ctx.Err())
				flushHealth()
				return &ExecuteResult{Channel: lastChannel, Err: ctx.Err(), Attempt: attempt, Fallback: switched, FirstErr: firstErr, FallbackReason: ClassifyRetryFallbackReason(firstErr)}
			case <-time.After(wait):
			}
			endBackoff(nil)

			if lastChannel != nil {
				excluded[RoutingSourceIdentityForChannel(lastChannel)] = true
			}
			previous := lastChannel
			selectCtx, endSelect := requesttrace.Phase(ctx, "route.reselect")
			ch := e.selectNextForRetry(selectCtx, group, clientModel, model, candidates, excluded, initialChannel)
			endSelect(nil)
			if ch != nil {
				if !SameRoutingSource(ch, lastChannel) {
					flushHealth()
				}
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

		if attempt > 0 && e.authorize != nil {
			if err := e.authorize(ctx, group, clientModel, model, lastChannel); err != nil {
				flushHealth()
				return &ExecuteResult{Channel: lastChannel, Err: err, Attempt: attempt, Fallback: switched, FirstErr: firstErr, FallbackReason: ClassifyRetryFallbackReason(firstErr)}
			}
		}
		startedAt := time.Now()
		trace.Number = int32(attempt + 1)
		trace.SourceKind = UpstreamSourceChannel
		if lastChannel.SubscriptionAccountID > 0 {
			trace.SourceKind = UpstreamSourceSubscription
		}
		trace.UpstreamModelID = ResolveChannelModel(lastChannel, model)
		attemptCtx := requesttrace.EnsureAttempt(requesttrace.WithAttempt(ctx, trace), trace.Number)
		sourceID := lastChannel.ID
		if lastChannel.SubscriptionAccountID > 0 {
			sourceID = lastChannel.SubscriptionAccountID
		}
		requesttrace.RecordAttempt(attemptCtx, requesttrace.TimingAttempt{Number: trace.Number, SourceKind: trace.SourceKind, SourceID: sourceID, UpstreamModelID: trace.UpstreamModelID})
		var err error
		if e.executionAdmission != nil {
			candidate := *lastChannel
			candidate.UpstreamModelID = trace.UpstreamModelID
			var admitted *Channel
			admissionCtx, endAdmission := requesttrace.Phase(attemptCtx, "admission.check")
			admitted, err = e.executionAdmission(admissionCtx, &candidate)
			endAdmission(err)
			if err == nil {
				lastChannel = admitted
			}
		}
		if err == nil {
			trace.HealthProbeID = lastChannel.HealthProbeID
			err = fn(requesttrace.WithAttempt(attemptCtx, trace), lastChannel)
		}
		if err != nil {
			requesttrace.FinishAttempt(attemptCtx, trace.Number, requesttrace.Outcome(err))
		}
		requesttrace.RecordAttempt(attemptCtx, requesttrace.TimingAttempt{Number: trace.Number, Outcome: requesttrace.Outcome(err)})
		responseTime := time.Since(startedAt).Milliseconds()
		if !primaryRecorded && e.budget != nil && lastChannel.UpstreamConfigurationRevision != "" && !IsLocalAdmissionError(err) && !relayprovider.IsPreForwardError(err) && (!e.externalSubscriptionHealth || lastChannel.SubscriptionAccountID <= 0) {
			e.budget.primary()
			primaryRecorded = true
		}
		if err == nil {
			if !e.deferStreamHealth {
				queueHealth(lastChannel, nil, responseTime, startedAt)
			}
			flushHealth()
			return &ExecuteResult{
				Channel:        lastChannel,
				Attempt:        attempt,
				Fallback:       switched,
				FirstErr:       firstErr,
				FallbackReason: ClassifyRetryFallbackReason(firstErr),
			}
		}

		lastErr = err
		queueHealth(lastChannel, err, responseTime, startedAt)

		// If not retryable, fail immediately
		if !e.policy.IsRetryable(err) {
			flushHealth()
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
	flushHealth()

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
	group, clientModel, model string,
	candidates *RoutingCandidateList,
	excluded map[RoutingSourceIdentity]bool,
	initialChannel *Channel,
) *Channel {
	// Provider-only callbacks cannot use subscription projections: their Key
	// is deliberately empty. Only credential-resolving callers may opt out.
	var initialKind UpstreamRouteKind
	if candidates != nil {
		initialKind = UpstreamRouteChannel
		if initialChannel != nil && initialChannel.SubscriptionAccountID > 0 {
			initialKind = UpstreamRouteSubscription
		}
	}

	// 1. Walk the request-scoped ordering, preserving the namespace restriction
	// for callbacks that have not opted into subscription credential resolution.
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
			if !e.crossSourceFallback && kind != initialKind {
				continue
			}
			return c.Channel
		}
	}
	// 2. Credential-resolving callbacks can select across namespaces after
	// exhausting the list. Preserve legacy selection for callers without a list.
	if candidates == nil || e.crossSourceFallback {
		if e.fallback != nil {
			if ch, err := e.fallback.SelectFallbackRoutingSource(ctx, group, clientModel, model, excluded); err == nil && ch != nil {
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
		if ch, err := e.fallback.SelectFallbackRoutingSource(ctx, group, clientModel, model, excluded); err == nil && ch != nil {
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
	case IsProtocolCapabilityMismatch(firstErr):
		return "capability_mismatch"
	case isUpstreamModelUnavailable(firstErr):
		return "model_unavailable"
	case isUpstreamPolicyRejection(firstErr):
		return "policy_rejection"
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

func (e *RetryExecutor) recordHealth(ctx context.Context, modelID, baseModel string, ch *Channel, channelOutcome relayprovider.HealthOutcome, modelRecord, modelOK bool, message string, responseTime int64, observed bool) {
	if e.selector == nil || ch == nil {
		return
	}
	if e.externalSubscriptionHealth && ch.SubscriptionAccountID > 0 {
		return
	}
	if !observed && ch.ID > 0 && ch.SubscriptionAccountID <= 0 && channelOutcome != relayprovider.HealthNeutral {
		channelOK := channelOutcome == relayprovider.HealthSuccess
		channelMessage := message
		if channelOK {
			channelMessage = ""
		}
		_ = e.selector.RecordChannelHealth(ctx, ch.ID, channelOK, channelMessage, responseTime)
	}
	if !modelRecord {
		return
	}
	recorder, ok := e.selector.(ModelHealthRecorder)
	if !ok {
		return
	}
	sourceKind, sourceID := UpstreamSourceChannel, ch.ID
	if ch.SubscriptionAccountID > 0 {
		sourceKind, sourceID = UpstreamSourceSubscription, ch.SubscriptionAccountID
	}
	if sourceID <= 0 {
		return
	}
	_ = recorder.RecordModelHealth(ctx, sourceKind, sourceID, RelayModelName(modelID), ResolveChannelModel(ch, baseModel), modelOK, message, responseTime)
}

// modelHealthDisposition excludes caller/local failures and records only
// outcomes that say something about the concrete upstream model route.
func modelHealthDisposition(ctx context.Context, err error) (record, success bool) {
	if err == nil {
		return true, true
	}
	if ctx.Err() != nil || IsLocalAdmissionError(err) || IsPostForwardError(err) || IsProtocolCapabilityMismatch(err) || isUpstreamPolicyRejection(err) || relayprovider.IsPreForwardError(err) || errors.Is(err, context.Canceled) {
		return false, false
	}
	if isUpstreamModelUnavailable(err) {
		return true, false
	}
	if relayprovider.HealthDisposition(ctx, err) == relayprovider.HealthFailure {
		return true, false
	}
	return false, false
}

// RecordAccountHealth feeds a known subscription-account upstream outcome
// into the selector. accountID<=0 is a no-op for ordinary API-key channels.
func (e *RetryExecutor) RecordAccountHealth(ctx context.Context, accountID int64, success bool) {
	if e == nil || e.selector == nil || accountID <= 0 {
		return
	}
	_ = e.selector.RecordSubscriptionAccountHealth(ctx, accountID, success)
}

func IsExplicitCapabilityError(err error) bool {
	var capability *relayprovider.CapabilityError
	var protocol *ProtocolCapabilityError
	return errors.As(err, &capability) || errors.As(err, &protocol)
}

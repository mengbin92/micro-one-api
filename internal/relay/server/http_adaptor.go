package server

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/bytedance/sonic"

	billingv1 "micro-one-api/api/billing/v1"
	"micro-one-api/internal/pkg/metrics"
	relayadaptor "micro-one-api/internal/relay/adaptor"
	relaybiz "micro-one-api/internal/relay/biz"
	relaycredential "micro-one-api/internal/relay/credential"
	"micro-one-api/internal/relay/passthrough"
	relayprovider "micro-one-api/internal/relay/provider"
	relayquota "micro-one-api/internal/relay/quota"
)

// handleChatCompletionsViaAdaptor is the feature-flag-gated request path for
// subscription-account channels (Codex / Claude OAuth). It resolves the real
// subscription-account metadata from the selected channel, builds a
// RelayContext, and drives the full ConvertRequest → BuildUpstreamRequest →
// upstream call → ConvertResponse / ConvertStreamResponse pipeline.
//
// It is intentionally a thin, self-contained path: it does not participate in
// the RetryExecutor (subscription accounts are selected explicitly and retried
// via a different mechanism in later phases), and it performs only
// best-effort quota accounting. The goal of the MVP is to validate that the
// Responses-hub + mimicry + credential layers compose end-to-end.
func (s *HTTPServer) handleChatCompletionsViaAdaptor(
	w http.ResponseWriter,
	r *http.Request,
	plan *relaybiz.RelayPlan,
	clientModel string,
	rawBody []byte,
	sessionHash string,
) {
	s.handleSubscriptionAccountViaAdaptor(w, r, plan, clientModel, rawBody, relayadaptor.FormatOpenAIChatCompletions, sessionHash)
}

func (s *HTTPServer) handleAnthropicMessagesViaAdaptor(
	w http.ResponseWriter,
	r *http.Request,
	plan *relaybiz.RelayPlan,
	clientModel string,
	rawBody []byte,
	sessionHash string,
) {
	s.handleSubscriptionAccountViaAdaptor(w, r, plan, clientModel, rawBody, relayadaptor.FormatAnthropicMessages, sessionHash)
}

func (s *HTTPServer) handleResponsesCreateLikeViaAdaptor(
	w http.ResponseWriter,
	r *http.Request,
	plan *relaybiz.RelayPlan,
	clientModel string,
	rawBody []byte,
) {
	// The Responses/WS path keeps its existing session -> channel stickiness
	// (wsScheduler); it does not participate in the new account-stickiness bind
	// (empty sessionHash), so the two mechanisms don't double-bind.
	s.handleSubscriptionAccountViaAdaptor(w, r, plan, clientModel, rawBody, relayadaptor.FormatOpenAIResponses, "")
}

func (s *HTTPServer) handleSubscriptionAccountViaAdaptor(
	w http.ResponseWriter,
	r *http.Request,
	plan *relaybiz.RelayPlan,
	clientModel string,
	rawBody []byte,
	inbound relayadaptor.Format,
	sessionHash string,
) {
	if plan == nil || plan.Channel == nil {
		s.writeError(w, http.StatusInternalServerError, "no channel selected")
		return
	}
	if plan.Auth == nil {
		s.writeError(w, http.StatusInternalServerError, "no auth selected")
		return
	}

	maxAttempts := s.subscriptionFailoverMaxAttempts()
	failedAccounts := make(map[int64]bool, maxAttempts)
	current := plan
	var lastErr error
	for attempt := 0; attempt < maxAttempts; attempt++ {
		if current == nil || current.Channel == nil {
			break
		}
		result := s.runSubscriptionAttempt(r, current, clientModel, rawBody, inbound)
		if result.retryable {
			accountID := subscriptionAccountIDFromPlan(current)
			if accountID > 0 {
				failedAccounts[accountID] = true
				// A concurrency-full account is healthy, just busy: fail over to a
				// sibling but never cool it down.
				if !result.concurrencyFull {
					s.blockRuntimeAccount(r.Context(), accountID, result.statusCode, result.err)
				}
			}
			lastErr = result.err
			next, err := s.selectSubscriptionFailoverPlan(r.Context(), plan, current, clientModel, failedAccounts)
			if err == nil && next != nil && next.Channel != nil && subscriptionAccountIDFromPlan(next) != accountID {
				metrics.RelaySubscriptionFailoverTotal.WithLabelValues(subscriptionRetryReason(result), "switched").Inc()
				current = next
				continue
			}
			metrics.RelaySubscriptionFailoverTotal.WithLabelValues(subscriptionRetryReason(result), "exhausted").Inc()
		}
		// Bind the conversation to the account that actually served it (success
		// only), so the next turn reuses it for prompt-cache hits. A failover
		// switch naturally rebinds to the sibling that succeeded.
		if subscriptionAttemptSucceeded(result) {
			s.bindSubscriptionSession(r.Context(), plan.Auth.Group, sessionHash, current)
		}
		result.write(w)
		return
	}
	if lastErr != nil {
		s.writeError(w, http.StatusBadGateway, fmt.Sprintf("upstream call: %v", lastErr))
		return
	}
	s.writeError(w, http.StatusBadGateway, "no subscription account available")
}

const (
	// subscriptionSameAccountMaxRetries bounds how many times a transient
	// same-account error (409/423) is retried in place before escalating to
	// cross-account failover.
	subscriptionSameAccountMaxRetries = 3
	// subscriptionSameAccountRetryDelay is the fixed pause between same-account
	// retries.
	subscriptionSameAccountRetryDelay = 500 * time.Millisecond
)

// runSubscriptionAttempt executes one subscription-account request against the
// current plan and transparently retries a small number of times on the SAME
// account for transient 409/423 responses. If those retries are exhausted the
// result is escalated to cross-account failover (result.retryable) so the caller
// can try a sibling account. Cross-account failover itself is handled by the
// caller.
func (s *HTTPServer) runSubscriptionAttempt(r *http.Request, current *relaybiz.RelayPlan, clientModel string, rawBody []byte, inbound relayadaptor.Format) subscriptionAdaptorResult {
	result := s.executeAndMeter(r.Context(), current, clientModel, r.Header.Clone(), rawBody, inbound)
	for tries := 0; result.retryableSameAccount && tries < subscriptionSameAccountMaxRetries; tries++ {
		metrics.RelaySubscriptionFailoverTotal.WithLabelValues("same_account", "retried").Inc()
		if !sleepCtx(r.Context(), subscriptionSameAccountRetryDelay) {
			break // client/context cancelled: stop retrying
		}
		result = s.executeAndMeter(r.Context(), current, clientModel, r.Header.Clone(), rawBody, inbound)
	}
	if result.retryableSameAccount {
		// Same-account retries exhausted: escalate to cross-account failover.
		// 409/423 carry a zero runtime-block duration, so the caller excludes the
		// account for this request without cooling it down.
		result.retryable = true
	}
	return result
}

// executeAndMeter runs a single subscription-account request and records the
// adaptor request metric for that attempt.
func (s *HTTPServer) executeAndMeter(ctx context.Context, current *relaybiz.RelayPlan, clientModel string, header http.Header, rawBody []byte, inbound relayadaptor.Format) subscriptionAdaptorResult {
	result := s.executeSubscriptionAccountViaAdaptor(ctx, current, clientModel, header, rawBody, inbound)
	metrics.RelaySubscriptionAdaptorRequestsTotal.WithLabelValues(subscriptionMetricPlatform(current), string(inbound), subscriptionAdaptorMetricResult(result)).Inc()
	return result
}

// sleepCtx waits for d or until ctx is cancelled. It returns true if the full
// delay elapsed, false if the context was cancelled first.
func sleepCtx(ctx context.Context, d time.Duration) bool {
	if d <= 0 {
		return true
	}
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-t.C:
		return true
	case <-ctx.Done():
		return false
	}
}

type subscriptionAdaptorResult struct {
	statusCode int
	err        error
	// retryable marks a failure that should fail over to a DIFFERENT account
	// (429/529/5xx/network). The failing account is added to the exclusion set
	// and cooled down via the runtime blocker.
	retryable bool
	// retryableSameAccount marks a transient failure (409/423) that should be
	// retried a few times on the SAME account before failing over. The account is
	// NOT cooled down and does NOT count against the cross-account switch budget.
	retryableSameAccount bool
	// concurrencyFull marks that the account was at its in-process concurrency
	// limit, so no upstream call was made. It fails over to another account but,
	// unlike a real upstream error, must not cool the account down.
	concurrencyFull bool
	body            []byte
	header          http.Header
	write           func(http.ResponseWriter)
}

type subscriptionAccountQuotaRecorder interface {
	RecordAccountQuotaSnapshot(ctx context.Context, accountID int64, snapshot *relayquota.CodexSnapshot) error
	AutoPauseAccount(ctx context.Context, accountID int64, reason string) error
}

func (s *HTTPServer) executeSubscriptionAccountViaAdaptor(
	ctx context.Context,
	plan *relaybiz.RelayPlan,
	clientModel string,
	inboundHeader http.Header,
	rawBody []byte,
	inbound relayadaptor.Format,
) subscriptionAdaptorResult {
	result := subscriptionAdaptorResult{
		statusCode: http.StatusInternalServerError,
		write: func(w http.ResponseWriter) {
			s.writeError(w, http.StatusInternalServerError, "subscription adaptor failed")
		},
	}

	ad, ok := relayadaptor.GetAdaptor(plan.Channel.Type)
	if !ok {
		result.statusCode = http.StatusBadGateway
		result.err = fmt.Errorf("no adaptor registered for subscription channel type")
		result.write = func(w http.ResponseWriter) { s.writeError(w, http.StatusBadGateway, result.err.Error()) }
		return result
	}

	// Enforce the account's in-process concurrency limit before doing any work.
	// A full account fails over to a sibling (concurrencyFull) rather than being
	// cooled down: it is healthy, just busy. The slot is held for the entire
	// upstream call — including the full lifetime of a streamed response — and
	// released via slotTransferred/releaseSlot below.
	accountID := subscriptionAccountIDFromPlan(plan)
	var concurrencyLimit int32
	if plan.Account != nil {
		concurrencyLimit = plan.Account.Concurrency
	}
	releaseSlot, acquired := s.accountConcurrency.TryAcquire(accountID, concurrencyLimit)
	if !acquired {
		result.statusCode = http.StatusServiceUnavailable
		result.err = fmt.Errorf("subscription account %d at concurrency limit %d", accountID, concurrencyLimit)
		result.retryable = true
		result.concurrencyFull = true
		result.write = func(w http.ResponseWriter) {
			s.writeError(w, http.StatusServiceUnavailable, "all subscription accounts busy")
		}
		return result
	}
	// Released on every early return via defer; for the two terminal success
	// paths (stream + non-stream) ownership is transferred into result.write and
	// slotTransferred is set so the defer does not release the slot early.
	slotTransferred := false
	defer func() {
		if !slotTransferred {
			releaseSlot()
		}
	}()

	// Prefer the first-class subscription account selected during planning
	// (plan.Account), then the resolver, then the channel-fallback metadata.
	// The account carries the access token; the channel view intentionally
	// does not (see biz.RelayPlan.Account).
	meta := fallbackSubscriptionAccountMetadata(plan, plan.Channel)
	if plan.Account != nil {
		meta = subscriptionAccountMetadataFromPlan(plan.Account)
	} else if s.accountResolver != nil {
		if resolved, err := s.accountResolver.Resolve(ctx, plan.Channel.ID); err == nil && resolved != nil {
			meta = resolved
		}
	}

	// Build the relay context with the real account identity. Account.ID keys
	// the credential/identity caches; AccountID is the upstream account id
	// (chatgpt-account-id / Claude metadata user_id).
	rc := &relayadaptor.RelayContext{
		InboundFormat: inbound,
		ClientModel:   clientModel,
		ResolvedModel: plan.ResolvedModel,
		Channel:       plan.Channel,
		Account: &relayadaptor.AccountRef{
			ID:          meta.ID,
			Platform:    string(meta.Platform),
			AccountType: accountTypeOrDefault(meta.AccountType),
			GroupID:     meta.GroupID,
			AccountID:   meta.AccountID,
			AccessToken: meta.AccessToken,
			Fingerprint: meta.Fingerprint,
		},
		UserID:        plan.Auth.UserID,
		InboundHeader: inboundHeader.Clone(),
		RawBody:       rawBody,
	}
	// Carry the configured upstream HTTP client so the OAuth path respects the
	// gateway's timeout/proxy/transport settings instead of silently falling
	// back to http.DefaultClient.
	if s.oauthHTTPClient != nil {
		rc.HTTPClient = s.oauthHTTPClient
	}
	ad.Init(rc)

	// Convert the inbound request body to the upstream format.
	upstreamFmt, upstreamBody, err := ad.ConvertRequest(rc, inbound, rawBody)
	if err != nil {
		result.statusCode = http.StatusBadGateway
		result.err = fmt.Errorf("adaptor convert request: %w", err)
		result.write = func(w http.ResponseWriter) { s.writeError(w, http.StatusBadGateway, result.err.Error()) }
		return result
	}
	// (reservation happens after BuildUpstreamRequest so a build error does not
	//  leak a reservation; see below.)

	// Build the upstream http.Request (includes identity mimicry + OAuth token).
	upstreamReq, err := ad.BuildUpstreamRequest(ctx, rc, upstreamFmt, upstreamBody)
	if err != nil {
		result.statusCode = http.StatusBadGateway
		result.err = fmt.Errorf("adaptor build request: %w", err)
		result.write = func(w http.ResponseWriter) { s.writeError(w, http.StatusBadGateway, result.err.Error()) }
		return result
	}

	// Determine whether the client requested streaming.
	isStream := false
	var probe map[string]any
	if err := sonic.Unmarshal(rawBody, &probe); err == nil {
		if v, ok := probe["stream"].(bool); ok {
			isStream = v
		}
	}

	// Use the relay context's client (configured timeout/transport) rather than
	// http.DefaultClient so the OAuth path inherits the gateway's upstream
	// settings. Fall back to DefaultClient only when none is configured.
	client := rc.HTTPClient
	if client == nil {
		client = http.DefaultClient
	}
	resp, err := client.Do(upstreamReq)
	if err != nil {
		result.statusCode = http.StatusBadGateway
		result.err = fmt.Errorf("upstream call: %w", err)
		result.retryable = true
		result.write = func(w http.ResponseWriter) { s.writeError(w, http.StatusBadGateway, result.err.Error()) }
		return result
	}

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
		io.Copy(io.Discard, resp.Body) // drain so the connection can be reused
		resp.Body.Close()
		s.recordCodexQuotaSnapshot(ctx, plan, body)
		upstreamErr := passthrough.Classify(resp.StatusCode, body)
		result.statusCode = resp.StatusCode
		result.body = body
		result.header = resp.Header.Clone()
		result.err = fmt.Errorf("upstream returned status %d", resp.StatusCode)
		result.retryable = upstreamErr.RetryableAcrossAccounts()
		result.retryableSameAccount = upstreamErr.RetryableOnSameAccount()
		result.write = func(w http.ResponseWriter) {
			if upstreamErr.ShouldPassthrough() {
				metrics.RelayUpstreamPassthroughTotal.WithLabelValues(string(upstreamErr.Kind), fmt.Sprint(resp.StatusCode)).Inc()
				writeUpstreamPassthrough(w, resp.StatusCode, resp.Header, body)
				return
			}
			s.writeError(w, resp.StatusCode, result.err.Error())
		}
		return result
	}

	// Quota reservation for the subscription call (plan §5 step 8). We reserve
	// an estimate up front, then commit the real usage after the upstream
	// responds. Failures release the reservation. This mirrors the
	// API-key path; it is best-effort so a billing hiccup never blocks a
	// successful relay, but it ensures subscription accounts are no longer
	// free/unmetered. When the billing client is not configured (e.g. in tests
	// or a billing-less deployment) accounting is skipped.
	requestID := generateRequestID()
	channelID := fmt.Sprintf("%d", plan.Channel.ID)
	var reservation *billingv1.ReserveQuotaResponse
	accountUsage := s.billingClient != nil
	if accountUsage {
		var reserveErr error
		reservation, reserveErr = s.reserveQuota(
			ctx,
			fmt.Sprintf("%d", plan.Auth.UserID),
			requestID,
			estimateRawTokens(rawBody),
			plan.ResolvedModel,
			channelID,
			subscriptionAccountIDFromPlan(plan),
		)
		if reserveErr != nil {
			result.statusCode = http.StatusPaymentRequired
			result.err = fmt.Errorf("reserve quota: %w", reserveErr)
			result.write = func(w http.ResponseWriter) { s.writeError(w, http.StatusPaymentRequired, result.err.Error()) }
			return result
		}
	}

	if isStream {
		_, reader, err := ad.ConvertStreamResponse(rc, upstreamFmt, resp)
		if err != nil {
			resp.Body.Close()
			if accountUsage {
				_ = s.releaseQuota(ctx, reservation.ReservationId, "adaptor convert stream error")
			}
			result.statusCode = http.StatusInternalServerError
			result.err = fmt.Errorf("adaptor convert stream: %w", err)
			result.write = func(w http.ResponseWriter) { s.writeError(w, http.StatusInternalServerError, result.err.Error()) }
			return result
		}
		// Tee the converted SSE through a usage tracker so we can commit real
		// token counts. The converted output is already in the client's
		// protocol (chat/anthropic/responses), whose usage objects
		// extractRawUsage understands.
		usageTracker := newRawStreamUsageTracker(estimateRawUsage(rawBody))
		result.statusCode = http.StatusOK
		slotTransferred = true // slot released when the stream finishes, below
		result.write = func(w http.ResponseWriter) {
			defer releaseSlot()
			defer resp.Body.Close()
			w.Header().Set("Content-Type", "text/event-stream")
			w.Header().Set("Cache-Control", "no-cache")
			w.Header().Set("Connection", "keep-alive")
			w.WriteHeader(http.StatusOK)
			if flusher, ok := w.(http.Flusher); ok {
				_, _ = io.Copy(&flushWriter{w: w, flusher: flusher, usageTracker: usageTracker}, reader)
			} else {
				_, _ = io.Copy(&streamUsageWriter{w: w, usageTracker: usageTracker}, reader)
			}
			if accountUsage {
				actualUsage := usageTracker.Usage()
				logInput := usageLogInput{
					UserID:                plan.Auth.UserID,
					TokenID:               plan.Auth.TokenID,
					TokenName:             plan.Auth.TokenName,
					RequestID:             requestID,
					Endpoint:              string(inbound),
					ModelName:             plan.ResolvedModel,
					Quota:                 actualUsage.TotalTokens,
					PromptTokens:          actualUsage.PromptTokens,
					CompletionTokens:      actualUsage.CompletionTokens,
					CacheReadTokens:       actualUsage.CacheReadTokens,
					ChannelID:             plan.Channel.ID,
					SubscriptionAccountID: subscriptionAccountIDFromPlan(plan),
					IsStream:              true,
				}
				if err := s.commitQuotaAfterResponse(reservation.ReservationId, actualUsage.TotalTokens, true, logInput); err != nil {
					s.logPostResponseCommitError(err)
				} else {
					s.ingestUsageLogAfterResponse(logInput)
				}
			}
		}
		return result
	}

	// Non-streaming: convert and write.
	_, outBody, err := ad.ConvertResponse(rc, upstreamFmt, resp)
	resp.Body.Close()
	if err != nil {
		if accountUsage {
			_ = s.releaseQuota(ctx, reservation.ReservationId, "adaptor convert response error")
		}
		result.statusCode = http.StatusInternalServerError
		result.err = fmt.Errorf("adaptor convert response: %w", err)
		result.write = func(w http.ResponseWriter) { s.writeError(w, http.StatusInternalServerError, result.err.Error()) }
		return result
	}
	s.recordCodexQuotaSnapshot(ctx, plan, outBody)
	result.statusCode = http.StatusOK
	slotTransferred = true // slot released when the response has been written, below
	result.write = func(w http.ResponseWriter) {
		defer releaseSlot()
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write(outBody)

		// Commit real usage from the converted response body.
		if accountUsage {
			usage := extractRawUsage(outBody, estimateRawTokens(rawBody))
			logInput := usageLogInput{
				UserID:                plan.Auth.UserID,
				TokenID:               plan.Auth.TokenID,
				TokenName:             plan.Auth.TokenName,
				RequestID:             requestID,
				Endpoint:              string(inbound),
				ModelName:             plan.ResolvedModel,
				Quota:                 usage.TotalTokens,
				PromptTokens:          usage.PromptTokens,
				CompletionTokens:      usage.CompletionTokens,
				CacheReadTokens:       usage.CacheReadTokens,
				ChannelID:             plan.Channel.ID,
				SubscriptionAccountID: subscriptionAccountIDFromPlan(plan),
			}
			if err := s.commitQuotaAfterResponse(reservation.ReservationId, usage.TotalTokens, true, logInput); err != nil {
				s.logPostResponseCommitError(err)
			} else {
				s.ingestUsageLogAfterResponse(logInput)
			}
		}
	}
	return result
}

func (s *HTTPServer) subscriptionFailoverMaxAttempts() int {
	if s == nil || s.wsPoolCfg.failoverMaxSwitches <= 0 {
		return 3
	}
	return s.wsPoolCfg.failoverMaxSwitches + 1
}

// subscriptionAttemptSucceeded reports whether a terminal adaptor result was a
// successful upstream response (2xx, not a retryable/failed attempt). Only these
// bind a session -> account stickiness record.
func subscriptionAttemptSucceeded(result subscriptionAdaptorResult) bool {
	return result.err == nil && !result.retryable && result.statusCode >= 200 && result.statusCode < 300
}

// bindSubscriptionSession records that this conversation was served by the given
// subscription account so the next turn reuses it (docs #7). It is a no-op
// unless session stickiness is enabled with a sticky store and a non-empty
// session hash. The prior binding is read to classify the outcome: "hit" when
// the served account was already the bound one, "rebind" otherwise (including
// the first bind of a session).
func (s *HTTPServer) bindSubscriptionSession(ctx context.Context, group, sessionHash string, served *relaybiz.RelayPlan) {
	if s == nil || !s.subscriptionSessionStickyEnabled || s.wsSticky == nil {
		return
	}
	sessionHash = strings.TrimSpace(sessionHash)
	if sessionHash == "" {
		return
	}
	servedID := subscriptionAccountIDFromPlan(served)
	if servedID <= 0 {
		return
	}
	prior := s.wsSticky.LookupSessionChannel(ctx, group, sessionHash)
	s.wsSticky.BindSessionChannel(ctx, group, sessionHash, servedID, s.openAIWSStickyTTL())
	result := "rebind"
	if prior == servedID {
		result = "hit"
	}
	metrics.RelaySubscriptionStickyTotal.WithLabelValues(result, subscriptionMetricPlatform(served)).Inc()
}

func (s *HTTPServer) blockRuntimeAccount(ctx context.Context, accountID int64, statusCode int, err error) {
	if s == nil || s.runtimeBlocker == nil || accountID <= 0 {
		return
	}
	duration := s.runtimeBlockDuration(statusCode)
	if duration <= 0 {
		return
	}
	reason := fmt.Sprintf("status=%d", statusCode)
	if err != nil {
		reason = err.Error()
	}
	_ = s.runtimeBlocker.Block(ctx, accountID, time.Now().Add(duration), reason)
	metrics.RelayRuntimeBlocksTotal.WithLabelValues(subscriptionRetryReason(subscriptionAdaptorResult{statusCode: statusCode, err: err})).Inc()
	metrics.RelayRuntimeBlockActive.Set(float64(s.runtimeBlocker.Metrics().ActiveSize))
}

func (s *HTTPServer) selectSubscriptionFailoverPlan(ctx context.Context, base, current *relaybiz.RelayPlan, clientModel string, failed map[int64]bool) (*relaybiz.RelayPlan, error) {
	if s == nil || s.relayUsecase == nil || base == nil || base.Auth == nil {
		return nil, fmt.Errorf("relay usecase unavailable")
	}
	resolvedModel := base.ResolvedModel
	if resolvedModel == "" && current != nil {
		resolvedModel = current.ResolvedModel
	}
	next, err := s.relayUsecase.SelectSubscriptionFailover(ctx, base.Auth.Group, clientModel, resolvedModel, failed)
	if err != nil {
		return nil, err
	}
	next.Auth = base.Auth
	return next, nil
}

func isSubscriptionRuntimeRetryableStatus(statusCode int) bool {
	return statusCode == http.StatusTooManyRequests || statusCode >= 500
}

func writeUpstreamPassthrough(w http.ResponseWriter, statusCode int, header http.Header, body []byte) {
	if contentType := header.Get("Content-Type"); contentType != "" {
		w.Header().Set("Content-Type", contentType)
	} else {
		w.Header().Set("Content-Type", "application/json")
	}
	if retryAfter := header.Get("Retry-After"); retryAfter != "" {
		w.Header().Set("Retry-After", retryAfter)
	}
	w.WriteHeader(statusCode)
	_, _ = w.Write(body)
}

func (s *HTTPServer) recordCodexQuotaSnapshot(ctx context.Context, plan *relaybiz.RelayPlan, body []byte) {
	if s == nil || plan == nil || plan.Account == nil || plan.Account.Platform != "codex" {
		return
	}
	if s.accountQuotaRecorder == nil {
		metrics.RelayCodexQuotaSnapshotsTotal.WithLabelValues("recorder_missing").Inc()
		return
	}
	snapshot, ok := relayquota.ParseCodexSnapshot(body, time.Now())
	if !ok {
		metrics.RelayCodexQuotaSnapshotsTotal.WithLabelValues("parse_miss").Inc()
		return
	}
	accountID := plan.Account.ID
	if accountID <= 0 {
		return
	}
	if snapshot.PrimaryUsedPercent != nil {
		metrics.RelayCodexQuotaUsedPercent.WithLabelValues("primary").Set(*snapshot.PrimaryUsedPercent)
	}
	if snapshot.SecondaryUsedPercent != nil {
		metrics.RelayCodexQuotaUsedPercent.WithLabelValues("secondary").Set(*snapshot.SecondaryUsedPercent)
	}
	if err := s.accountQuotaRecorder.RecordAccountQuotaSnapshot(ctx, accountID, snapshot); err != nil {
		metrics.RelayCodexQuotaSnapshotsTotal.WithLabelValues("record_error").Inc()
		return
	}
	metrics.RelayCodexQuotaSnapshotsTotal.WithLabelValues("recorded").Inc()
	if relayquota.ShouldAutoPause(snapshot, 95, 100) {
		if err := s.accountQuotaRecorder.AutoPauseAccount(ctx, accountID, "codex quota exhausted"); err != nil {
			metrics.RelayCodexQuotaSnapshotsTotal.WithLabelValues("auto_pause_error").Inc()
			return
		}
		metrics.RelayCodexQuotaSnapshotsTotal.WithLabelValues("auto_paused").Inc()
	}
}

func subscriptionMetricPlatform(plan *relaybiz.RelayPlan) string {
	if plan != nil && plan.Account != nil && plan.Account.Platform != "" {
		return plan.Account.Platform
	}
	if plan != nil && plan.Channel != nil {
		switch plan.Channel.Type {
		case relayprovider.ChannelTypeCodexOAuth:
			return "codex"
		case relayprovider.ChannelTypeClaudeOAuth:
			return "claude"
		}
	}
	return "unknown"
}

func subscriptionAdaptorMetricResult(result subscriptionAdaptorResult) string {
	if result.statusCode >= 200 && result.statusCode < 300 && result.err == nil {
		return "success"
	}
	if result.retryable {
		return "retryable_" + statusClass(result.statusCode)
	}
	if result.statusCode > 0 {
		return statusClass(result.statusCode)
	}
	return "error"
}

func subscriptionRetryReason(result subscriptionAdaptorResult) string {
	if result.concurrencyFull {
		return "concurrency"
	}
	if result.retryableSameAccount {
		return "same_account"
	}
	if result.statusCode == http.StatusTooManyRequests {
		return "429"
	}
	if result.statusCode == passthrough.StatusOverloaded {
		return "529"
	}
	if result.statusCode > 0 {
		return statusClass(result.statusCode)
	}
	if result.err != nil {
		return "network_error"
	}
	return "unknown"
}

func statusClass(statusCode int) string {
	switch {
	case statusCode >= 500:
		return "5xx"
	case statusCode >= 400:
		return "4xx"
	case statusCode >= 300:
		return "3xx"
	case statusCode >= 200:
		return "2xx"
	default:
		return "network_error"
	}
}

// runtimeBlockDuration returns how long to cool an account down for a given
// upstream status, honouring the configured overrides (SetRuntimeBlockDurations)
// and falling back to the built-in defaults (429=5s, 401=2m, 5xx=2m). Other
// statuses are not blocked.
func (s *HTTPServer) runtimeBlockDuration(statusCode int) time.Duration {
	var cfg runtimeBlockConfig
	if s != nil {
		cfg = s.runtimeBlockCfg
	}
	switch {
	case statusCode == http.StatusTooManyRequests:
		if cfg.rateLimited > 0 {
			return cfg.rateLimited
		}
		return 5 * time.Second
	case statusCode == passthrough.StatusOverloaded:
		if cfg.overloaded > 0 {
			return cfg.overloaded
		}
		return 30 * time.Second
	case statusCode == http.StatusUnauthorized:
		if cfg.unauthorized > 0 {
			return cfg.unauthorized
		}
		return 2 * time.Minute
	case statusCode >= 500:
		if cfg.serverError > 0 {
			return cfg.serverError
		}
		return 2 * time.Minute
	}
	return 0
}

// accountTypeOrDefault returns the subscription account type, defaulting to
// "oauth" for legacy records that do not carry an explicit account_type.
func accountTypeOrDefault(t string) string {
	if t == "" {
		return "oauth"
	}
	return t
}

// subscriptionAccountMetadataFromPlan projects the first-class subscription
// account selected during planning into the metadata the relay context needs.
// This is the canonical path: the access token, upstream account id and
// fingerprint all come from the account entity, not from the generic channel.
func subscriptionAccountMetadataFromPlan(a *relaybiz.SubscriptionAccount) *relaycredential.SubscriptionAccountMetadata {
	if a == nil {
		return nil
	}
	platform := relaycredential.PlatformClaude
	switch a.Platform {
	case "codex":
		platform = relaycredential.PlatformCodex
	case "claude":
		platform = relaycredential.PlatformClaude
	}
	return &relaycredential.SubscriptionAccountMetadata{
		ID:          a.ID,
		AccessToken: a.AccessToken,
		AccountID:   a.AccountID,
		Platform:    platform,
		AccountType: accountTypeOrDefault(a.AccountType),
		Fingerprint: a.Fingerprint,
		GroupID:     a.Group,
	}
}

// subscriptionAccountIDFromPlan returns the real subscription account id
// selected during planning, or 0 for ordinary API-key channels.
func subscriptionAccountIDFromPlan(plan *relaybiz.RelayPlan) int64 {
	if plan == nil || plan.Account == nil {
		return 0
	}
	return plan.Account.ID
}

func fallbackSubscriptionAccountMetadata(plan *relaybiz.RelayPlan, ch *relaybiz.Channel) *relaycredential.SubscriptionAccountMetadata {
	meta := &relaycredential.SubscriptionAccountMetadata{
		ID:       ch.ID,
		GroupID:  ch.Group,
		Platform: relaycredential.PlatformClaude,
	}
	meta.AccountID = fmt.Sprintf("%d", ch.ID)
	switch ch.Type {
	case relayprovider.ChannelTypeCodexOAuth:
		meta.Platform = relaycredential.PlatformCodex
	case relayprovider.ChannelTypeClaudeOAuth:
		meta.Platform = relaycredential.PlatformClaude
	}
	meta.AccessToken = ch.Key
	return meta
}

package server

import (
	"context"
	stderrors "errors"
	"fmt"
	"os"
	"strings"
	"time"

	"go.uber.org/zap"

	billingv1 "micro-one-api/api/billing/v1"
	channelv1 "micro-one-api/api/channel/v1"
	identityv1 "micro-one-api/api/identity/v1"
	relaybiz "micro-one-api/internal/biz"
	applogger "micro-one-api/platform/logging"
	"micro-one-api/platform/metrics"

	"micro-one-api/pkg/safecast"
)

// canonicalUsageProducerEnabled reports the §5.3 relay producer feature gate
// (RELAY_CANONICAL_USAGE_PRODUCER). It must stay OFF until every billing/log
// consumer instance is upgraded to the v1 contract; regardless of the gate,
// the legacy token fields are always dual-written with their OLD meanings so
// mixed-version fleets keep behaving identically. Read per call so tests and
// rollout switches take effect without a restart of the process context.
func canonicalUsageProducerEnabled() bool {
	switch strings.ToLower(strings.TrimSpace(os.Getenv("RELAY_CANONICAL_USAGE_PRODUCER"))) {
	case "1", "true", "on", "enabled":
		return true
	}
	return false
}

// usageEnvelopeToProto maps the parser verdict onto the v1 wire envelope
// (§6.2). Candidates travel with ambiguous verdicts so billing can settle at
// the lower candidate cost without re-deriving semantics.
func usageEnvelopeToProto(env relaybiz.UsageEnvelope) *billingv1.UsageEnvelope {
	out := &billingv1.UsageEnvelope{
		Reported: &billingv1.ReportedUsageV1{
			PromptTokens:           env.Reported.PromptTokens,
			OutputTokens:           env.Reported.OutputTokens,
			CacheReadTokens:        env.Reported.CacheReadTokens,
			CacheCreation_5MTokens: env.Reported.CacheCreation5mTokens,
			CacheCreation_1HTokens: env.Reported.CacheCreation1hTokens,
			TotalTokens:            env.Reported.TotalTokens,
			SourceProtocol:         env.Reported.SourceProtocol,
			FieldShape:             env.Reported.FieldShape,
		},
		Semantics:      string(env.Semantics),
		ParseStatus:    string(env.ParseStatus),
		DecisionReason: env.DecisionReason,
	}
	if env.Canonical != nil {
		out.Canonical = canonicalUsageToProto(*env.Canonical)
	}
	if env.SubsetCandidate != nil {
		out.SubsetCandidate = canonicalUsageToProto(*env.SubsetCandidate)
	}
	if env.ExclusiveCandidate != nil {
		out.ExclusiveCandidate = canonicalUsageToProto(*env.ExclusiveCandidate)
	}
	return out
}

func canonicalUsageToProto(u relaybiz.CanonicalUsage) *billingv1.CanonicalUsageV1 {
	return &billingv1.CanonicalUsageV1{
		UncachedInputTokens:    u.UncachedInputTokens,
		CacheReadTokens:        u.CacheReadTokens,
		CacheCreation_5MTokens: u.CacheCreation5mTokens,
		CacheCreation_1HTokens: u.CacheCreation1hTokens,
		OutputTokens:           u.OutputTokens,
	}
}

// 配额管理方法

func postResponseContext() (context.Context, context.CancelFunc) {
	return detachedBillingContext(context.Background())
}

func detachedBillingContext(parent context.Context) (context.Context, context.CancelFunc) {
	if parent == nil {
		parent = context.Background()
	}
	return context.WithTimeout(context.WithoutCancel(parent), postResponseWriteTimeout)
}

func (s *HTTPServer) commitQuotaAfterResponseObserved(parent context.Context, reservationID string, actualTokens int64, success bool, details ...usageLogInput) error {
	if parent == nil {
		parent = context.Background()
	}
	ctx, cancel := detachedBillingContext(context.WithoutCancel(parent))
	defer cancel()
	return s.commitQuota(ctx, reservationID, actualTokens, success, details...)
}

func (s *HTTPServer) ingestUsageLogAfterResponse(in usageLogInput) {
	ctx, cancel := postResponseContext()
	defer cancel()
	s.ingestUsageLog(ctx, in)
}

func (s *HTTPServer) logPostResponseCommitError(err error) {
	if err != nil {
		applogger.Log.Warn("failed to commit quota after response was written", zap.Error(err))
	}
}

func (s *HTTPServer) reserveQuota(ctx context.Context, userID, requestID string, estimatedTokens int64, model, channelID string, subscriptionAccountID int64) (*billingv1.ReserveQuotaResponse, error) {
	// P3 #6: the model name used for billing is derived from the configured
	// billing_model_source. Callers ALREADY apply BillingModelName at each
	// call site (chat/anthropic/responses/ws) before passing the result as
	// `model` here — this function does NOT re-apply the source. The
	// "internal" name passed in is therefore the billing model name for the
	// configured source, not necessarily plan.ResolvedModel. The client-
	// facing name is threaded via the request context separately where needed.
	req := &billingv1.ReserveQuotaRequest{
		UserId:                userID,
		RequestId:             requestID,
		EstimatedTokens:       estimatedTokens,
		Model:                 model,
		ChannelId:             channelID,
		SubscriptionAccountId: subscriptionAccountID,
	}
	resp, err := s.billingClient.ReserveQuota(ctx, req)
	if err != nil {
		recordRelayQuotaOutcome(ctx, "reserve_error")
		return nil, err
	}
	if resp == nil || !resp.GetSuccess() {
		recordRelayQuotaOutcome(ctx, "reserve_error")
		return resp, stderrors.New(billingErrorMessage(resp, "reserve quota failed"))
	}
	recordRelayQuotaOutcome(ctx, "reserve_success")
	return resp, nil
}

func (s *HTTPServer) commitQuota(ctx context.Context, reservationID string, actualTokens int64, success bool, details ...usageLogInput) error {
	_, err := s.commitQuotaWithResponse(ctx, reservationID, actualTokens, success, details...)
	return err
}

func (s *HTTPServer) commitQuotaWithResponse(ctx context.Context, reservationID string, actualTokens int64, success bool, details ...usageLogInput) (*billingv1.CommitQuotaResponse, error) {
	if len(details) > 0 && details[0].Usage != nil {
		// Canonical (or conservative ambiguous-candidate) total is the only
		// count allowed into settlement and downstream usage counters. Raw
		// reported totals may omit exclusive cache tokens or double-count
		// subset cache tokens when the upstream omitted total_tokens.
		if canonicalTotal := details[0].billableTokens(); canonicalTotal > 0 {
			actualTokens = canonicalTotal
		}
	}
	req := &billingv1.CommitQuotaRequest{
		ReservationId: reservationID,
		ActualTokens:  actualTokens,
		Success:       success,
	}
	if len(details) > 0 {
		detail := details[0]
		req.TokenName = usageTokenName(detail)
		req.Endpoint = detail.Endpoint
		req.PromptTokens = detail.PromptTokens
		req.CompletionTokens = detail.CompletionTokens
		req.CacheReadTokens = detail.CacheReadTokens
		req.CacheCreation_5MTokens = detail.CacheCreation5mTokens
		req.CacheCreation_1HTokens = detail.CacheCreation1hTokens
		req.ElapsedTime = detail.ElapsedTime
		req.IsStream = detail.IsStream
		req.SubscriptionAccountId = detail.SubscriptionAccountID
		// v0.11.0 Phase 2 §2.2: stable upstream cost-key inputs.
		req.UpstreamModelId = detail.UpstreamModelID
		req.SourceKind = detail.SourceKind
		req.PromptExclusive = detail.PromptExclusive
		// §5.3 dual-write: the v1 envelope is sent only when the producer
		// feature gate is on; the legacy fields above always keep their old
		// meanings so old billing consumers are unaffected.
		if detail.Usage != nil && canonicalUsageProducerEnabled() {
			req.UsageContractVersion = relaybiz.UsageContractVersionV1
			req.Usage = usageEnvelopeToProto(*detail.Usage)
		}
	}
	billingCtx, cancel := detachedBillingContext(ctx)
	defer cancel()
	resp, err := s.billingClient.CommitQuota(billingCtx, req)
	if err != nil {
		recordRelayQuotaOutcome(ctx, "commit_error")
		setRelayObservationResult(ctx, "quota_error")
		return nil, err
	}
	if len(details) > 0 {
		// §5.2: feed the usage-semantics quarantine with the final attempt's
		// parser verdict. Best-effort — billing has already committed.
		// Use the same detached, bounded context as the financial commit. The
		// client may disconnect immediately after receiving the response; that
		// must not cancel the quarantine verdict for the final attempt.
		s.reportUsageSemanticVerdict(billingCtx, details[0])
	}
	if resp == nil || !resp.GetSuccess() {
		recordRelayQuotaOutcome(ctx, "commit_error")
		setRelayObservationResult(ctx, "quota_error")
		return resp, stderrors.New(billingErrorMessage(resp, "commit quota failed"))
	}
	// A failed upstream attempt is released/refunded by billing and therefore
	// has no consume ledger. Do not increment channel/model/token counters for
	// it; doing so was the source of the persistent channel-vs-ledger drift.
	if !success {
		recordRelayQuotaOutcome(ctx, "commit_failure_settlement")
		return resp, nil
	}
	// Phase 2.1 async billing: when the billing service enqueues the
	// settlement instead of committing synchronously, the returned amounts are
	// provisional (committed_amount=0). Downstream usage accounting must not
	// rely on provisional values; the worker will finalize the ledger and
	// subscription usage on its own.
	if resp.GetAsyncEnqueued() {
		applogger.Log.Info("commit quota enqueued for async settlement", zap.String("reservation_id", reservationID), zap.Int64("actual_tokens", actualTokens))
		// Channel token usage does not depend on the provisional
		// committed_amount; record it now so the channel stats stay accurate
		// even when billing is settled asynchronously. Subscription-account
		// and session-window accounting must still wait for the authoritative
		// worker result, so they are skipped here.
		if len(details) > 0 {
			s.recordChannelUsageFromDetail(ctx, details[0], actualTokens, reservationID)
			s.recordModelUsage(ctx, details[0].ModelName, actualTokens, details[0].ElapsedTime, false)
			// High #5: consume per-key token quota even on the async path.
			s.consumeTokenQuota(ctx, details[0].UserID, details[0].TokenID, actualTokens)
		}
		recordRelayQuotaOutcome(ctx, "commit_async")
		return resp, nil
	}
	if len(details) > 0 {
		detail := details[0]
		s.recordChannelUsageFromDetail(ctx, detail, actualTokens, reservationID)
		s.recordModelUsage(ctx, detail.ModelName, actualTokens, detail.ElapsedTime, false)
		costUSD := amountUnitsToUSD(resp.GetCommittedAmount())
		s.recordSubscriptionAccountQuotaUsage(ctx, detail.SubscriptionAccountID, reservationID, costUSD)
		s.recordSubscriptionSessionWindowUsage(ctx, detail, reservationID, costUSD)
		// recordSubscriptionUsage is a no-op on the dual-track
		// path: the billing layer's CommitQuotaWithUsage already
		// wrote the subscription usage via the row-locked
		// RecordUsageForSubscriptionInTx call inside the same
		// transaction. Recording again would double-count the
		// window. The legacy path is preserved.
		s.recordSubscriptionUsage(ctx, detail.UserID, actualTokens)
		// Enforce per-key quota after billing settles. Persistent identity
		// failures fail-close this token in the relay until validation recovers.
		s.consumeTokenQuota(ctx, detail.UserID, detail.TokenID, actualTokens)
	}
	recordRelayQuotaOutcome(ctx, "commit_success")
	return resp, nil
}

func (s *HTTPServer) recordSubscriptionAccountQuotaUsage(ctx context.Context, accountID int64, reservationID string, costUSD float64) {
	if s == nil || s.channelClient == nil || accountID <= 0 || costUSD <= 0 {
		return
	}
	channelCtx, cancel := detachedBillingContext(ctx)
	defer cancel()
	resp, err := s.channelClient.RecordSubscriptionAccountQuotaUsage(channelCtx, &channelv1.RecordSubscriptionAccountQuotaUsageRequest{
		AccountId:     accountID,
		CostUsd:       costUSD,
		ReservationId: reservationID,
		CostSource:    "billing_commit",
	})
	if err != nil {
		applogger.Log.Warn("failed to record subscription account quota usage", zap.Int64("account_id", accountID), zap.Error(err))
		return
	}
	if resp != nil && !resp.GetSuccess() {
		applogger.Log.Warn("subscription account quota usage rejected", zap.Int64("account_id", accountID), zap.String("message", resp.GetMessage()))
	}
}

func (s *HTTPServer) recordSubscriptionSessionWindowUsage(ctx context.Context, detail usageLogInput, reservationID string, costUSD float64) {
	if s == nil || detail.SubscriptionAccountID <= 0 || detail.SessionWindowLimitUSD <= 0 || strings.TrimSpace(detail.SessionHash) == "" || costUSD <= 0 {
		return
	}
	if s.sessionWindow == nil {
		s.sessionWindow = newSubscriptionSessionWindowStore(nil)
	}
	s.sessionWindow.RecordUsage(ctx, detail.Group, detail.SessionHash, detail.SubscriptionAccountID, reservationID, costUSD, s.openAIWSStickyTTL())
}

// consumeTokenQuota is the per-key quota enforcement path (review High #5).
// It calls identity-service to decrement the token's RemainQuota by `amount`.
// Billing has already committed when this runs, so transient failures are
// retried. Persistent failure fail-closes the token locally until identity can
// be checked again instead of allowing unmetered follow-up requests.
func (s *HTTPServer) consumeTokenQuota(ctx context.Context, userID, tokenID, amount int64) {
	if s == nil || s.identityClient == nil || tokenID <= 0 || amount <= 0 {
		return
	}
	tokenCtx, cancel := detachedBillingContext(ctx)
	defer cancel()
	var resp *identityv1.ConsumeTokenQuotaReply
	var err error
	consumed := false
	for attempt := 0; attempt < 3; attempt++ {
		resp, err = s.identityClient.ConsumeTokenQuota(tokenCtx, &identityv1.ConsumeTokenQuotaRequest{
			UserId: userID, TokenId: tokenID, Amount: amount,
		})
		if err == nil && resp != nil && resp.GetSuccess() {
			consumed = true
			break
		}
		if attempt < 2 {
			timer := time.NewTimer(time.Duration(attempt+1) * 50 * time.Millisecond)
			select {
			case <-tokenCtx.Done():
				timer.Stop()
				attempt = 2
			case <-timer.C:
			}
		}
	}
	if !consumed {
		if s.tokenQuotaBlocker != nil {
			s.tokenQuotaBlocker.BlockTokenQuota(tokenID, time.Minute)
		}
		fields := []zap.Field{zap.Int64("token_id", tokenID)}
		if err != nil {
			fields = append(fields, zap.Error(err))
		} else if resp != nil {
			fields = append(fields, zap.String("message", resp.GetMessage()))
		}
		applogger.Log.Warn("consume token quota failed; token temporarily blocked", fields...)
		return
	}
	if s.tokenQuotaBlocker != nil {
		if resp.GetRemaining() == 0 {
			s.tokenQuotaBlocker.BlockTokenQuota(tokenID, time.Hour)
		} else {
			s.tokenQuotaBlocker.ClearTokenQuotaBlock(tokenID)
		}
	}
}

func (s *HTTPServer) recordSubscriptionUsage(ctx context.Context, userID int64, amount int64) {
	// Billing CommitQuotaWithUsage records subscription usage transactionally.
	// Keeping a relay-side write would double-count subscription windows.
	metrics.SubscriptionUsageRecordsTotal.WithLabelValues("skipped").Inc()
}

// recordChannelUsageFromDetail records channel token usage only for
// traffic that actually executed on a real channel. Subscription-sourced
// traffic (SourceKind == "subscription") is billed through the dedicated
// subscription-account / session-window paths; its ChannelID is a synthetic
// value derived from the subscription account id, so recording it as a
// channel would always fail with "channel not found" on the channel-service
// side. Model-usage stats apply to both sources and are handled separately.
func (s *HTTPServer) recordChannelUsageFromDetail(ctx context.Context, detail usageLogInput, quota int64, reservationIDs ...string) {
	if detail.SourceKind == relaybiz.UpstreamSourceSubscription {
		metrics.SubscriptionUsageRecordsTotal.WithLabelValues("skipped_channel_stats").Inc()
		return
	}
	reservationID := ""
	if len(reservationIDs) > 0 {
		reservationID = reservationIDs[0]
	}
	s.recordChannelUsage(ctx, detail.ChannelID, quota, reservationID)
}

func (s *HTTPServer) recordChannelUsage(ctx context.Context, channelID int64, quota int64, reservationID string) {
	if s.channelClient == nil || channelID <= 0 || quota <= 0 {
		return
	}
	channelCtx, cancel := detachedBillingContext(ctx)
	defer cancel()
	var resp *channelv1.RecordChannelUsageResponse
	var err error
	for attempt := 0; attempt < 3; attempt++ {
		resp, err = s.channelClient.RecordChannelUsage(channelCtx, &channelv1.RecordChannelUsageRequest{
			ChannelId:     channelID,
			Quota:         quota,
			ReservationId: reservationID,
		})
		if err == nil && resp != nil && resp.GetSuccess() {
			return
		}
		if attempt < 2 {
			time.Sleep(time.Duration(attempt+1) * 50 * time.Millisecond)
		}
	}
	fields := []zap.Field{zap.Int64("channel_id", channelID), zap.Int64("quota", quota), zap.String("reservation_id", reservationID)}
	if err != nil {
		fields = append(fields, zap.Error(err))
	} else if resp != nil {
		fields = append(fields, zap.String("message", resp.GetMessage()))
	}
	applogger.Log.Warn("failed to record channel usage after retries", fields...)
}

// recordModelUsage records a usage event to the model usage stats table via
// the channel-service gRPC client. Best-effort: errors are logged but never
// propagated. Called from commitQuotaWithResponse (the shared post-response
// billing path) alongside recordChannelUsage so model stats stay in sync
// with channel stats. All HTTP entry points (chat, anthropic, responses,
// raw, ws, adaptor) go through commitQuota -> commitQuotaWithResponse, so
// a single call site here covers every path without double-counting.
func (s *HTTPServer) recordModelUsage(ctx context.Context, modelID string, tokenCount int64, latencyMs int64, isError bool) {
	if s.channelClient == nil || modelID == "" {
		return
	}
	channelCtx, cancel := detachedBillingContext(ctx)
	defer cancel()
	req := &channelv1.RecordModelUsageRequest{
		ModelId:      modelID,
		TokenCount:   tokenCount,
		AvgLatency:   safecast.Int64ToInt32Saturating(latencyMs),
		RequestCount: 1,
	}
	if isError {
		req.ErrorCount = 1
	}
	resp, err := s.channelClient.RecordModelUsage(channelCtx, req)
	if err != nil {
		applogger.Log.Warn("failed to record model usage", zap.String("model", modelID), zap.Error(err))
		return
	}
	if resp != nil && !resp.GetSuccess() {
		applogger.Log.Warn("failed to record model usage", zap.String("model", modelID), zap.String("message", resp.GetMessage()))
	}
}

func usageTokenName(in usageLogInput) string {
	if strings.TrimSpace(in.TokenName) != "" {
		return strings.TrimSpace(in.TokenName)
	}
	return fmt.Sprintf("token-%d", in.TokenID)
}

// reportUsageSemanticVerdict feeds the §5.2 quarantine control plane with the
// FINAL attempt's parser verdict (never the initial plan's guess). Failures
// are logged, never propagated: billing has already committed and a
// control-plane blip must not affect the user.
func (s *HTTPServer) reportUsageSemanticVerdict(ctx context.Context, detail usageLogInput) {
	if s == nil || s.channelClient == nil || detail.Usage == nil {
		return
	}
	env := detail.Usage
	if env.ParseStatus != relaybiz.UsageParseVerified && env.ParseStatus != relaybiz.UsageParseAmbiguous {
		return
	}
	sourceKind := detail.SourceKind
	var sourceID int64
	if sourceKind == relaybiz.UpstreamSourceSubscription {
		sourceID = detail.SubscriptionAccountID
	} else {
		sourceKind = relaybiz.UpstreamSourceChannel
		sourceID = detail.ChannelID
	}
	if sourceID <= 0 || strings.TrimSpace(detail.UpstreamModelID) == "" {
		return
	}
	if env.ParseStatus == relaybiz.UsageParseAmbiguous && metrics.UsageSemanticSourceIsolationTotal != nil {
		metrics.UsageSemanticSourceIsolationTotal.WithLabelValues(sourceKind, "ambiguous_reported").Inc()
	}
	resp, err := s.channelClient.RecordUsageSemanticVerdict(ctx, &channelv1.RecordUsageSemanticVerdictRequest{
		SourceKind:      sourceKind,
		SourceId:        sourceID,
		UpstreamModelId: detail.UpstreamModelID,
		AdapterProtocol: env.Reported.SourceProtocol,
		ParseStatus:     string(env.ParseStatus),
		Reason:          env.DecisionReason,
	})
	if err != nil {
		applogger.Log.Warn("failed to report usage semantic verdict",
			zap.String("source_kind", sourceKind), zap.Int64("source_id", sourceID), zap.Error(err))
		return
	}
	if resp != nil && resp.GetBlocked() {
		if metrics.UsageSemanticSourceIsolationTotal != nil {
			metrics.UsageSemanticSourceIsolationTotal.WithLabelValues(sourceKind, "blocked").Inc()
		}
		applogger.Log.Warn("usage semantic source blocked by quarantine",
			zap.String("source_kind", sourceKind),
			zap.Int64("source_id", sourceID),
			zap.String("upstream_model_id", detail.UpstreamModelID),
			zap.Int64("blocked_until_ms", resp.GetBlockedUntil()),
		)
	}
}

func (s *HTTPServer) releaseQuota(ctx context.Context, reservationID, reason string) error {
	req := &billingv1.ReleaseQuotaRequest{
		ReservationId: reservationID,
		Reason:        reason,
	}
	billingCtx, cancel := detachedBillingContext(ctx)
	defer cancel()
	resp, err := s.billingClient.ReleaseQuota(billingCtx, req)
	if err != nil {
		recordRelayQuotaOutcome(ctx, "release_error")
		return err
	}
	if resp == nil || !resp.GetSuccess() {
		recordRelayQuotaOutcome(ctx, "release_error")
		return stderrors.New(billingErrorMessage(resp, "release quota failed"))
	}
	recordRelayQuotaOutcome(ctx, "release_success")
	return nil
}

func billingErrorMessage(resp billingFailure, fallback string) string {
	if resp == nil {
		return fallback
	}
	if msg := strings.TrimSpace(resp.GetErrorMessage()); msg != "" {
		return msg
	}
	return fallback
}

type billingFailure interface {
	GetErrorMessage() string
}

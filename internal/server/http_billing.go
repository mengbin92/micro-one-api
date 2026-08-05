package server

import (
	"context"
	stderrors "errors"
	"fmt"
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

func (s *HTTPServer) commitQuotaAfterResponse(reservationID string, actualTokens int64, success bool, details ...usageLogInput) error {
	ctx, cancel := postResponseContext()
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
		return nil, err
	}
	if resp == nil || !resp.GetSuccess() {
		return resp, stderrors.New(billingErrorMessage(resp, "reserve quota failed"))
	}
	return resp, nil
}

func (s *HTTPServer) commitQuota(ctx context.Context, reservationID string, actualTokens int64, success bool, details ...usageLogInput) error {
	_, err := s.commitQuotaWithResponse(ctx, reservationID, actualTokens, success, details...)
	return err
}

func (s *HTTPServer) commitQuotaWithResponse(ctx context.Context, reservationID string, actualTokens int64, success bool, details ...usageLogInput) (*billingv1.CommitQuotaResponse, error) {
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
	}
	billingCtx, cancel := detachedBillingContext(ctx)
	defer cancel()
	resp, err := s.billingClient.CommitQuota(billingCtx, req)
	if err != nil {
		return nil, err
	}
	if resp == nil || !resp.GetSuccess() {
		return resp, stderrors.New(billingErrorMessage(resp, "commit quota failed"))
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
			s.recordChannelUsageFromDetail(ctx, details[0], actualTokens)
			s.recordModelUsage(ctx, details[0].ModelName, actualTokens, details[0].ElapsedTime, false)
			// High #5: consume per-key token quota even on the async path.
			s.consumeTokenQuota(ctx, details[0].UserID, details[0].TokenID, actualTokens)
		}
		return resp, nil
	}
	if len(details) > 0 {
		detail := details[0]
		s.recordChannelUsageFromDetail(ctx, detail, actualTokens)
		s.recordModelUsage(ctx, detail.ModelName, actualTokens, detail.ElapsedTime, false)
		costUSD := quotaToUSD(resp.GetCommittedAmount())
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

func (s *HTTPServer) recordSubscriptionUsage(ctx context.Context, userID int64, quota int64) {
	// Billing CommitQuotaWithUsage records subscription usage transactionally.
	// Keeping a relay-side write would double-count subscription windows.
	metrics.SubscriptionUsageRecordsTotal.WithLabelValues("skipped").Inc()
}

func quotaToUSD(quota int64) float64 {
	if quota <= 0 {
		return 0
	}
	perUSD := quotaPerUSDFromEnv()
	if perUSD <= 0 {
		perUSD = defaultQuotaPerUSD
	}
	return float64(quota) / float64(perUSD)
}

// recordChannelUsageFromDetail records channel token usage only for
// traffic that actually executed on a real channel. Subscription-sourced
// traffic (SourceKind == "subscription") is billed through the dedicated
// subscription-account / session-window paths; its ChannelID is a synthetic
// value derived from the subscription account id, so recording it as a
// channel would always fail with "channel not found" on the channel-service
// side. Model-usage stats apply to both sources and are handled separately.
func (s *HTTPServer) recordChannelUsageFromDetail(ctx context.Context, detail usageLogInput, quota int64) {
	if detail.SourceKind == relaybiz.UpstreamSourceSubscription {
		metrics.SubscriptionUsageRecordsTotal.WithLabelValues("skipped_channel_stats").Inc()
		return
	}
	s.recordChannelUsage(ctx, detail.ChannelID, quota)
}

func (s *HTTPServer) recordChannelUsage(ctx context.Context, channelID int64, quota int64) {
	if s.channelClient == nil || channelID <= 0 || quota <= 0 {
		return
	}
	channelCtx, cancel := detachedBillingContext(ctx)
	defer cancel()
	resp, err := s.channelClient.RecordChannelUsage(channelCtx, &channelv1.RecordChannelUsageRequest{
		ChannelId: channelID,
		Quota:     quota,
	})
	if err != nil {
		applogger.Log.Warn("failed to record channel usage", zap.Int64("channel_id", channelID), zap.Int64("quota", quota), zap.Error(err))
		return
	}
	if resp != nil && !resp.GetSuccess() {
		applogger.Log.Warn("failed to record channel usage", zap.Int64("channel_id", channelID), zap.Int64("quota", quota), zap.String("message", resp.GetMessage()))
	}
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

func (s *HTTPServer) releaseQuota(ctx context.Context, reservationID, reason string) error {
	req := &billingv1.ReleaseQuotaRequest{
		ReservationId: reservationID,
		Reason:        reason,
	}
	billingCtx, cancel := detachedBillingContext(ctx)
	defer cancel()
	resp, err := s.billingClient.ReleaseQuota(billingCtx, req)
	if err != nil {
		return err
	}
	if resp == nil || !resp.GetSuccess() {
		return stderrors.New(billingErrorMessage(resp, "release quota failed"))
	}
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

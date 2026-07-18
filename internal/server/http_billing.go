package server

import (
	"context"
	stderrors "errors"
	"fmt"
	"strings"

	"go.uber.org/zap"

	billingv1 "micro-one-api/api/billing/v1"
	channelv1 "micro-one-api/api/channel/v1"
	applogger "micro-one-api/platform/logging"
	"micro-one-api/platform/metrics"
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
	if err != nil && applogger.Log != nil {
		applogger.Log.Warn("failed to commit quota after response was written", zap.Error(err))
	}
}

func (s *HTTPServer) reserveQuota(ctx context.Context, userID, requestID string, estimatedTokens int64, model, channelID string, subscriptionAccountID int64) (*billingv1.ReserveQuotaResponse, error) {
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
		req.ElapsedTime = detail.ElapsedTime
		req.IsStream = detail.IsStream
		req.SubscriptionAccountId = detail.SubscriptionAccountID
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
		if applogger.Log != nil {
			applogger.Log.Info("commit quota enqueued for async settlement", zap.String("reservation_id", reservationID), zap.Int64("actual_tokens", actualTokens))
		}
		// Channel token usage does not depend on the provisional
		// committed_amount; record it now so the channel stats stay accurate
		// even when billing is settled asynchronously. Subscription-account
		// and session-window accounting must still wait for the authoritative
		// worker result, so they are skipped here.
		if len(details) > 0 {
			s.recordChannelUsage(ctx, details[0].ChannelID, actualTokens)
		}
		return resp, nil
	}
	if len(details) > 0 {
		detail := details[0]
		s.recordChannelUsage(ctx, detail.ChannelID, actualTokens)
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
		if applogger.Log != nil {
			applogger.Log.Warn("failed to record subscription account quota usage", zap.Int64("account_id", accountID), zap.Error(err))
		}
		return
	}
	if resp != nil && !resp.GetSuccess() && applogger.Log != nil {
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
	if err != nil && applogger.Log != nil {
		applogger.Log.Warn("failed to record channel usage", zap.Int64("channel_id", channelID), zap.Int64("quota", quota), zap.Error(err))
		return
	}
	if resp != nil && !resp.GetSuccess() && applogger.Log != nil {
		applogger.Log.Warn("failed to record channel usage", zap.Int64("channel_id", channelID), zap.Int64("quota", quota), zap.String("message", resp.GetMessage()))
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

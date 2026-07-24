package server

import (
	"context"
	stderrors "errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/bytedance/sonic"
	"go.uber.org/zap"

	billingv1 "micro-one-api/api/billing/v1"
	relayprovider "micro-one-api/domain/upstream/provider"
	relaybiz "micro-one-api/internal/biz"
	applogger "micro-one-api/platform/logging"
)

func (s *HTTPServer) handleChatCompletions(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		s.writeError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}

	authHeader := r.Header.Get("Authorization")
	if authHeader == "" {
		s.writeError(w, http.StatusUnauthorized, "missing authorization header")
		return
	}

	if !strings.HasPrefix(authHeader, "Bearer ") {
		s.writeError(w, http.StatusUnauthorized, "invalid authorization header format")
		return
	}

	token := strings.TrimPrefix(authHeader, "Bearer ")
	if token == "" {
		s.writeError(w, http.StatusUnauthorized, "missing token")
		return
	}

	// Read the original body so session_hash (which the typed struct does not
	// carry) survives for session stickiness; then decode from those bytes.
	originalBody, err := io.ReadAll(io.LimitReader(r.Body, 10*1024*1024))
	if err != nil {
		s.writeError(w, http.StatusBadRequest, "failed to read request body")
		return
	}
	var req relayprovider.ChatCompletionsRequest
	if err := sonic.Unmarshal(originalBody, &req); err != nil {
		s.writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}

	if req.Model == "" {
		s.writeError(w, http.StatusBadRequest, "model is required")
		return
	}

	sessionHash := ""
	if s.subscriptionSessionStickyEnabled {
		sessionHash = extractSessionHashFromRequest(r, originalBody)
	}

	// Delegate auth, model validation, model mapping, and channel selection to biz layer
	plan, err := s.relayUsecase.Plan(r.Context(), relaybiz.RelayRequest{
		Token:       token,
		Model:       req.Model,
		SessionHash: sessionHash,
	})
	if err != nil {
		s.handleRelayPlanError(w, err)
		return
	}
	if err := s.checkUserRPM(r.Context(), plan.Auth.UserID); err != nil {
		s.writeUserRPMError(w)
		return
	}

	// Subscription-account channels (Codex/Claude OAuth) are routed through the
	// hybrid adaptor layer when the feature flag is on. The adaptor owns the
	// full upstream interaction (protocol conversion, identity mimicry, OAuth
	// token, stream bridging). API-key channels fall through to the existing
	// provider-factory path below.
	if s.hybridAdaptorEnabled && plan.Channel != nil && isSubscriptionChannel(plan.Channel.Type) {
		// req.Model still holds the client-facing model name at this point (it is
		// reassigned to the resolved model only further below). Reconstruct the raw
		// body from the decoded request since the original body was consumed.
		rawBody, _ := sonic.Marshal(req)
		s.handleChatCompletionsViaAdaptor(w, r, plan, req.Model, rawBody, sessionHash)
		return
	}

	clientModel := req.Model

	// Use resolved model name for upstream calls
	req.Model = plan.ResolvedModel

	// Use RetryExecutor for upstream calls with channel fallback
	retryExecutor := s.relayUsecase.NewRetryExecutor()
	result := retryExecutor.ExecuteWithInitialChannel(r.Context(), plan.Auth.Group, plan.ResolvedModel, plan.Channel, func(ctx context.Context, ch *relaybiz.Channel) error {
		startedAt := time.Now()
		// Reserve quota
		requestID := generateRequestID()
		estimatedTokens := s.estimateTokens(&req)
		// P3 #6: derive the billing model name from billing_model_source.
		billingModel := s.BillingModelName(clientModel, plan.ResolvedModel, plan.ResolvedModel)
		reservation, reserveErr := s.reserveQuota(ctx, fmt.Sprintf("%d", plan.Auth.UserID), requestID, estimatedTokens, billingModel, fmt.Sprintf("%d", ch.ID), subscriptionAccountIDFromPlan(plan))
		if reserveErr != nil {
			return &relaybiz.RetryableError{Status: http.StatusPaymentRequired, Err: reserveErr}
		}

		provider, provErr := s.providerFactory.CreateProviderWithConfig(ch.Type, ch.BaseURL, ch.Key, relayprovider.ProviderConfig{
			APIVersion: ch.Config.APIVersion,
		})
		if provErr != nil {
			_ = s.releaseQuota(ctx, reservation.ReservationId, "failed to create provider")
			return fmt.Errorf("failed to create provider: %w", provErr)
		}

		if req.Stream {
			return s.handleStreamingResponse(w, r, provider, &req, reservation, usageLogInput{
				UserID:                plan.Auth.UserID,
				TokenID:               plan.Auth.TokenID,
				TokenName:             plan.Auth.TokenName,
				RequestID:             requestID,
				Endpoint:              "/v1/chat/completions",
				ModelName:             s.BillingModelName(clientModel, plan.ResolvedModel, plan.ResolvedModel),
				ChannelID:             ch.ID,
				SubscriptionAccountID: subscriptionAccountIDFromPlan(plan),
				IsStream:              true,
			})
		}

		// Non-streaming call
		resp, callErr := provider.ChatCompletions(ctx, &req)
		if callErr != nil {
			_ = s.releaseQuota(ctx, reservation.ReservationId, "upstream error")
			return callErr
		}

		// Success — commit quota and return
		actualTokens := s.calculateActualTokens(resp)
		logInput := usageLogInput{
			UserID:           plan.Auth.UserID,
			TokenID:          plan.Auth.TokenID,
			TokenName:        plan.Auth.TokenName,
			RequestID:        requestID,
			Endpoint:         "/v1/chat/completions",
			ModelName:        s.BillingModelName(clientModel, plan.ResolvedModel, plan.ResolvedModel),
			Quota:            actualTokens,
			PromptTokens:     int64(resp.Usage.PromptTokens),
			CompletionTokens: int64(resp.Usage.CompletionTokens),
			CacheReadTokens:  cacheReadTokensFromProviderUsage(resp.Usage),
			ChannelID:        ch.ID,
			ElapsedTime:      time.Since(startedAt).Milliseconds(),
			IsStream:         false,
		}
		if err := s.commitQuota(ctx, reservation.ReservationId, actualTokens, true, logInput); err != nil {
			return err
		}
		logUpstreamUsage(logInput)
		s.ingestUsageLog(ctx, logInput)
		s.writeJSON(w, http.StatusOK, resp)
		return nil
	})

	if result.Err != nil {
		s.writeError(w, mapUpstreamError(relaybiz.UpstreamStatus(result.Err)), "upstream service error")
	}
}

func (s *HTTPServer) handleStreamingResponse(w http.ResponseWriter, r *http.Request, provider relayprovider.Provider, req *relayprovider.ChatCompletionsRequest, reservation *billingv1.ReserveQuotaResponse, logInput usageLogInput) error {
	startedAt := time.Now()
	chunkChan, err := provider.ChatCompletionsStream(r.Context(), req)
	if err != nil {
		// 流式请求失败，释放预扣配额
		_ = s.releaseQuota(r.Context(), reservation.ReservationId, "upstream stream error")
		return err
	}

	flusher, ok := w.(http.Flusher)
	if !ok {
		// 流式不支持，释放预扣配额
		_ = s.releaseQuota(r.Context(), reservation.ReservationId, "streaming not supported")
		return stderrors.New("streaming not supported")
	}

	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	w.Header().Set("Transfer-Encoding", "chunked")

	totalTokens := int64(0)
	promptTokens := int64(0)
	completionTokens := int64(0)
	cacheReadTokens := int64(0)
	estimatedTokens := int64(0)
	streamError := false

	for chunk := range chunkChan {
		if chunk.Usage.TotalTokens > 0 {
			totalTokens = int64(chunk.Usage.TotalTokens)
			promptTokens = int64(chunk.Usage.PromptTokens)
			completionTokens = int64(chunk.Usage.CompletionTokens)
			cacheReadTokens = cacheReadTokensFromProviderUsage(chunk.Usage)
		}
		for _, choice := range chunk.Choices {
			estimatedTokens += int64(len(choice.Delta.Content) / 4)
		}

		jsonData, err := sonic.Marshal(chunk)
		if err != nil {
			if applogger.Log != nil {
				applogger.Log.Warn("failed to marshal chunk", zap.Error(err))
			}
			continue
		}

		fmt.Fprintf(w, "data: %s\n\n", string(jsonData))
		flusher.Flush()
	}

	fmt.Fprintf(w, "data: [DONE]\n\n")
	flusher.Flush()

	// 流式请求完成，提交配额
	if !streamError {
		if totalTokens == 0 {
			totalTokens = estimatedTokens
			completionTokens = estimatedTokens
		}
		logInput.Quota = totalTokens
		logInput.PromptTokens = promptTokens
		logInput.CompletionTokens = completionTokens
		logInput.CacheReadTokens = cacheReadTokens
		logInput.ElapsedTime = time.Since(startedAt).Milliseconds()
		if logInput.Endpoint == "" {
			logInput.Endpoint = "/v1/chat/completions"
		}
		if err := s.commitQuotaAfterResponse(reservation.ReservationId, totalTokens, true, logInput); err != nil {
			s.logPostResponseCommitError(err)
		} else {
			logUpstreamUsage(logInput)
			s.ingestUsageLogAfterResponse(logInput)
		}
	} else {
		_ = s.releaseQuota(r.Context(), reservation.ReservationId, "stream error")
	}
	return nil
}

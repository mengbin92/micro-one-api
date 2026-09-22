package server

import (
	"context"
	stderrors "errors"
	"fmt"
	"net/http"
	"strings"
	"time"

	"go.uber.org/zap"
	"micro-one-api/pkg/jsonx"

	billingv1 "micro-one-api/api/billing/v1"
	"micro-one-api/domain/requesttrace"
	relayprovider "micro-one-api/domain/upstream/provider"
	relayadaptor "micro-one-api/internal/adaptor"
	relaybiz "micro-one-api/internal/biz"
	applogger "micro-one-api/platform/logging"
	"micro-one-api/platform/metrics"
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
	originalBody, err := readRouteRequestBody(r)
	if err != nil {
		s.writeRequestBodyError(w, r, err)
		return
	}
	var req relayprovider.ChatCompletionsRequest
	if err := jsonx.Unmarshal(originalBody, &req); err != nil {
		s.writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}

	if req.Model == "" {
		s.writeError(w, http.StatusBadRequest, "model is required")
		return
	}
	setRelayObservationStream(r.Context(), req.Stream)

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

	clientModel := req.Model

	// Use resolved model name for upstream calls
	req.Model = plan.ResolvedModel

	// Use RetryExecutor for upstream calls with channel fallback
	retryStartedAt := time.Now()
	retryExecutor := s.relayUsecase.NewRetryExecutor()
	if s.hybridAdaptorEnabled {
		retryExecutor.WithCrossSourceFallback().WithExternalSubscriptionHealth()
	}
	var subscriptionResult *subscriptionAdaptorResult
	var subscriptionErr error
	var subscriptionChannel *relaybiz.Channel
	var extraAttempts int32
	result := retryExecutor.ExecuteWithCandidates(r.Context(), plan, subscriptionAccountIDFromPlan(plan), func(ctx context.Context, ch *relaybiz.Channel) error {
		if subscriptionResult != nil && subscriptionResult.retryable && !relaybiz.SameRoutingSource(subscriptionChannel, ch) {
			metrics.RelaySubscriptionFailoverTotal.WithLabelValues(subscriptionRetryReason(*subscriptionResult), "switched").Inc()
		}
		subscriptionResult = nil
		subscriptionErr = nil
		trace := requesttrace.FromContext(ctx)
		trace.Number += extraAttempts
		ctx = requesttrace.WithAttempt(ctx, trace)
		if s.hybridAdaptorEnabled && isSubscriptionChannel(ch.Type) {
			attemptPlan, err := relayPlanForAttempt(ctx, s.relayUsecase, plan, ch, clientModel)
			if err != nil {
				return err
			}
			attempt := s.runSubscriptionAttempt(r.WithContext(ctx), attemptPlan, clientModel, originalBody, relayadaptor.FormatOpenAIChatCompletions, sessionHash)
			extraAttempts += attempt.attemptCount - 1
			subscriptionResult = &attempt
			subscriptionChannel = ch
			if subscriptionAttemptSucceeded(attempt) {
				attempt.write(w)
				s.bindSubscriptionSession(ctx, plan.Auth.Group, sessionHash, attemptPlan)
				return nil
			}
			if attempt.retryable && !attempt.concurrencyFull && !attempt.rpmFull && !attempt.sessionWindowFull && !isSubscriptionRateLimitStatus(attempt.statusCode) {
				s.blockRuntimeAccount(ctx, subscriptionAccountIDFromPlan(attemptPlan), attempt.statusCode, attempt.err)
			}
			err = &relaybiz.RetryableError{Status: attempt.statusCode, Err: attempt.err, Retryable: &attempt.retryable}
			if attempt.upstreamSucceeded {
				err = relaybiz.MarkPostForwardError(err)
			}
			subscriptionErr = err
			return err
		}
		startedAt := time.Now()
		// Reserve quota
		requestID := generateRequestID()
		estimatedTokens := s.estimateTokens(&req)
		// re-apply the retried channel's per-channel model mapping so
		// the upstream body and billing use the new channel's mapping. Plan()
		// applied the initial channel's mapping to plan.ResolvedModel; on a
		// retry a different channel is selected, so we re-derive the upstream
		// model against the retried channel's mapping.
		currentResolvedModel := relaybiz.ResolveChannelModel(ch, plan.BaseModel()) // recompute from globally-resolved model, not the already-mapped plan.ResolvedModel
		req.Model = currentResolvedModel
		// P3 #6: derive the billing model name from billing_model_source.
		billingModel := s.BillingModelName(clientModel, plan.ResolvedModel, currentResolvedModel)
		reservation, reserveErr := s.reserveQuota(ctx, fmt.Sprintf("%d", plan.Auth.UserID), requestID, estimatedTokens, billingModel, fmt.Sprintf("%d", ch.ID), ch.SubscriptionAccountID, plan.Auth.RoutingContext)
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
			streamLogInput := usageLogInput{
				UserID:                plan.Auth.UserID,
				TokenID:               plan.Auth.TokenID,
				TokenName:             plan.Auth.TokenName,
				RequestID:             requestID,
				Endpoint:              "/v1/chat/completions",
				ModelName:             s.BillingModelName(clientModel, plan.ResolvedModel, currentResolvedModel),
				ChannelID:             ch.ID,
				SubscriptionAccountID: ch.SubscriptionAccountID,
				IsStream:              true,
			}
			// v0.11.0 review M1: record the source that actually executed the
			// request, not the original plan, so failover attribution is correct.
			streamLogInput.applyChannelInputs(ch)
			streamLogInput.applyReservation(reservation)
			return s.handleStreamingResponse(w, r.WithContext(ctx), provider, &req, reservation, streamLogInput)
		}

		// Non-streaming call
		resp, callErr := provider.ChatCompletions(ctx, &req)
		if callErr != nil {
			_ = s.releaseQuota(ctx, reservation.ReservationId, "upstream error")
			return callErr
		}

		// Success — commit quota and return
		actualTokens := s.calculateActualTokens(resp)
		cacheCreation5mTokens, cacheCreation1hTokens := cacheCreationTokensFromProviderUsage(resp.Usage)
		logInput := usageLogInput{
			UserID:                plan.Auth.UserID,
			TokenID:               plan.Auth.TokenID,
			TokenName:             plan.Auth.TokenName,
			RequestID:             requestID,
			Endpoint:              "/v1/chat/completions",
			ModelName:             s.BillingModelName(clientModel, plan.ResolvedModel, currentResolvedModel),
			Quota:                 actualTokens,
			PromptTokens:          int64(resp.Usage.PromptTokens),
			CompletionTokens:      int64(resp.Usage.CompletionTokens),
			CacheReadTokens:       cacheReadTokensFromProviderUsage(resp.Usage),
			CacheCreation5mTokens: cacheCreation5mTokens,
			CacheCreation1hTokens: cacheCreation1hTokens,
			ChannelID:             ch.ID,
			ElapsedTime:           time.Since(startedAt).Milliseconds(),
			IsStream:              false,
		}
		// v0.11.0 review M1: record the source that actually executed the
		// request, not the original plan, so failover attribution is correct.
		logInput.applyChannelInputs(ch)
		logInput.applyReservation(reservation)
		// §4.3: the envelope comes from the provider-proven canonical buckets
		// (Anthropic) or the OpenAI field shape — never the channel type.
		logInput.applyEnvelope(envelopeFromProviderUsage(resp.Usage, resp.Canonical))
		if err := s.commitQuota(ctx, reservation.ReservationId, actualTokens, true, logInput); err != nil {
			return relaybiz.MarkPostForwardError(err)
		}
		logUpstreamUsage(logInput)
		s.ingestUsageLog(ctx, logInput)
		s.writeJSON(w, http.StatusOK, resp)
		return nil
	})

	// Finalize the routing selection observation with the execution outcome
	// (success/error + fallback info) so routing_selection_total and
	// routing_fallback_total fire once (code review #1/#2).
	s.finalizeSelectionFromResult(plan, result, time.Since(retryStartedAt))
	recordRelayRetryOutcome(r.Context(), result.Fallback, result.Err, result.FallbackReason)

	if result.Err != nil {
		setErrorChannel(w, result.Channel)
		if subscriptionResult != nil && result.Err == subscriptionErr {
			if subscriptionResult.retryable {
				metrics.RelaySubscriptionFailoverTotal.WithLabelValues(subscriptionRetryReason(*subscriptionResult), "exhausted").Inc()
			}
			subscriptionResult.write(w)
			return
		}
		s.writeError(w, mapUpstreamError(relaybiz.UpstreamStatus(result.Err)), "upstream service error")
	}
}

func (s *HTTPServer) handleStreamingResponse(w http.ResponseWriter, r *http.Request, provider relayprovider.Provider, req *relayprovider.ChatCompletionsRequest, reservation *billingv1.ReserveQuotaResponse, logInput usageLogInput) error {
	startedAt := time.Now()
	streamCtx, cancelStream := context.WithCancel(r.Context())
	defer cancelStream()
	chunkChan, err := provider.ChatCompletionsStream(streamCtx, req)
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
	cacheCreation5mTokens := int64(0)
	cacheCreation1hTokens := int64(0)
	estimatedTokens := int64(0)
	streamError := false
	terminal := &streamTerminalTracker{endpoint: EndpointChatCompletions}
	var lastUsage relayprovider.Usage
	var streamCanonical *relayprovider.CanonicalUsage

	for chunk := range chunkChan {
		if chunk.Complete {
			terminal.success = true
			continue
		}
		if chunk.StreamError != nil || r.Context().Err() != nil {
			streamError = true
			break
		}
		if chunk.Usage.TotalTokens > 0 {
			lastUsage = chunk.Usage
			totalTokens = int64(chunk.Usage.TotalTokens)
			promptTokens = int64(chunk.Usage.PromptTokens)
			completionTokens = int64(chunk.Usage.CompletionTokens)
			cacheReadTokens = cacheReadTokensFromProviderUsage(chunk.Usage)
			if fiveM, oneH := cacheCreationTokensFromProviderUsage(chunk.Usage); fiveM > 0 || oneH > 0 {
				cacheCreation5mTokens = fiveM
				cacheCreation1hTokens = oneH
			}
		}
		if chunk.Canonical != nil {
			streamCanonical = chunk.Canonical
		}
		for _, choice := range chunk.Choices {
			estimatedTokens += int64(len(choice.Delta.Content) / 4)
		}

		jsonData, err := jsonx.Marshal(chunk)
		if err != nil {
			applogger.Log.Warn("failed to marshal chunk", zap.Error(err))
			continue
		}

		terminal.observeLine("data: " + string(jsonData))
		if _, err := fmt.Fprintf(w, "data: %s\n\n", string(jsonData)); err != nil {
			streamError = true
			break
		}
		flusher.Flush()
	}

	streamError = streamError || r.Context().Err() != nil || !terminal.Success()
	if !streamError {
		_, err := fmt.Fprintf(w, "data: [DONE]\n\n")
		streamError = err != nil
		flusher.Flush()
	}

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
		logInput.CacheCreation5mTokens = cacheCreation5mTokens
		logInput.CacheCreation1hTokens = cacheCreation1hTokens
		logInput.ElapsedTime = time.Since(startedAt).Milliseconds()
		if logInput.Endpoint == "" {
			logInput.Endpoint = "/v1/chat/completions"
		}
		// §4.3: prefer the provider-proven canonical buckets from the
		// terminal usage chunk; otherwise decide from the OpenAI shape.
		if totalTokens > 0 {
			logInput.applyEnvelope(envelopeFromProviderUsage(lastUsage, streamCanonical))
		}
		if err := s.commitQuotaAfterResponseObserved(r.Context(), reservation.ReservationId, totalTokens, true, logInput); err != nil {
			s.logPostResponseCommitError(err)
		} else {
			logUpstreamUsage(logInput)
			s.ingestUsageLogAfterResponse(logInput)
		}
	} else {
		setRelayObservationResult(r.Context(), "stream_error")
		_ = s.releaseQuota(r.Context(), reservation.ReservationId, "stream error")
	}
	return nil
}

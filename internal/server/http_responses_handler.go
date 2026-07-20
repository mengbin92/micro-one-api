package server

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	relayprovider "micro-one-api/domain/upstream/provider"
	relaybiz "micro-one-api/internal/biz"
)

func (s *HTTPServer) handleResponsesRelay(w http.ResponseWriter, r *http.Request) {
	// Codex Responses WebSocket: when the client sends an Upgrade: websocket
	// request against /v1/responses, hand off to the WS forwarder instead of
	// the HTTP/SSE path. This is the ingress point for the new Responses WS
	// protocol used by the Codex CLI.
	if isOpenAIWSUpgradeRequest(r) {
		s.handleResponsesWebSocket(r.Context(), w, r)
		return
	}

	upstreamPath := r.URL.Path
	if strings.HasPrefix(upstreamPath, "/v1/") {
		upstreamPath = strings.TrimPrefix(upstreamPath, "/v1")
	}

	if r.URL.Path == "/v1/responses" || r.URL.Path == "/v1/responses/input_tokens" || r.URL.Path == "/v1/responses/compact" {
		if r.Method != http.MethodPost {
			s.writeError(w, http.StatusMethodNotAllowed, "method not allowed")
			return
		}
		s.handleResponsesCreateLike(w, r, upstreamPath)
		return
	}

	responseID, ok := parseResponsesResourcePath(r.Method, r.URL.Path)
	if !ok {
		s.writeError(w, http.StatusNotFound, "response not found")
		return
	}
	s.handleResponsesResource(w, r, upstreamPath, responseID)
}

func (s *HTTPServer) handleResponsesCreateLike(w http.ResponseWriter, r *http.Request, upstreamPath string) {
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

	body, err := io.ReadAll(io.LimitReader(r.Body, 64*1024*1024))
	if err != nil {
		s.writeError(w, http.StatusBadRequest, "failed to read request body")
		return
	}

	clientModel := extractRawModel(body)
	previousResponseID := extractPreviousResponseID(body)
	sessionHash := extractSessionHashFromRequest(r, body)
	if clientModel == "" {
		if previousRoute, ok := s.lookupResponseRouteWithSticky(r.Context(), token, previousResponseID); ok {
			s.forwardResponsesToStoredRoute(w, r, upstreamPath, body, token, previousRoute, isRawStreamRequest(body))
			return
		}
		s.writeError(w, http.StatusBadRequest, "model is required")
		return
	}

	var plan *relaybiz.RelayPlan
	if s.wsScheduler != nil {
		plan, err = s.wsScheduler.ResolvePlan(r.Context(), token, clientModel, previousResponseID, sessionHash)
	} else {
		plan, err = s.relayUsecase.Plan(r.Context(), relaybiz.RelayRequest{
			Token: token,
			Model: clientModel,
		})
	}
	if err != nil {
		s.handleRelayPlanError(w, err)
		return
	}
	if err := s.checkUserRPM(r.Context(), plan.Auth.UserID); err != nil {
		s.writeUserRPMError(w)
		return
	}
	if s.hybridAdaptorEnabled && plan.Channel != nil && isSubscriptionChannel(plan.Channel.Type) {
		s.handleResponsesCreateLikeViaAdaptor(w, r, plan, clientModel, body)
		return
	}
	upstreamBody := rewriteRawModel(body, plan.ResolvedModel)

	var upstreamResp *relayprovider.RawResponse
	var responseChannel *relaybiz.Channel
	retryExecutor := s.relayUsecase.NewRetryExecutor()
	result := retryExecutor.ExecuteWithInitialChannel(r.Context(), plan.Auth.Group, plan.ResolvedModel, plan.Channel, func(ctx context.Context, ch *relaybiz.Channel) error {
		startedAt := time.Now()
		requestID := generateRequestID()
		reservation, reserveErr := s.reserveQuota(
			ctx,
			fmt.Sprintf("%d", plan.Auth.UserID),
			requestID,
			estimateRawTokens(upstreamBody),
			plan.ResolvedModel,
			fmt.Sprintf("%d", ch.ID),
			subscriptionAccountIDFromPlan(plan),
		)
		if reserveErr != nil {
			return &relaybiz.RetryableError{Status: http.StatusPaymentRequired, Err: reserveErr}
		}

		if isRawStreamRequest(body) {
			// Anthropic API-key channels speak /v1/messages, not Responses.
			// Convert the inbound Responses request to Anthropic Messages and
			// bridge the upstream SSE back to Responses SSE.
			if isAnthropicAPIKeyChannel(ch) {
				fallbackResp, fallbackErr := s.forwardResponsesViaAnthropicFallback(ctx, ch, r.Header.Clone(), upstreamBody)
				if fallbackErr != nil {
					_ = s.releaseQuota(ctx, reservation.ReservationId, "upstream stream error")
					return fallbackErr
				}
				if fallbackResp.Stream != nil {
					usage := newRawStreamUsageTracker(estimateRawUsage(upstreamBody))
					writeRawStreamResponse(w, fallbackResp.Stream, usage)
					actualUsage := usage.Usage()
					logInput := usageLogInput{
						UserID:           plan.Auth.UserID,
						TokenID:          plan.Auth.TokenID,
						TokenName:        plan.Auth.TokenName,
						RequestID:        requestID,
						Endpoint:         r.URL.Path,
						ModelName:        clientModel,
						Quota:            actualUsage.TotalTokens,
						PromptTokens:     actualUsage.PromptTokens,
						CompletionTokens: actualUsage.CompletionTokens,
						CacheReadTokens:  actualUsage.CacheReadTokens,
						ChannelID:        ch.ID,
						ElapsedTime:      time.Since(startedAt).Milliseconds(),
						IsStream:         true,
					}
					if err := s.commitQuotaAfterResponse(reservation.ReservationId, actualUsage.TotalTokens, true, logInput); err != nil {
						s.logPostResponseCommitError(err)
					} else {
						logUpstreamUsage(logInput)
						s.ingestUsageLogAfterResponse(logInput)
					}
					upstreamResp = &relayprovider.RawResponse{StatusCode: fallbackResp.Stream.StatusCode}
					responseChannel = ch
					if responseID := usage.ResponseID(); responseID != "" {
						s.storeResponseRoute(responseID, responseRoute{Model: clientModel, ResolvedModel: plan.ResolvedModel, Channel: *ch, UserID: plan.Auth.UserID, SubscriptionAccountID: subscriptionAccountIDFromPlan(plan)})
					}
					return nil
				}
			}
			streamResp, streamErr := s.forwardResponsesRawStream(ctx, ch, r.Method, upstreamPath, r.URL.RawQuery, r.Header.Clone(), upstreamBody)
			if streamErr != nil {
				if shouldFallbackResponsesToChat(upstreamPath, streamErr) {
					fallbackResp, fallbackErr := s.forwardResponsesViaChatFallback(ctx, ch, r.Header.Clone(), upstreamBody)
					if fallbackErr == nil && fallbackResp.Stream != nil {
						usage := newRawStreamUsageTracker(estimateRawUsage(upstreamBody))
						writeRawStreamResponse(w, fallbackResp.Stream, usage)
						actualUsage := usage.Usage()
						logInput := usageLogInput{
							UserID:           plan.Auth.UserID,
							TokenID:          plan.Auth.TokenID,
							TokenName:        plan.Auth.TokenName,
							RequestID:        requestID,
							Endpoint:         r.URL.Path,
							ModelName:        clientModel,
							Quota:            actualUsage.TotalTokens,
							PromptTokens:     actualUsage.PromptTokens,
							CompletionTokens: actualUsage.CompletionTokens,
							CacheReadTokens:  actualUsage.CacheReadTokens,
							ChannelID:        ch.ID,
							ElapsedTime:      time.Since(startedAt).Milliseconds(),
							IsStream:         true,
						}
						if err := s.commitQuotaAfterResponse(reservation.ReservationId, actualUsage.TotalTokens, true, logInput); err != nil {
							s.logPostResponseCommitError(err)
						} else {
							logUpstreamUsage(logInput)
							s.ingestUsageLogAfterResponse(logInput)
						}
						upstreamResp = &relayprovider.RawResponse{StatusCode: fallbackResp.Stream.StatusCode}
						responseChannel = ch
						if responseID := usage.ResponseID(); responseID != "" {
							s.storeResponseRoute(responseID, responseRoute{Model: clientModel, ResolvedModel: plan.ResolvedModel, Channel: *ch, UserID: plan.Auth.UserID, SubscriptionAccountID: subscriptionAccountIDFromPlan(plan)})
						}
						return nil
					}
				}
				_ = s.releaseQuota(ctx, reservation.ReservationId, "upstream stream error")
				return streamErr
			}
			usage := newRawStreamUsageTracker(estimateRawUsage(upstreamBody))
			writeRawStreamResponse(w, streamResp, usage)
			actualUsage := usage.Usage()
			logInput := usageLogInput{
				UserID:           plan.Auth.UserID,
				TokenID:          plan.Auth.TokenID,
				TokenName:        plan.Auth.TokenName,
				RequestID:        requestID,
				Endpoint:         r.URL.Path,
				ModelName:        clientModel,
				Quota:            actualUsage.TotalTokens,
				PromptTokens:     actualUsage.PromptTokens,
				CompletionTokens: actualUsage.CompletionTokens,
				CacheReadTokens:  actualUsage.CacheReadTokens,
				ChannelID:        ch.ID,
				ElapsedTime:      time.Since(startedAt).Milliseconds(),
				IsStream:         true,
			}
			if err := s.commitQuotaAfterResponse(reservation.ReservationId, actualUsage.TotalTokens, true, logInput); err != nil {
				s.logPostResponseCommitError(err)
			} else {
				logUpstreamUsage(logInput)
				s.ingestUsageLogAfterResponse(logInput)
			}
			upstreamResp = &relayprovider.RawResponse{StatusCode: streamResp.StatusCode}
			responseChannel = ch
			if responseID := usage.ResponseID(); responseID != "" {
				s.storeResponseRoute(responseID, responseRoute{Model: clientModel, ResolvedModel: plan.ResolvedModel, Channel: *ch, UserID: plan.Auth.UserID, SubscriptionAccountID: subscriptionAccountIDFromPlan(plan)})
			}
			return nil
		}

		// Anthropic API-key channels speak /v1/messages, not Responses.
		if isAnthropicAPIKeyChannel(ch) {
			fallbackResp, fallbackErr := s.forwardResponsesViaAnthropicFallback(ctx, ch, r.Header.Clone(), upstreamBody)
			if fallbackErr == nil && fallbackResp.Response != nil {
				usage := fallbackResp.Usage
				if usage.TotalTokens <= 0 {
					usage = extractRawUsage(fallbackResp.Response.Body, estimateRawTokens(upstreamBody))
				}
				logInput := usageLogInput{
					UserID:           plan.Auth.UserID,
					TokenID:          plan.Auth.TokenID,
					TokenName:        plan.Auth.TokenName,
					RequestID:        requestID,
					Endpoint:         r.URL.Path,
					ModelName:        clientModel,
					Quota:            usage.TotalTokens,
					PromptTokens:     usage.PromptTokens,
					CompletionTokens: usage.CompletionTokens,
					CacheReadTokens:  usage.CacheReadTokens,
					ChannelID:        ch.ID,
					ElapsedTime:      time.Since(startedAt).Milliseconds(),
					IsStream:         false,
				}
				if err := s.commitQuota(ctx, reservation.ReservationId, usage.TotalTokens, true, logInput); err != nil {
					return err
				}
				logUpstreamUsage(logInput)
				s.ingestUsageLog(ctx, logInput)
				upstreamResp = fallbackResp.Response
				responseChannel = ch
				if responseID := extractResponseID(fallbackResp.Response.Body); responseID != "" {
					s.storeResponseRoute(responseID, responseRoute{Model: clientModel, ResolvedModel: plan.ResolvedModel, Channel: *ch, UserID: plan.Auth.UserID, SubscriptionAccountID: subscriptionAccountIDFromPlan(plan)})
				}
				return nil
			}
			// Conversion/forwarding failed: surface as upstream error so the
			// retry executor can fail over.
			_ = s.releaseQuota(ctx, reservation.ReservationId, "upstream anthropic error")
			return fmt.Errorf("anthropic upstream: %w", fallbackErr)
		}

		resp, forwardErr := s.forwardResponsesRaw(ctx, ch, r.Method, upstreamPath, r.URL.RawQuery, r.Header.Clone(), upstreamBody)
		if forwardErr != nil {
			if shouldFallbackResponsesToChat(upstreamPath, forwardErr) {
				fallbackResp, fallbackErr := s.forwardResponsesViaChatFallback(ctx, ch, r.Header.Clone(), upstreamBody)
				if fallbackErr == nil && fallbackResp.Response != nil {
					usage := fallbackResp.Usage
					if usage.TotalTokens <= 0 {
						usage = extractRawUsage(fallbackResp.Response.Body, estimateRawTokens(upstreamBody))
					}
					logInput := usageLogInput{
						UserID:           plan.Auth.UserID,
						TokenID:          plan.Auth.TokenID,
						TokenName:        plan.Auth.TokenName,
						RequestID:        requestID,
						Endpoint:         r.URL.Path,
						ModelName:        clientModel,
						Quota:            usage.TotalTokens,
						PromptTokens:     usage.PromptTokens,
						CompletionTokens: usage.CompletionTokens,
						CacheReadTokens:  usage.CacheReadTokens,
						ChannelID:        ch.ID,
						ElapsedTime:      time.Since(startedAt).Milliseconds(),
						IsStream:         false,
					}
					if err := s.commitQuota(ctx, reservation.ReservationId, usage.TotalTokens, true, logInput); err != nil {
						return err
					}
					logUpstreamUsage(logInput)
					s.ingestUsageLog(ctx, logInput)
					upstreamResp = fallbackResp.Response
					responseChannel = ch
					return nil
				}
			}
			_ = s.releaseQuota(ctx, reservation.ReservationId, "upstream error")
			return forwardErr
		}

		usage := extractRawUsage(resp.Body, estimateRawTokens(upstreamBody))
		logInput := usageLogInput{
			UserID:           plan.Auth.UserID,
			TokenID:          plan.Auth.TokenID,
			TokenName:        plan.Auth.TokenName,
			RequestID:        requestID,
			Endpoint:         r.URL.Path,
			ModelName:        clientModel,
			Quota:            usage.TotalTokens,
			PromptTokens:     usage.PromptTokens,
			CompletionTokens: usage.CompletionTokens,
			CacheReadTokens:  usage.CacheReadTokens,
			ChannelID:        ch.ID,
			ElapsedTime:      time.Since(startedAt).Milliseconds(),
			IsStream:         false,
		}
		if err := s.commitQuota(ctx, reservation.ReservationId, usage.TotalTokens, true, logInput); err != nil {
			return err
		}
		logUpstreamUsage(logInput)
		s.ingestUsageLog(ctx, logInput)
		upstreamResp = resp
		responseChannel = ch
		return nil
	})

	if result.Err != nil {
		s.writeError(w, mapUpstreamError(relaybiz.UpstreamStatus(result.Err)), "upstream service error")
		return
	}
	if upstreamResp == nil || responseChannel == nil {
		s.writeError(w, http.StatusBadGateway, "upstream service error")
		return
	}

	if s.wsScheduler != nil {
		s.wsScheduler.BindSession(r.Context(), &relaybiz.RelayPlan{
			Auth:          plan.Auth,
			Channel:       responseChannel,
			ResolvedModel: plan.ResolvedModel,
			Account:       plan.Account,
		}, sessionHash)
	}
	if upstreamResp.Body == nil {
		return
	}
	if responseID := extractResponseID(upstreamResp.Body); responseID != "" {
		s.storeResponseRoute(responseID, responseRoute{Model: clientModel, ResolvedModel: plan.ResolvedModel, Channel: *responseChannel, UserID: plan.Auth.UserID, SubscriptionAccountID: subscriptionAccountIDFromPlan(plan)})
	}
	writeRawResponse(w, upstreamResp)
}

func (s *HTTPServer) handleResponsesResource(w http.ResponseWriter, r *http.Request, upstreamPath, responseID string) {
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

	route, ok := s.lookupResponseRoute(responseID)
	if !ok {
		s.writeJSON(w, http.StatusNotFound, map[string]interface{}{
			"error": map[string]interface{}{
				"message": "response route not found",
				"type":    "invalid_request_error",
				"param":   "response_id",
				"code":    "response_not_found",
			},
		})
		return
	}

	body, err := io.ReadAll(io.LimitReader(r.Body, 64*1024*1024))
	if err != nil {
		s.writeError(w, http.StatusBadRequest, "failed to read request body")
		return
	}
	s.forwardResponsesToStoredRoute(w, r, upstreamPath, body, token, route, false)
}

func (s *HTTPServer) forwardResponsesToStoredRoute(w http.ResponseWriter, r *http.Request, upstreamPath string, body []byte, token string, route responseRoute, stream bool) {
	authSnapshot, err := s.getAuthSnapshot(r.Context(), token)
	if err != nil {
		s.handleIdentityError(w, err)
		return
	}
	if route.UserID != 0 && route.UserID != authSnapshot.UserId {
		s.writeJSON(w, http.StatusNotFound, map[string]interface{}{
			"error": map[string]interface{}{
				"message": "response route not found",
				"type":    "invalid_request_error",
				"param":   "response_id",
				"code":    "response_not_found",
			},
		})
		return
	}
	if err := s.checkUserRPM(r.Context(), authSnapshot.UserId); err != nil {
		s.writeUserRPMError(w)
		return
	}

	requestID := generateRequestID()
	resolvedModel := routeResolvedModel(route)
	fallbackBody := ensureRawModel(body, resolvedModel)
	reservation, err := s.reserveQuota(
		r.Context(),
		fmt.Sprintf("%d", authSnapshot.UserId),
		requestID,
		estimateRawTokens(body),
		route.Model,
		fmt.Sprintf("%d", route.Channel.ID),
		route.SubscriptionAccountID,
	)
	if err != nil {
		s.writeError(w, http.StatusPaymentRequired, "quota reservation failed")
		return
	}

	startedAt := time.Now()
	if stream {
		// Anthropic API-key channels speak /v1/messages, not Responses.
		if isAnthropicAPIKeyChannel(&route.Channel) {
			fallbackResp, fallbackErr := s.forwardResponsesViaAnthropicFallback(r.Context(), &route.Channel, r.Header.Clone(), fallbackBody)
			if fallbackErr == nil && fallbackResp.Stream != nil {
				usage := newRawStreamUsageTracker(estimateRawUsage(fallbackBody))
				writeRawStreamResponse(w, fallbackResp.Stream, usage)
				actualUsage := usage.Usage()
				logInput := usageLogInput{
					UserID:                authSnapshot.UserId,
					TokenID:               authSnapshot.TokenId,
					TokenName:             authSnapshot.TokenName,
					RequestID:             requestID,
					Endpoint:              r.URL.Path,
					ModelName:             route.Model,
					Quota:                 actualUsage.TotalTokens,
					PromptTokens:          actualUsage.PromptTokens,
					CompletionTokens:      actualUsage.CompletionTokens,
					CacheReadTokens:       actualUsage.CacheReadTokens,
					ChannelID:             route.Channel.ID,
					SubscriptionAccountID: route.SubscriptionAccountID,
					ElapsedTime:           time.Since(startedAt).Milliseconds(),
					IsStream:              true,
				}
				if err := s.commitQuotaAfterResponse(reservation.ReservationId, actualUsage.TotalTokens, true, logInput); err != nil {
					s.logPostResponseCommitError(err)
				} else {
					s.ingestUsageLogAfterResponse(logInput)
				}
				if responseID := usage.ResponseID(); responseID != "" {
					route.UserID = authSnapshot.UserId
					route.ResolvedModel = resolvedModel
					s.storeResponseRoute(responseID, route)
				}
				return
			}
			_ = s.releaseQuota(r.Context(), reservation.ReservationId, "upstream stream error")
			s.writeError(w, mapUpstreamError(relaybiz.UpstreamStatus(fallbackErr)), "upstream service error")
			return
		}
		streamResp, err := s.forwardResponsesRawStream(r.Context(), &route.Channel, r.Method, upstreamPath, r.URL.RawQuery, r.Header.Clone(), body)
		if err != nil {
			if shouldFallbackResponsesToChat(upstreamPath, err) {
				fallbackResp, fallbackErr := s.forwardResponsesViaChatFallback(r.Context(), &route.Channel, r.Header.Clone(), fallbackBody)
				if fallbackErr == nil && fallbackResp.Stream != nil {
					usage := newRawStreamUsageTracker(estimateRawUsage(fallbackBody))
					writeRawStreamResponse(w, fallbackResp.Stream, usage)
					actualUsage := usage.Usage()
					logInput := usageLogInput{
						UserID:                authSnapshot.UserId,
						TokenID:               authSnapshot.TokenId,
						TokenName:             authSnapshot.TokenName,
						RequestID:             requestID,
						Endpoint:              r.URL.Path,
						ModelName:             route.Model,
						Quota:                 actualUsage.TotalTokens,
						PromptTokens:          actualUsage.PromptTokens,
						CompletionTokens:      actualUsage.CompletionTokens,
						CacheReadTokens:       actualUsage.CacheReadTokens,
						ChannelID:             route.Channel.ID,
						SubscriptionAccountID: route.SubscriptionAccountID,
						ElapsedTime:           time.Since(startedAt).Milliseconds(),
						IsStream:              true,
					}
					if err := s.commitQuotaAfterResponse(reservation.ReservationId, actualUsage.TotalTokens, true, logInput); err != nil {
						s.logPostResponseCommitError(err)
					} else {
						s.ingestUsageLogAfterResponse(logInput)
					}
					if responseID := usage.ResponseID(); responseID != "" {
						route.UserID = authSnapshot.UserId
						route.ResolvedModel = resolvedModel
						s.storeResponseRoute(responseID, route)
					}
					return
				}
			}
			_ = s.releaseQuota(r.Context(), reservation.ReservationId, "upstream stream error")
			s.writeError(w, mapUpstreamError(relaybiz.UpstreamStatus(err)), "upstream service error")
			return
		}
		usage := newRawStreamUsageTracker(estimateRawUsage(body))
		writeRawStreamResponse(w, streamResp, usage)
		actualUsage := usage.Usage()
		logInput := usageLogInput{
			UserID:                authSnapshot.UserId,
			TokenID:               authSnapshot.TokenId,
			TokenName:             authSnapshot.TokenName,
			RequestID:             requestID,
			Endpoint:              r.URL.Path,
			ModelName:             route.Model,
			Quota:                 actualUsage.TotalTokens,
			PromptTokens:          actualUsage.PromptTokens,
			CompletionTokens:      actualUsage.CompletionTokens,
			CacheReadTokens:       actualUsage.CacheReadTokens,
			ChannelID:             route.Channel.ID,
			SubscriptionAccountID: route.SubscriptionAccountID,
			ElapsedTime:           time.Since(startedAt).Milliseconds(),
			IsStream:              true,
		}
		if err := s.commitQuotaAfterResponse(reservation.ReservationId, actualUsage.TotalTokens, true, logInput); err != nil {
			s.logPostResponseCommitError(err)
		} else {
			s.ingestUsageLogAfterResponse(logInput)
		}
		if responseID := usage.ResponseID(); responseID != "" {
			route.UserID = authSnapshot.UserId
			s.storeResponseRoute(responseID, route)
		}
		return
	}

	// Anthropic API-key channels speak /v1/messages, not Responses.
	if isAnthropicAPIKeyChannel(&route.Channel) {
		fallbackResp, fallbackErr := s.forwardResponsesViaAnthropicFallback(r.Context(), &route.Channel, r.Header.Clone(), fallbackBody)
		if fallbackErr == nil && fallbackResp.Response != nil {
			usage := fallbackResp.Usage
			if usage.TotalTokens <= 0 {
				usage = extractRawUsage(fallbackResp.Response.Body, estimateRawTokens(fallbackBody))
			}
			logInput := usageLogInput{
				UserID:                authSnapshot.UserId,
				TokenID:               authSnapshot.TokenId,
				TokenName:             authSnapshot.TokenName,
				RequestID:             requestID,
				Endpoint:              r.URL.Path,
				ModelName:             route.Model,
				Quota:                 usage.TotalTokens,
				PromptTokens:          usage.PromptTokens,
				CompletionTokens:      usage.CompletionTokens,
				CacheReadTokens:       usage.CacheReadTokens,
				ChannelID:             route.Channel.ID,
				SubscriptionAccountID: route.SubscriptionAccountID,
				ElapsedTime:           time.Since(startedAt).Milliseconds(),
				IsStream:              false,
			}
			if err := s.commitQuota(r.Context(), reservation.ReservationId, usage.TotalTokens, true, logInput); err != nil {
				s.writeError(w, http.StatusPaymentRequired, "billing commit failed")
				return
			}
			s.ingestUsageLog(r.Context(), logInput)
			if responseID := extractResponseID(fallbackResp.Response.Body); responseID != "" {
				route.UserID = authSnapshot.UserId
				route.ResolvedModel = resolvedModel
				s.storeResponseRoute(responseID, route)
			}
			writeRawResponse(w, fallbackResp.Response)
			return
		}
		_ = s.releaseQuota(r.Context(), reservation.ReservationId, "upstream error")
		s.writeError(w, mapUpstreamError(relaybiz.UpstreamStatus(fallbackErr)), "upstream service error")
		return
	}

	resp, err := s.forwardResponsesRaw(r.Context(), &route.Channel, r.Method, upstreamPath, r.URL.RawQuery, r.Header.Clone(), body)
	if err != nil {
		if shouldFallbackResponsesToChat(upstreamPath, err) {
			fallbackResp, fallbackErr := s.forwardResponsesViaChatFallback(r.Context(), &route.Channel, r.Header.Clone(), fallbackBody)
			if fallbackErr == nil && fallbackResp.Response != nil {
				usage := fallbackResp.Usage
				if usage.TotalTokens <= 0 {
					usage = extractRawUsage(fallbackResp.Response.Body, estimateRawTokens(fallbackBody))
				}
				logInput := usageLogInput{
					UserID:                authSnapshot.UserId,
					TokenID:               authSnapshot.TokenId,
					TokenName:             authSnapshot.TokenName,
					RequestID:             requestID,
					Endpoint:              r.URL.Path,
					ModelName:             route.Model,
					Quota:                 usage.TotalTokens,
					PromptTokens:          usage.PromptTokens,
					CompletionTokens:      usage.CompletionTokens,
					CacheReadTokens:       usage.CacheReadTokens,
					ChannelID:             route.Channel.ID,
					SubscriptionAccountID: route.SubscriptionAccountID,
					ElapsedTime:           time.Since(startedAt).Milliseconds(),
					IsStream:              false,
				}
				if err := s.commitQuota(r.Context(), reservation.ReservationId, usage.TotalTokens, true, logInput); err != nil {
					s.writeError(w, http.StatusPaymentRequired, "billing commit failed")
					return
				}
				s.ingestUsageLog(r.Context(), logInput)
				if responseID := extractResponseID(fallbackResp.Response.Body); responseID != "" {
					route.UserID = authSnapshot.UserId
					route.ResolvedModel = resolvedModel
					s.storeResponseRoute(responseID, route)
				}
				writeRawResponse(w, fallbackResp.Response)
				return
			}
		}
		_ = s.releaseQuota(r.Context(), reservation.ReservationId, "upstream error")
		s.writeError(w, mapUpstreamError(relaybiz.UpstreamStatus(err)), "upstream service error")
		return
	}

	usage := extractRawUsage(resp.Body, estimateRawTokens(body))
	logInput := usageLogInput{
		UserID:                authSnapshot.UserId,
		TokenID:               authSnapshot.TokenId,
		TokenName:             authSnapshot.TokenName,
		RequestID:             requestID,
		Endpoint:              r.URL.Path,
		ModelName:             route.Model,
		Quota:                 usage.TotalTokens,
		PromptTokens:          usage.PromptTokens,
		CompletionTokens:      usage.CompletionTokens,
		CacheReadTokens:       usage.CacheReadTokens,
		ChannelID:             route.Channel.ID,
		SubscriptionAccountID: route.SubscriptionAccountID,
		ElapsedTime:           time.Since(startedAt).Milliseconds(),
		IsStream:              false,
	}
	if err := s.commitQuota(r.Context(), reservation.ReservationId, usage.TotalTokens, true, logInput); err != nil {
		s.writeError(w, http.StatusPaymentRequired, "billing commit failed")
		return
	}
	s.ingestUsageLog(r.Context(), logInput)
	if responseID := extractResponseID(resp.Body); responseID != "" {
		route.UserID = authSnapshot.UserId
		s.storeResponseRoute(responseID, route)
	}
	writeRawResponse(w, resp)
}

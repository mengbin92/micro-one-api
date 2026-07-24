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

func (s *HTTPServer) handleRawRelay(upstreamPath string, requireModel bool) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
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

		body, err := io.ReadAll(io.LimitReader(r.Body, 64*1024*1024))
		if err != nil {
			s.writeError(w, http.StatusBadRequest, "failed to read request body")
			return
		}

		clientModel := extractRawModel(body)
		if clientModel == "" {
			clientModel = defaultRawModel(upstreamPath)
		}
		if requireModel && clientModel == "" {
			s.writeError(w, http.StatusBadRequest, "model is required")
			return
		}

		plan, err := s.relayUsecase.Plan(r.Context(), relaybiz.RelayRequest{
			Token: token,
			Model: clientModel,
		})
		if err != nil {
			s.handleRelayPlanError(w, err)
			return
		}
		if err := s.checkUserRPM(r.Context(), plan.Auth.UserID); err != nil {
			s.writeUserRPMError(w)
			return
		}
		upstreamBody := rewriteRawModel(body, plan.ResolvedModel)

		var upstreamResp *relayprovider.RawResponse
		retryExecutor := s.relayUsecase.NewRetryExecutor()
		result := retryExecutor.ExecuteWithInitialChannel(r.Context(), plan.Auth.Group, plan.ResolvedModel, plan.Channel, func(ctx context.Context, ch *relaybiz.Channel) error {
			startedAt := time.Now()
			requestID := generateRequestID()
			// P3 #6: derive the billing model name from billing_model_source.
			billingModel := s.BillingModelName(clientModel, plan.ResolvedModel, plan.ResolvedModel)
			reservation, reserveErr := s.reserveQuota(
				ctx,
				fmt.Sprintf("%d", plan.Auth.UserID),
				requestID,
				estimateRawTokens(body),
				billingModel,
				fmt.Sprintf("%d", ch.ID),
				subscriptionAccountIDFromPlan(plan),
			)
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

			resp, forwardErr := provider.Forward(ctx, &relayprovider.RawRequest{
				Method: r.Method,
				Path:   upstreamPath,
				Query:  r.URL.RawQuery,
				Header: r.Header.Clone(),
				Body:   upstreamBody,
			})
			if forwardErr != nil {
				_ = s.releaseQuota(ctx, reservation.ReservationId, "upstream error")
				return forwardErr
			}

			usage := extractRawUsage(resp.Body, estimateRawTokens(body))
			logInput := usageLogInput{
				UserID:           plan.Auth.UserID,
				TokenID:          plan.Auth.TokenID,
				TokenName:        plan.Auth.TokenName,
				RequestID:        requestID,
				Endpoint:         upstreamPath,
				ModelName:        s.BillingModelName(clientModel, plan.ResolvedModel, plan.ResolvedModel),
				Quota:            usage.TotalTokens,
				PromptTokens:     usage.PromptTokens,
				CompletionTokens: usage.CompletionTokens,
				CacheReadTokens:  usage.CacheReadTokens,
				ChannelID:        ch.ID,
				ElapsedTime:      time.Since(startedAt).Milliseconds(),
				IsStream:         false,
			}
			logUpstreamUsage(logInput)
			if err := s.commitQuota(ctx, reservation.ReservationId, usage.TotalTokens, true, logInput); err != nil {
				return err
			}
			s.ingestUsageLog(ctx, logInput)
			upstreamResp = resp
			return nil
		})

		if result.Err != nil {
			s.writeError(w, mapUpstreamError(relaybiz.UpstreamStatus(result.Err)), "upstream service error")
			return
		}
		if upstreamResp == nil {
			s.writeError(w, http.StatusBadGateway, "upstream service error")
			return
		}

		writeRawResponse(w, upstreamResp)
	}
}

package server

import (
	"context"
	crypto_rand "crypto/rand"
	"encoding/json"
	stderrors "errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/bytedance/sonic"
	"go.uber.org/zap"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	billingv1 "micro-one-api/api/billing/v1"
	channelv1 "micro-one-api/api/channel/v1"
	commonv1 "micro-one-api/api/common/v1"
	identityv1 "micro-one-api/api/identity/v1"
	logv1 "micro-one-api/api/log/v1"
	"micro-one-api/internal/pkg/errors"
	applogger "micro-one-api/internal/pkg/logger"
	"micro-one-api/internal/pkg/metrics"
	relaybiz "micro-one-api/internal/relay/biz"
	relayprovider "micro-one-api/internal/relay/provider"

	khttp "github.com/go-kratos/kratos/v2/transport/http"
)

const postResponseWriteTimeout = 10 * time.Second
const defaultQuotaPerUSD = 500000

// HTTPServer handles HTTP requests for relay-gateway.
type HTTPServer struct {
	identityClient  identityv1.IdentityServiceClient
	channelClient   channelv1.ChannelServiceClient
	billingClient   billingv1.BillingServiceClient
	logClient       logv1.LogServiceClient
	providerFactory *relayprovider.ProviderFactory
	relayUsecase    *relaybiz.RelayUsecase
	responsesMu     sync.RWMutex
	responseRoutes  map[string]responseRoute
}

type responseRoute struct {
	Model   string
	Channel relaybiz.Channel
	UserID  int64
}

// NewHTTPServer creates a new HTTP server for Kratos.
func NewHTTPServer(
	identityClient identityv1.IdentityServiceClient,
	channelClient channelv1.ChannelServiceClient,
	billingClient billingv1.BillingServiceClient,
	providerFactory *relayprovider.ProviderFactory,
	relayUsecase *relaybiz.RelayUsecase,
	logClients ...logv1.LogServiceClient,
) *HTTPServer {
	var logClient logv1.LogServiceClient
	if len(logClients) > 0 {
		logClient = logClients[0]
	}
	return &HTTPServer{
		identityClient:  identityClient,
		channelClient:   channelClient,
		billingClient:   billingClient,
		logClient:       logClient,
		providerFactory: providerFactory,
		relayUsecase:    relayUsecase,
		responseRoutes:  make(map[string]responseRoute),
	}
}

// RegisterRoutes registers HTTP routes to a Kratos *khttp.Server.
func (s *HTTPServer) RegisterRoutes(srv *khttp.Server) {
	srv.HandleFunc("/v1/chat/completions", s.handleChatCompletions)
	srv.HandleFunc("/v1/completions", s.handleRawRelay("/completions", true))
	srv.HandleFunc("/v1/embeddings", s.handleRawRelay("/embeddings", false))
	srv.HandleFunc("/v1/images/generations", s.handleRawRelay("/images/generations", true))
	srv.HandleFunc("/v1/images/edits", s.handleUnsupportedOpenAIRoute("images.edits"))
	srv.HandleFunc("/v1/images/variations", s.handleUnsupportedOpenAIRoute("images.variations"))
	srv.HandleFunc("/v1/audio/transcriptions", s.handleRawRelay("/audio/transcriptions", true))
	srv.HandleFunc("/v1/audio/translations", s.handleRawRelay("/audio/translations", true))
	srv.HandleFunc("/v1/audio/speech", s.handleRawRelay("/audio/speech", false))
	srv.HandleFunc("/v1/moderations", s.handleRawRelay("/moderations", false))
	srv.HandleFunc("/v1/edits", s.handleUnsupportedOpenAIRoute("edits"))
	srv.HandleFunc("/v1/responses", s.handleResponsesRelay)
	srv.HandlePrefix("/v1/responses/", http.HandlerFunc(s.handleResponsesRelay))
	srv.HandleFunc("/v1/usage", s.handleUsage)
	srv.HandleFunc("/v1/engines", s.handleUnsupportedOpenAIRoute("engines"))
	srv.HandlePrefix("/v1/engines/", http.HandlerFunc(s.handleUnsupportedOpenAIRoute("engines")))
	srv.HandleFunc("/v1/files", s.handleUnsupportedOpenAIRoute("files"))
	srv.HandlePrefix("/v1/files/", http.HandlerFunc(s.handleUnsupportedOpenAIRoute("files")))
	srv.HandleFunc("/v1/fine-tunes", s.handleUnsupportedOpenAIRoute("fine-tunes"))
	srv.HandlePrefix("/v1/fine-tunes/", http.HandlerFunc(s.handleUnsupportedOpenAIRoute("fine-tunes")))
	srv.HandleFunc("/v1/fine_tuning/jobs", s.handleUnsupportedOpenAIRoute("fine_tuning.jobs"))
	srv.HandlePrefix("/v1/fine_tuning/jobs/", http.HandlerFunc(s.handleUnsupportedOpenAIRoute("fine_tuning.jobs")))
	srv.HandleFunc("/v1/batches", s.handleUnsupportedOpenAIRoute("batches"))
	srv.HandlePrefix("/v1/batches/", http.HandlerFunc(s.handleUnsupportedOpenAIRoute("batches")))
	srv.HandleFunc("/v1/uploads", s.handleUnsupportedOpenAIRoute("uploads"))
	srv.HandlePrefix("/v1/uploads/", http.HandlerFunc(s.handleUnsupportedOpenAIRoute("uploads")))
	srv.HandleFunc("/v1/vector_stores", s.handleUnsupportedOpenAIRoute("vector_stores"))
	srv.HandlePrefix("/v1/vector_stores/", http.HandlerFunc(s.handleUnsupportedOpenAIRoute("vector_stores")))
	srv.HandleFunc("/v1/evals", s.handleUnsupportedOpenAIRoute("evals"))
	srv.HandlePrefix("/v1/evals/", http.HandlerFunc(s.handleUnsupportedOpenAIRoute("evals")))
	srv.HandleFunc("/v1/containers", s.handleUnsupportedOpenAIRoute("containers"))
	srv.HandlePrefix("/v1/containers/", http.HandlerFunc(s.handleUnsupportedOpenAIRoute("containers")))
	srv.HandlePrefix("/v1/fine_tuning/alpha/graders/", http.HandlerFunc(s.handleUnsupportedOpenAIRoute("graders")))
	srv.HandlePrefix("/v1/realtime/", http.HandlerFunc(s.handleUnsupportedOpenAIRoute("realtime")))
	srv.HandleFunc("/v1/conversations", s.handleUnsupportedOpenAIRoute("conversations"))
	srv.HandlePrefix("/v1/conversations/", http.HandlerFunc(s.handleUnsupportedOpenAIRoute("conversations")))
	srv.HandleFunc("/v1/assistants", s.handleUnsupportedOpenAIRoute("assistants"))
	srv.HandlePrefix("/v1/assistants/", http.HandlerFunc(s.handleUnsupportedOpenAIRoute("assistants")))
	srv.HandleFunc("/v1/threads", s.handleUnsupportedOpenAIRoute("threads"))
	srv.HandlePrefix("/v1/threads/", http.HandlerFunc(s.handleUnsupportedOpenAIRoute("threads")))
	srv.HandlePrefix("/v1/oneapi/proxy/", http.HandlerFunc(s.handleOneAPIProxy))
	srv.HandleFunc("/v1/models", s.handleModels)
	srv.HandlePrefix("/v1/models/", http.HandlerFunc(s.handleRetrieveModel))
	srv.HandleFunc("/api/status", s.handleAPIStatus)
	srv.HandleFunc("/api/models", s.handleDashboardModels)
	srv.HandleFunc("/api/group", s.handleGroups)
	srv.HandleFunc("/healthz", s.handleHealth)
	srv.HandleFunc("/metrics", func(w http.ResponseWriter, r *http.Request) {
		metrics.Handler().ServeHTTP(w, r)
	})
}

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

		var upstreamResp *relayprovider.RawResponse
		retryExecutor := s.relayUsecase.NewRetryExecutor()
		result := retryExecutor.Execute(r.Context(), plan.Auth.Group, clientModel, func(ctx context.Context, ch *relaybiz.Channel) error {
			startedAt := time.Now()
			requestID := generateRequestID()
			reservation, reserveErr := s.reserveQuota(
				ctx,
				fmt.Sprintf("%d", plan.Auth.UserID),
				requestID,
				estimateRawTokens(body),
				plan.ResolvedModel,
				fmt.Sprintf("%d", ch.ID),
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
				Body:   body,
			})
			if forwardErr != nil {
				_ = s.releaseQuota(ctx, reservation.ReservationId, "upstream error")
				return forwardErr
			}

			actualTokens := extractTotalTokens(resp.Body, estimateRawTokens(body))
			logInput := usageLogInput{
				UserID:      plan.Auth.UserID,
				TokenID:     plan.Auth.TokenID,
				TokenName:   plan.Auth.TokenName,
				RequestID:   requestID,
				Endpoint:    upstreamPath,
				ModelName:   clientModel,
				Quota:       actualTokens,
				ChannelID:   ch.ID,
				ElapsedTime: time.Since(startedAt).Milliseconds(),
				IsStream:    false,
			}
			if err := s.commitQuota(ctx, reservation.ReservationId, actualTokens, true, logInput); err != nil {
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

func (s *HTTPServer) handleResponsesRelay(w http.ResponseWriter, r *http.Request) {
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
	if clientModel == "" {
		if previousRoute, ok := s.lookupResponseRoute(extractPreviousResponseID(body)); ok {
			s.forwardResponsesToStoredRoute(w, r, upstreamPath, body, token, previousRoute, isRawStreamRequest(body))
			return
		}
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

	var upstreamResp *relayprovider.RawResponse
	var responseChannel *relaybiz.Channel
	retryExecutor := s.relayUsecase.NewRetryExecutor()
	result := retryExecutor.Execute(r.Context(), plan.Auth.Group, clientModel, func(ctx context.Context, ch *relaybiz.Channel) error {
		startedAt := time.Now()
		requestID := generateRequestID()
		reservation, reserveErr := s.reserveQuota(
			ctx,
			fmt.Sprintf("%d", plan.Auth.UserID),
			requestID,
			estimateRawTokens(body),
			plan.ResolvedModel,
			fmt.Sprintf("%d", ch.ID),
		)
		if reserveErr != nil {
			return &relaybiz.RetryableError{Status: http.StatusPaymentRequired, Err: reserveErr}
		}

		if isRawStreamRequest(body) {
			streamResp, streamErr := s.forwardResponsesRawStream(ctx, ch, r.Method, upstreamPath, r.URL.RawQuery, r.Header.Clone(), body)
			if streamErr != nil {
				if shouldFallbackResponsesToChat(upstreamPath, streamErr) {
					fallbackResp, fallbackErr := s.forwardResponsesViaChatFallback(ctx, ch, r.Header.Clone(), body)
					if fallbackErr == nil && fallbackResp.Stream != nil {
						usage := newRawStreamUsageTracker(estimateRawUsage(body))
						writeRawStreamResponse(w, fallbackResp.Stream, usage)
						actualUsage := usage.Usage()
						logInput := usageLogInput{
							UserID:           plan.Auth.UserID,
							TokenID:          plan.Auth.TokenID,
							TokenName:        plan.Auth.TokenName,
							RequestID:        requestID,
							Endpoint:         "/chat/completions",
							ModelName:        clientModel,
							Quota:            actualUsage.TotalTokens,
							PromptTokens:     actualUsage.PromptTokens,
							CompletionTokens: actualUsage.CompletionTokens,
							ChannelID:        ch.ID,
							ElapsedTime:      time.Since(startedAt).Milliseconds(),
							IsStream:         true,
						}
						if err := s.commitQuotaAfterResponse(reservation.ReservationId, actualUsage.TotalTokens, true, logInput); err != nil {
							s.logPostResponseCommitError(err)
						} else {
							s.ingestUsageLogAfterResponse(logInput)
						}
						upstreamResp = &relayprovider.RawResponse{StatusCode: fallbackResp.Stream.StatusCode}
						responseChannel = ch
						return nil
					}
				}
				_ = s.releaseQuota(ctx, reservation.ReservationId, "upstream stream error")
				return streamErr
			}
			usage := newRawStreamUsageTracker(estimateRawUsage(body))
			writeRawStreamResponse(w, streamResp, usage)
			actualUsage := usage.Usage()
			logInput := usageLogInput{
				UserID:           plan.Auth.UserID,
				TokenID:          plan.Auth.TokenID,
				TokenName:        plan.Auth.TokenName,
				RequestID:        requestID,
				Endpoint:         upstreamPath,
				ModelName:        clientModel,
				Quota:            actualUsage.TotalTokens,
				PromptTokens:     actualUsage.PromptTokens,
				CompletionTokens: actualUsage.CompletionTokens,
				ChannelID:        ch.ID,
				ElapsedTime:      time.Since(startedAt).Milliseconds(),
				IsStream:         true,
			}
			if err := s.commitQuotaAfterResponse(reservation.ReservationId, actualUsage.TotalTokens, true, logInput); err != nil {
				s.logPostResponseCommitError(err)
			} else {
				s.ingestUsageLogAfterResponse(logInput)
			}
			upstreamResp = &relayprovider.RawResponse{StatusCode: streamResp.StatusCode}
			responseChannel = ch
			return nil
		}

		resp, forwardErr := s.forwardResponsesRaw(ctx, ch, r.Method, upstreamPath, r.URL.RawQuery, r.Header.Clone(), body)
		if forwardErr != nil {
			if shouldFallbackResponsesToChat(upstreamPath, forwardErr) {
				fallbackResp, fallbackErr := s.forwardResponsesViaChatFallback(ctx, ch, r.Header.Clone(), body)
				if fallbackErr == nil && fallbackResp.Response != nil {
					usage := fallbackResp.Usage
					if usage.TotalTokens <= 0 {
						usage = extractRawUsage(fallbackResp.Response.Body, estimateRawTokens(body))
					}
					logInput := usageLogInput{
						UserID:           plan.Auth.UserID,
						TokenID:          plan.Auth.TokenID,
						TokenName:        plan.Auth.TokenName,
						RequestID:        requestID,
						Endpoint:         "/chat/completions",
						ModelName:        clientModel,
						Quota:            usage.TotalTokens,
						PromptTokens:     usage.PromptTokens,
						CompletionTokens: usage.CompletionTokens,
						ChannelID:        ch.ID,
						ElapsedTime:      time.Since(startedAt).Milliseconds(),
						IsStream:         false,
					}
					if err := s.commitQuota(ctx, reservation.ReservationId, usage.TotalTokens, true, logInput); err != nil {
						return err
					}
					s.ingestUsageLog(ctx, logInput)
					upstreamResp = fallbackResp.Response
					responseChannel = ch
					return nil
				}
			}
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
			ModelName:        clientModel,
			Quota:            usage.TotalTokens,
			PromptTokens:     usage.PromptTokens,
			CompletionTokens: usage.CompletionTokens,
			ChannelID:        ch.ID,
			ElapsedTime:      time.Since(startedAt).Milliseconds(),
			IsStream:         false,
		}
		if err := s.commitQuota(ctx, reservation.ReservationId, usage.TotalTokens, true, logInput); err != nil {
			return err
		}
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

	if upstreamResp.Body == nil {
		return
	}
	if responseID := extractResponseID(upstreamResp.Body); responseID != "" {
		s.storeResponseRoute(responseID, responseRoute{Model: clientModel, Channel: *responseChannel, UserID: plan.Auth.UserID})
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

	requestID := generateRequestID()
	reservation, err := s.reserveQuota(
		r.Context(),
		fmt.Sprintf("%d", authSnapshot.UserId),
		requestID,
		estimateRawTokens(body),
		route.Model,
		fmt.Sprintf("%d", route.Channel.ID),
	)
	if err != nil {
		s.writeError(w, http.StatusPaymentRequired, "quota reservation failed")
		return
	}

	startedAt := time.Now()
	if stream {
		streamResp, err := s.forwardResponsesRawStream(r.Context(), &route.Channel, r.Method, upstreamPath, r.URL.RawQuery, r.Header.Clone(), body)
		if err != nil {
			_ = s.releaseQuota(r.Context(), reservation.ReservationId, "upstream stream error")
			s.writeError(w, mapUpstreamError(relaybiz.UpstreamStatus(err)), "upstream service error")
			return
		}
		usage := newRawStreamUsageTracker(estimateRawUsage(body))
		writeRawStreamResponse(w, streamResp, usage)
		actualUsage := usage.Usage()
		logInput := usageLogInput{
			UserID:           authSnapshot.UserId,
			TokenID:          authSnapshot.TokenId,
			TokenName:        authSnapshot.TokenName,
			RequestID:        requestID,
			Endpoint:         upstreamPath,
			ModelName:        route.Model,
			Quota:            actualUsage.TotalTokens,
			PromptTokens:     actualUsage.PromptTokens,
			CompletionTokens: actualUsage.CompletionTokens,
			ChannelID:        route.Channel.ID,
			ElapsedTime:      time.Since(startedAt).Milliseconds(),
			IsStream:         true,
		}
		if err := s.commitQuotaAfterResponse(reservation.ReservationId, actualUsage.TotalTokens, true, logInput); err != nil {
			s.logPostResponseCommitError(err)
		} else {
			s.ingestUsageLogAfterResponse(logInput)
		}
		return
	}

	resp, err := s.forwardResponsesRaw(r.Context(), &route.Channel, r.Method, upstreamPath, r.URL.RawQuery, r.Header.Clone(), body)
	if err != nil {
		_ = s.releaseQuota(r.Context(), reservation.ReservationId, "upstream error")
		s.writeError(w, mapUpstreamError(relaybiz.UpstreamStatus(err)), "upstream service error")
		return
	}

	usage := extractRawUsage(resp.Body, estimateRawTokens(body))
	logInput := usageLogInput{
		UserID:           authSnapshot.UserId,
		TokenID:          authSnapshot.TokenId,
		TokenName:        authSnapshot.TokenName,
		RequestID:        requestID,
		Endpoint:         upstreamPath,
		ModelName:        route.Model,
		Quota:            usage.TotalTokens,
		PromptTokens:     usage.PromptTokens,
		CompletionTokens: usage.CompletionTokens,
		ChannelID:        route.Channel.ID,
		ElapsedTime:      time.Since(startedAt).Milliseconds(),
		IsStream:         false,
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

func (s *HTTPServer) forwardResponsesRaw(ctx context.Context, ch *relaybiz.Channel, method, path, query string, header http.Header, body []byte) (*relayprovider.RawResponse, error) {
	provider, err := s.providerFactory.CreateProviderWithConfig(ch.Type, ch.BaseURL, ch.Key, relayprovider.ProviderConfig{
		APIVersion: ch.Config.APIVersion,
	})
	if err != nil {
		return nil, fmt.Errorf("failed to create provider: %w", err)
	}
	return provider.Forward(ctx, &relayprovider.RawRequest{
		Method: method,
		Path:   path,
		Query:  query,
		Header: header,
		Body:   body,
	})
}

func (s *HTTPServer) forwardResponsesRawStream(ctx context.Context, ch *relaybiz.Channel, method, path, query string, header http.Header, body []byte) (*relayprovider.RawStreamResponse, error) {
	provider, err := s.providerFactory.CreateProviderWithConfig(ch.Type, ch.BaseURL, ch.Key, relayprovider.ProviderConfig{
		APIVersion: ch.Config.APIVersion,
	})
	if err != nil {
		return nil, fmt.Errorf("failed to create provider: %w", err)
	}
	return provider.ForwardStream(ctx, &relayprovider.RawRequest{
		Method: method,
		Path:   path,
		Query:  query,
		Header: header,
		Body:   body,
	})
}

func (s *HTTPServer) storeResponseRoute(responseID string, route responseRoute) {
	if responseID == "" {
		return
	}
	s.responsesMu.Lock()
	defer s.responsesMu.Unlock()
	s.responseRoutes[responseID] = route
}

func (s *HTTPServer) lookupResponseRoute(responseID string) (responseRoute, bool) {
	if responseID == "" {
		return responseRoute{}, false
	}
	s.responsesMu.RLock()
	defer s.responsesMu.RUnlock()
	route, ok := s.responseRoutes[responseID]
	return route, ok
}

func providerConfigFromChannelInfo(channel *commonv1.ChannelInfo) relayprovider.ProviderConfig {
	if channel == nil || channel.Config == nil {
		return relayprovider.ProviderConfig{}
	}
	return relayprovider.ProviderConfig{APIVersion: channel.Config.ApiVersion}
}

func (s *HTTPServer) handleUnsupportedOpenAIRoute(feature string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		s.writeJSON(w, http.StatusNotImplemented, map[string]interface{}{
			"error": map[string]interface{}{
				"message": fmt.Sprintf("%s is not implemented", feature),
				"type":    "one_api_not_implemented",
				"param":   nil,
				"code":    "not_implemented",
			},
		})
	}
}

func (s *HTTPServer) handleOneAPIProxy(w http.ResponseWriter, r *http.Request) {
	const prefix = "/v1/oneapi/proxy/"

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

	rest := strings.TrimPrefix(r.URL.Path, prefix)
	channelPart, targetPart, ok := strings.Cut(rest, "/")
	if !ok || channelPart == "" || targetPart == "" {
		s.writeError(w, http.StatusBadRequest, "invalid proxy path")
		return
	}
	channelID, err := parsePositiveInt64(channelPart)
	if err != nil {
		s.writeError(w, http.StatusBadRequest, "invalid channel id")
		return
	}

	authSnapshot, err := s.getAuthSnapshot(r.Context(), token)
	if err != nil {
		s.handleIdentityError(w, err)
		return
	}

	channelReply, err := s.channelClient.GetChannel(r.Context(), &channelv1.GetChannelRequest{ChannelId: channelID})
	if err != nil {
		s.handleChannelError(w, err)
		return
	}
	if channelReply.Channel == nil {
		s.writeError(w, http.StatusNotFound, "channel not found")
		return
	}

	body, err := io.ReadAll(io.LimitReader(r.Body, 64*1024*1024))
	if err != nil {
		s.writeError(w, http.StatusBadRequest, "failed to read request body")
		return
	}
	model := extractRawModel(body)
	if model == "" {
		model = "proxy"
	}

	requestID := generateRequestID()
	startedAt := time.Now()
	reservation, err := s.reserveQuota(
		r.Context(),
		fmt.Sprintf("%d", authSnapshot.UserId),
		requestID,
		estimateRawTokens(body),
		model,
		fmt.Sprintf("%d", channelReply.Channel.Id),
	)
	if err != nil {
		s.writeError(w, http.StatusPaymentRequired, "quota reservation failed")
		return
	}

	provider, err := s.providerFactory.CreateProviderWithConfig(channelReply.Channel.Type, channelReply.Channel.BaseUrl, channelReply.Channel.Key, providerConfigFromChannelInfo(channelReply.Channel))
	if err != nil {
		_ = s.releaseQuota(r.Context(), reservation.ReservationId, "failed to create provider")
		s.writeError(w, http.StatusInternalServerError, "failed to create provider")
		return
	}

	resp, err := provider.Forward(r.Context(), &relayprovider.RawRequest{
		Method: r.Method,
		Path:   "/" + targetPart,
		Query:  r.URL.RawQuery,
		Header: r.Header.Clone(),
		Body:   body,
	})
	if err != nil {
		_ = s.releaseQuota(r.Context(), reservation.ReservationId, "upstream error")
		s.writeError(w, mapUpstreamError(relaybiz.UpstreamStatus(err)), "upstream service error")
		return
	}

	totalTokens := extractTotalTokens(resp.Body, estimateRawTokens(body))
	usage := extractRawUsage(resp.Body, totalTokens)
	if err := s.commitQuota(r.Context(), reservation.ReservationId, totalTokens, true, usageLogInput{
		UserID:           authSnapshot.UserId,
		TokenID:          authSnapshot.TokenId,
		TokenName:        authSnapshot.TokenName,
		RequestID:        requestID,
		Endpoint:         "/" + targetPart,
		ModelName:        model,
		Quota:            totalTokens,
		PromptTokens:     usage.PromptTokens,
		CompletionTokens: usage.CompletionTokens,
		ChannelID:        channelReply.Channel.Id,
		ElapsedTime:      time.Since(startedAt).Milliseconds(),
		IsStream:         false,
	}); err != nil {
		s.writeError(w, http.StatusPaymentRequired, "billing commit failed")
		return
	}
	writeRawResponse(w, resp)
}

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

	var req relayprovider.ChatCompletionsRequest
	if err := decodeJSON(r.Body, &req); err != nil {
		s.writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}

	if req.Model == "" {
		s.writeError(w, http.StatusBadRequest, "model is required")
		return
	}

	// Delegate auth, model validation, model mapping, and channel selection to biz layer
	plan, err := s.relayUsecase.Plan(r.Context(), relaybiz.RelayRequest{
		Token: token,
		Model: req.Model,
	})
	if err != nil {
		s.handleRelayPlanError(w, err)
		return
	}

	clientModel := req.Model

	// Use resolved model name for upstream calls
	req.Model = plan.ResolvedModel

	// Use RetryExecutor for upstream calls with channel fallback
	retryExecutor := s.relayUsecase.NewRetryExecutor()
	result := retryExecutor.Execute(r.Context(), plan.Auth.Group, clientModel, func(ctx context.Context, ch *relaybiz.Channel) error {
		startedAt := time.Now()
		// Reserve quota
		requestID := generateRequestID()
		estimatedTokens := s.estimateTokens(&req)
		reservation, reserveErr := s.reserveQuota(ctx, fmt.Sprintf("%d", plan.Auth.UserID), requestID, estimatedTokens, plan.ResolvedModel, fmt.Sprintf("%d", ch.ID))
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
			s.handleStreamingResponse(w, r, provider, &req, reservation, usageLogInput{
				UserID:    plan.Auth.UserID,
				TokenID:   plan.Auth.TokenID,
				TokenName: plan.Auth.TokenName,
				RequestID: requestID,
				Endpoint:  "/v1/chat/completions",
				ModelName: clientModel,
				ChannelID: ch.ID,
				IsStream:  true,
			})
			return nil
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
			ModelName:        clientModel,
			Quota:            actualTokens,
			PromptTokens:     int64(resp.Usage.PromptTokens),
			CompletionTokens: int64(resp.Usage.CompletionTokens),
			ChannelID:        ch.ID,
			ElapsedTime:      time.Since(startedAt).Milliseconds(),
			IsStream:         false,
		}
		if err := s.commitQuota(ctx, reservation.ReservationId, actualTokens, true, logInput); err != nil {
			return err
		}
		s.ingestUsageLog(ctx, logInput)
		s.writeJSON(w, http.StatusOK, resp)
		return nil
	})

	if result.Err != nil {
		s.writeError(w, mapUpstreamError(relaybiz.UpstreamStatus(result.Err)), "upstream service error")
	}
}

func (s *HTTPServer) handleStreamingResponse(w http.ResponseWriter, r *http.Request, provider relayprovider.Provider, req *relayprovider.ChatCompletionsRequest, reservation *billingv1.ReserveQuotaResponse, logInput usageLogInput) {
	startedAt := time.Now()
	chunkChan, err := provider.ChatCompletionsStream(r.Context(), req)
	if err != nil {
		// 流式请求失败，释放预扣配额
		_ = s.releaseQuota(r.Context(), reservation.ReservationId, "upstream stream error")
		s.writeError(w, http.StatusBadGateway, "upstream stream error")
		return
	}

	flusher, ok := w.(http.Flusher)
	if !ok {
		// 流式不支持，释放预扣配额
		_ = s.releaseQuota(r.Context(), reservation.ReservationId, "streaming not supported")
		s.writeError(w, http.StatusInternalServerError, "streaming not supported")
		return
	}

	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	w.Header().Set("Transfer-Encoding", "chunked")

	totalTokens := int64(0)
	promptTokens := int64(0)
	completionTokens := int64(0)
	estimatedTokens := int64(0)
	streamError := false

	for chunk := range chunkChan {
		if chunk.Usage.TotalTokens > 0 {
			totalTokens = int64(chunk.Usage.TotalTokens)
			promptTokens = int64(chunk.Usage.PromptTokens)
			completionTokens = int64(chunk.Usage.CompletionTokens)
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
		logInput.ElapsedTime = time.Since(startedAt).Milliseconds()
		if logInput.Endpoint == "" {
			logInput.Endpoint = "/v1/chat/completions"
		}
		if err := s.commitQuotaAfterResponse(reservation.ReservationId, totalTokens, true, logInput); err != nil {
			s.logPostResponseCommitError(err)
		} else {
			s.ingestUsageLogAfterResponse(logInput)
		}
	} else {
		_ = s.releaseQuota(r.Context(), reservation.ReservationId, "stream error")
	}
}

type usageLogInput struct {
	UserID           int64
	TokenID          int64
	TokenName        string
	RequestID        string
	Endpoint         string
	ModelName        string
	Quota            int64
	PromptTokens     int64
	CompletionTokens int64
	ChannelID        int64
	ElapsedTime      int64
	IsStream         bool
}

func (s *HTTPServer) ingestUsageLog(ctx context.Context, in usageLogInput) {
	if s.logClient == nil {
		return
	}
	message := fmt.Sprintf("model=%s quota=%d prompt_tokens=%d completion_tokens=%d channel=%d", in.ModelName, in.Quota, in.PromptTokens, in.CompletionTokens, in.ChannelID)
	_, err := s.logClient.IngestLog(ctx, &logv1.IngestLogRequest{
		Level:            "consume",
		Message:          message,
		Source:           "relay-gateway",
		RequestId:        in.RequestID,
		UserId:           in.UserID,
		TokenName:        usageTokenName(in),
		ModelName:        in.ModelName,
		Quota:            in.Quota,
		PromptTokens:     in.PromptTokens,
		CompletionTokens: in.CompletionTokens,
		ChannelId:        in.ChannelID,
		ElapsedTime:      in.ElapsedTime,
		IsStream:         in.IsStream,
	})
	if err != nil && applogger.Log != nil {
		applogger.Log.Warn("failed to ingest usage log", zap.Error(err))
	}
}

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

func (s *HTTPServer) handleModels(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
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

	authSnapshot, err := s.getAuthSnapshot(r.Context(), token)
	if err != nil {
		s.handleIdentityError(w, err)
		return
	}

	modelsReply, err := s.listAvailableModels(r.Context(), authSnapshot.Group)
	if err != nil {
		s.handleChannelError(w, err)
		return
	}

	models := s.applyModelWhitelist(modelsReply.Models, authSnapshot.AllowedModels)

	response := struct {
		Object string `json:"object"`
		Data   []struct {
			ID      string `json:"id"`
			Object  string `json:"object"`
			Created int64  `json:"created"`
			OwnedBy string `json:"owned_by"`
		} `json:"data"`
	}{
		Object: "list",
	}

	for _, model := range models {
		response.Data = append(response.Data, struct {
			ID      string `json:"id"`
			Object  string `json:"object"`
			Created int64  `json:"created"`
			OwnedBy string `json:"owned_by"`
		}{
			ID:      model,
			Object:  "model",
			Created: 0,
			OwnedBy: "organization",
		})
	}

	s.writeJSON(w, http.StatusOK, response)
}

func (s *HTTPServer) handleUsage(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
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
	token := strings.TrimSpace(strings.TrimPrefix(authHeader, "Bearer "))
	if token == "" {
		s.writeError(w, http.StatusUnauthorized, "missing token")
		return
	}

	authSnapshot, err := s.getAuthSnapshot(r.Context(), token)
	if err != nil {
		s.handleIdentityError(w, err)
		return
	}
	if s.billingClient == nil {
		s.writeError(w, http.StatusServiceUnavailable, "billing service unavailable")
		return
	}
	resp, err := s.billingClient.GetAccountSnapshot(r.Context(), &billingv1.GetAccountSnapshotRequest{
		UserId: strconv.FormatInt(authSnapshot.UserId, 10),
	})
	if err != nil {
		s.writeError(w, http.StatusBadGateway, "billing service error")
		return
	}
	account := resp.GetSnapshot()
	if account == nil {
		s.writeError(w, http.StatusBadGateway, "billing account not found")
		return
	}

	remaining := account.GetQuota()
	used := account.GetUsedQuota()
	frozen := account.GetFrozenQuota()
	quotaPerUSD := quotaPerUSDFromEnv()
	remainingUSD := float64(remaining) / float64(quotaPerUSD)
	s.writeJSON(w, http.StatusOK, map[string]interface{}{
		"mode":      "unrestricted",
		"isValid":   true,
		"is_active": true,
		"status":    "active",
		"user_id":   account.GetUserId(),
		"planName":  "钱包余额",
		"remaining": remainingUSD,
		"balance":   remainingUSD,
		"unit":      "USD",
		"quota": map[string]interface{}{
			"remaining": remaining,
			"used":      used,
			"frozen":    frozen,
			"unit":      "quota",
			"per_usd":   quotaPerUSD,
		},
		"usage": map[string]interface{}{
			"total": map[string]interface{}{
				"cost":     used,
				"requests": account.GetRequestCount(),
			},
		},
	})
}

func quotaPerUSDFromEnv() int64 {
	raw := strings.TrimSpace(os.Getenv("PAYMENT_QUOTA_PER_UNIT"))
	if raw == "" {
		return defaultQuotaPerUSD
	}
	value, err := strconv.ParseInt(raw, 10, 64)
	if err != nil || value <= 0 {
		return defaultQuotaPerUSD
	}
	return value
}

func (s *HTTPServer) handleRetrieveModel(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		s.writeError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}

	const prefix = "/v1/models/"
	modelID := strings.TrimPrefix(r.URL.Path, prefix)
	if modelID == "" || strings.Contains(modelID, "/") {
		s.writeError(w, http.StatusNotFound, "model not found")
		return
	}

	s.writeJSON(w, http.StatusOK, openAIModelResponse(modelID))
}

func (s *HTTPServer) handleAPIStatus(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		s.writeError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}

	s.writeJSON(w, http.StatusOK, map[string]interface{}{
		"success": true,
		"message": "",
		"data": map[string]interface{}{
			"version":              "micro-one-api",
			"system_name":          "micro-one-api",
			"email_verification":   false,
			"github_oauth":         false,
			"wechat_login":         false,
			"turnstile_check":      false,
			"display_in_currency":  false,
			"registration_enabled": true,
		},
	})
}

func (s *HTTPServer) handleDashboardModels(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		s.writeError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	s.writeJSON(w, http.StatusOK, map[string]interface{}{
		"success":  true,
		"message":  "",
		"data":     oneAPIChannelModelsByType(),
		"metadata": oneAPIProviderCatalogMetadata(),
	})
}

func (s *HTTPServer) handleGroups(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		s.writeError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	group := r.URL.Query().Get("group")
	if group == "" {
		group = "default"
	}
	s.writeJSON(w, http.StatusOK, map[string]interface{}{
		"success": true,
		"message": "",
		"data":    []string{group},
	})
}

func (s *HTTPServer) handleHealth(w http.ResponseWriter, r *http.Request) {
	s.writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

func openAIModelResponse(modelID string) map[string]interface{} {
	permissionID := "modelperm-micro-one-api"
	return map[string]interface{}{
		"id":       modelID,
		"object":   "model",
		"created":  1626777600,
		"owned_by": "organization",
		"permission": []map[string]interface{}{
			{
				"id":                   permissionID,
				"object":               "model_permission",
				"created":              1626777600,
				"allow_create_engine":  true,
				"allow_sampling":       true,
				"allow_logprobs":       true,
				"allow_search_indices": false,
				"allow_view":           true,
				"allow_fine_tuning":    false,
				"organization":         "*",
				"group":                nil,
				"is_blocking":          false,
			},
		},
		"root":   modelID,
		"parent": nil,
	}
}

func (s *HTTPServer) getAuthSnapshot(ctx context.Context, token string) (*identityv1.GetAuthSnapshotReply, error) {
	req := &identityv1.GetAuthSnapshotRequest{
		Token: token,
	}
	return s.identityClient.GetAuthSnapshot(ctx, req)
}

func (s *HTTPServer) listAvailableModels(ctx context.Context, group string) (*channelv1.ListAvailableModelsReply, error) {
	req := &channelv1.ListAvailableModelsRequest{
		Group: group,
	}
	return s.channelClient.ListAvailableModels(ctx, req)
}

func (s *HTTPServer) applyModelWhitelist(availableModels []string, allowedModels []string) []string {
	if len(allowedModels) == 0 {
		return availableModels
	}

	allowedSet := make(map[string]bool)
	for _, model := range allowedModels {
		allowedSet[model] = true
	}

	filtered := make([]string, 0, len(availableModels))
	for _, model := range availableModels {
		if allowedSet[model] {
			filtered = append(filtered, model)
		}
	}

	return filtered
}

// handleRelayPlanError maps biz-layer Plan() errors to HTTP responses.
func (s *HTTPServer) handleRelayPlanError(w http.ResponseWriter, err error) {
	// Check for structured errors
	if errors.IsUnauthorized(err) {
		s.writeError(w, http.StatusUnauthorized, "unauthorized")
		return
	}
	if errors.IsForbidden(err) {
		s.writeError(w, http.StatusForbidden, "forbidden")
		return
	}
	if errors.IsServiceUnavailable(err) {
		s.writeError(w, http.StatusServiceUnavailable, "service unavailable")
		return
	}

	// Handle gRPC errors from downstream services
	st, ok := status.FromError(err)
	if ok {
		switch st.Code() {
		case codes.NotFound:
			s.writeError(w, http.StatusUnauthorized, "unauthorized")
		case codes.PermissionDenied:
			s.writeError(w, http.StatusForbidden, "forbidden")
		case codes.ResourceExhausted:
			s.writeError(w, http.StatusTooManyRequests, "rate limit exceeded")
		case codes.Unavailable:
			s.writeError(w, http.StatusServiceUnavailable, "service unavailable")
		default:
			s.writeError(w, http.StatusInternalServerError, "internal server error")
		}
		return
	}

	// Model not allowed (string match from biz layer)
	if strings.Contains(err.Error(), "not allowed") {
		s.writeError(w, http.StatusForbidden, "model not allowed")
		return
	}

	s.writeError(w, http.StatusInternalServerError, "internal server error")
}

func (s *HTTPServer) handleIdentityError(w http.ResponseWriter, err error) {
	// Check for structured errors first
	if errors.IsUnauthorized(err) {
		s.writeError(w, http.StatusUnauthorized, "unauthorized")
		return
	}
	if errors.IsForbidden(err) {
		s.writeError(w, http.StatusForbidden, "forbidden")
		return
	}

	// Handle gRPC errors
	st, ok := status.FromError(err)
	if ok {
		switch st.Code() {
		case codes.NotFound:
			s.writeError(w, http.StatusUnauthorized, "unauthorized")
		case codes.PermissionDenied:
			s.writeError(w, http.StatusForbidden, "forbidden")
		case codes.ResourceExhausted:
			s.writeError(w, http.StatusTooManyRequests, "rate limit exceeded")
		default:
			s.writeError(w, http.StatusInternalServerError, "internal server error")
		}
		return
	}

	s.writeError(w, http.StatusInternalServerError, "internal server error")
}

func (s *HTTPServer) handleChannelError(w http.ResponseWriter, err error) {
	if errors.IsServiceUnavailable(err) {
		s.writeError(w, http.StatusServiceUnavailable, "service unavailable")
		return
	}
	s.writeError(w, http.StatusInternalServerError, "internal server error")
}

func (s *HTTPServer) writeError(w http.ResponseWriter, statusCode int, message string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(statusCode)
	encodeJSON(w, map[string]interface{}{
		"error": map[string]interface{}{
			"message": message,
		},
	})
}

func (s *HTTPServer) writeJSON(w http.ResponseWriter, statusCode int, data interface{}) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(statusCode)
	encodeJSON(w, data)
}

// 配额管理方法

func (s *HTTPServer) reserveQuota(ctx context.Context, userID, requestID string, estimatedTokens int64, model, channelID string) (*billingv1.ReserveQuotaResponse, error) {
	req := &billingv1.ReserveQuotaRequest{
		UserId:          userID,
		RequestId:       requestID,
		EstimatedTokens: estimatedTokens,
		Model:           model,
		ChannelId:       channelID,
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
		req.ElapsedTime = detail.ElapsedTime
		req.IsStream = detail.IsStream
	}
	billingCtx, cancel := detachedBillingContext(ctx)
	defer cancel()
	resp, err := s.billingClient.CommitQuota(billingCtx, req)
	if err != nil {
		return err
	}
	if resp == nil || !resp.GetSuccess() {
		return stderrors.New(billingErrorMessage(resp, "commit quota failed"))
	}
	return nil
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

type billingFailure interface {
	GetErrorMessage() string
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

func (s *HTTPServer) estimateTokens(req *relayprovider.ChatCompletionsRequest) int64 {
	// 简单的 token 估算逻辑
	// 实际应用中可以使用更精确的 tokenizer
	tokens := int64(0)

	// 估算输入 tokens
	for _, msg := range req.Messages {
		tokens += int64(len(msg.Content) / 4) // 假设平均每个 token 4 个字符
	}

	// 估算输出 tokens (基于 max_tokens 或默认值)
	if req.MaxTokens != nil && *req.MaxTokens > 0 {
		tokens += int64(*req.MaxTokens)
	} else {
		tokens += 1000 // 默认输出 tokens
	}

	return tokens
}

func (s *HTTPServer) calculateActualTokens(resp *relayprovider.ChatCompletionsResponse) int64 {
	// resp.Usage 不是指针，是值类型
	return int64(resp.Usage.TotalTokens)
}

func extractRawModel(body []byte) string {
	var payload map[string]interface{}
	if err := sonic.Unmarshal(body, &payload); err != nil {
		return ""
	}
	model, _ := payload["model"].(string)
	return strings.TrimSpace(model)
}

func isRawStreamRequest(body []byte) bool {
	var payload map[string]interface{}
	if err := sonic.Unmarshal(body, &payload); err != nil {
		return false
	}
	stream, _ := payload["stream"].(bool)
	return stream
}

func extractPreviousResponseID(body []byte) string {
	var payload map[string]interface{}
	if err := sonic.Unmarshal(body, &payload); err != nil {
		return ""
	}
	responseID, _ := payload["previous_response_id"].(string)
	return strings.TrimSpace(responseID)
}

func extractResponseID(body []byte) string {
	var payload struct {
		ID string `json:"id"`
	}
	if err := sonic.Unmarshal(body, &payload); err != nil {
		return ""
	}
	return strings.TrimSpace(payload.ID)
}

type rawUsage struct {
	PromptTokens     int64
	CompletionTokens int64
	TotalTokens      int64
}

func extractRawUsage(body []byte, fallback int64) rawUsage {
	var payload interface{}
	if err := sonic.Unmarshal(body, &payload); err != nil {
		return rawUsage{TotalTokens: fallback}
	}
	return normalizeRawUsage(extractRawUsageValue(payload), fallback)
}

func extractRawUsageValue(value interface{}) rawUsage {
	switch typed := value.(type) {
	case map[string]interface{}:
		var usage rawUsage
		if nested, ok := typed["usage"]; ok {
			usage = extractRawUsageValue(nested)
		}
		usage = mergeRawUsage(usage, rawUsage{
			PromptTokens:     numberField(typed, "prompt_tokens", "input_tokens"),
			CompletionTokens: numberField(typed, "completion_tokens", "output_tokens"),
			TotalTokens:      numberField(typed, "total_tokens"),
		})
		if usage.TotalTokens > 0 || usage.PromptTokens > 0 || usage.CompletionTokens > 0 {
			return usage
		}
		for _, nested := range typed {
			usage = extractRawUsageValue(nested)
			if usage.TotalTokens > 0 || usage.PromptTokens > 0 || usage.CompletionTokens > 0 {
				return usage
			}
		}
	case []interface{}:
		for _, item := range typed {
			usage := extractRawUsageValue(item)
			if usage.TotalTokens > 0 || usage.PromptTokens > 0 || usage.CompletionTokens > 0 {
				return usage
			}
		}
	}
	return rawUsage{}
}

func mergeRawUsage(primary, fallback rawUsage) rawUsage {
	if primary.PromptTokens == 0 {
		primary.PromptTokens = fallback.PromptTokens
	}
	if primary.CompletionTokens == 0 {
		primary.CompletionTokens = fallback.CompletionTokens
	}
	if primary.TotalTokens == 0 {
		primary.TotalTokens = fallback.TotalTokens
	}
	return primary
}

func normalizeRawUsage(usage rawUsage, fallback int64) rawUsage {
	return normalizeRawUsageWithFallback(usage, rawUsage{TotalTokens: fallback})
}

func normalizeRawUsageWithFallback(usage rawUsage, fallback rawUsage) rawUsage {
	if usage.TotalTokens == 0 && usage.PromptTokens+usage.CompletionTokens > 0 {
		usage.TotalTokens = usage.PromptTokens + usage.CompletionTokens
	}
	if usage.TotalTokens <= 0 {
		usage.TotalTokens = fallback.TotalTokens
	}
	if usage.PromptTokens == 0 {
		usage.PromptTokens = fallback.PromptTokens
	}
	if usage.CompletionTokens == 0 {
		usage.CompletionTokens = fallback.CompletionTokens
	}
	return usage
}

func numberField(m map[string]interface{}, keys ...string) int64 {
	for _, key := range keys {
		if value, ok := m[key]; ok {
			if number := int64Value(value); number != 0 {
				return number
			}
		}
	}
	return 0
}

func int64Value(value interface{}) int64 {
	switch v := value.(type) {
	case float64:
		return int64(v)
	case float32:
		return int64(v)
	case int:
		return int64(v)
	case int64:
		return v
	case int32:
		return int64(v)
	case json.Number:
		n, _ := v.Int64()
		return n
	default:
		return 0
	}
}

func parseResponsesResourcePath(method, path string) (string, bool) {
	const prefix = "/v1/responses/"
	rest := strings.TrimPrefix(path, prefix)
	if rest == "" || rest == path {
		return "", false
	}
	parts := strings.Split(rest, "/")
	if parts[0] == "" {
		return "", false
	}
	switch {
	case len(parts) == 1 && (method == http.MethodGet || method == http.MethodDelete):
		return parts[0], true
	case len(parts) == 2 && parts[1] == "cancel" && method == http.MethodPost:
		return parts[0], true
	case len(parts) == 2 && parts[1] == "input_items" && method == http.MethodGet:
		return parts[0], true
	default:
		return "", false
	}
}

func defaultRawModel(upstreamPath string) string {
	switch upstreamPath {
	case "/embeddings":
		return "text-embedding-ada-002"
	case "/moderations":
		return "text-moderation-latest"
	case "/audio/speech":
		return "tts-1"
	default:
		return ""
	}
}

func estimateRawPromptTokens(body []byte) int64 {
	tokens := int64(len(body) / 4)
	if tokens < 1 {
		return 1
	}
	return tokens
}

func estimateRawUsage(body []byte) rawUsage {
	promptTokens := estimateRawPromptTokens(body)
	completionTokens := int64(100)
	return rawUsage{
		TotalTokens: promptTokens + completionTokens,
	}
}

func estimateRawTokens(body []byte) int64 {
	return estimateRawUsage(body).TotalTokens
}

func extractTotalTokens(body []byte, fallback int64) int64 {
	return extractRawUsage(body, fallback).TotalTokens
}

func writeRawResponse(w http.ResponseWriter, resp *relayprovider.RawResponse) {
	for key, values := range resp.Header {
		if isRelayHopByHopHeader(key) {
			continue
		}
		for _, value := range values {
			w.Header().Add(key, value)
		}
	}
	if w.Header().Get("Content-Type") == "" {
		w.Header().Set("Content-Type", "application/json")
	}
	w.WriteHeader(resp.StatusCode)
	_, _ = w.Write(resp.Body)
}

type rawStreamUsageTracker struct {
	fallback rawUsage
	usage    rawUsage
	pending  string
}

func newRawStreamUsageTracker(fallback rawUsage) *rawStreamUsageTracker {
	return &rawStreamUsageTracker{fallback: fallback}
}

func (t *rawStreamUsageTracker) Observe(chunk []byte) {
	usage := extractRawUsage(chunk, 0)
	if usage.TotalTokens > 0 || usage.PromptTokens > 0 || usage.CompletionTokens > 0 {
		t.usage = mergeRawUsage(usage, t.usage)
	}
}

func (t *rawStreamUsageTracker) ObserveBytes(p []byte) {
	t.pending += string(p)
	for {
		line, rest, ok := strings.Cut(t.pending, "\n")
		if !ok {
			break
		}
		t.pending = rest
		data, ok := strings.CutPrefix(strings.TrimSpace(line), "data: ")
		if !ok || data == "" || data == "[DONE]" {
			continue
		}
		t.Observe([]byte(data))
	}
}

func (t *rawStreamUsageTracker) Usage() rawUsage {
	if strings.TrimSpace(t.pending) != "" {
		t.ObserveBytes([]byte("\n"))
	}
	return normalizeRawUsageWithFallback(t.usage, t.fallback)
}

func writeRawStreamResponse(w http.ResponseWriter, resp *relayprovider.RawStreamResponse, usageTracker ...*rawStreamUsageTracker) {
	defer resp.Body.Close()

	for key, values := range resp.Header {
		if isRelayHopByHopHeader(key) {
			continue
		}
		for _, value := range values {
			w.Header().Add(key, value)
		}
	}
	if w.Header().Get("Content-Type") == "" {
		w.Header().Set("Content-Type", "text/event-stream")
	}
	w.WriteHeader(resp.StatusCode)
	if flusher, ok := w.(http.Flusher); ok {
		_, _ = io.Copy(&flushWriter{w: w, flusher: flusher, usageTracker: firstRawStreamUsageTracker(usageTracker)}, resp.Body)
		return
	}
	_, _ = io.Copy(&streamUsageWriter{w: w, usageTracker: firstRawStreamUsageTracker(usageTracker)}, resp.Body)
}

func firstRawStreamUsageTracker(trackers []*rawStreamUsageTracker) *rawStreamUsageTracker {
	if len(trackers) == 0 {
		return nil
	}
	return trackers[0]
}

type flushWriter struct {
	w            http.ResponseWriter
	flusher      http.Flusher
	usageTracker *rawStreamUsageTracker
}

func (w *flushWriter) Write(p []byte) (int, error) {
	observeStreamUsage(w.usageTracker, p)
	n, err := w.w.Write(p)
	w.flusher.Flush()
	return n, err
}

type streamUsageWriter struct {
	w            io.Writer
	usageTracker *rawStreamUsageTracker
}

func (w *streamUsageWriter) Write(p []byte) (int, error) {
	observeStreamUsage(w.usageTracker, p)
	return w.w.Write(p)
}

func observeStreamUsage(tracker *rawStreamUsageTracker, p []byte) {
	if tracker == nil {
		return
	}
	tracker.ObserveBytes(p)
}

func isRelayHopByHopHeader(key string) bool {
	switch strings.ToLower(key) {
	case "connection", "keep-alive", "proxy-authenticate", "proxy-authorization",
		"te", "trailer", "transfer-encoding", "upgrade":
		return true
	default:
		return false
	}
}

func parsePositiveInt64(value string) (int64, error) {
	id, err := strconv.ParseInt(value, 10, 64)
	if err != nil {
		return 0, err
	}
	if id <= 0 {
		return 0, fmt.Errorf("id must be positive")
	}
	return id, nil
}

func generateRequestID() string {
	b := make([]byte, 16)
	if _, err := crypto_rand.Read(b); err != nil {
		return fmt.Sprintf("req_%d", time.Now().UnixNano())
	}
	return fmt.Sprintf("req_%x", b)
}

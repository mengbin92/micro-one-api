package server

import (
	"context"
	crypto_rand "crypto/rand"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/bytedance/sonic"
	"go.uber.org/zap"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	billingv1 "micro-one-api/api/billing/v1"
	channelv1 "micro-one-api/api/channel/v1"
	identityv1 "micro-one-api/api/identity/v1"
	"micro-one-api/internal/pkg/errors"
	applogger "micro-one-api/internal/pkg/logger"
	"micro-one-api/internal/pkg/metrics"
	relaybiz "micro-one-api/internal/relay/biz"
	relayprovider "micro-one-api/internal/relay/provider"

	khttp "github.com/go-kratos/kratos/v2/transport/http"
)

// HTTPServer handles HTTP requests for relay-gateway.
type HTTPServer struct {
	identityClient  identityv1.IdentityServiceClient
	channelClient   channelv1.ChannelServiceClient
	billingClient   billingv1.BillingServiceClient
	providerFactory *relayprovider.ProviderFactory
	relayUsecase    *relaybiz.RelayUsecase
}

// NewHTTPServer creates a new HTTP server for Kratos.
func NewHTTPServer(
	identityClient identityv1.IdentityServiceClient,
	channelClient channelv1.ChannelServiceClient,
	billingClient billingv1.BillingServiceClient,
	providerFactory *relayprovider.ProviderFactory,
	relayUsecase *relaybiz.RelayUsecase,
) *HTTPServer {
	return &HTTPServer{
		identityClient:  identityClient,
		channelClient:   channelClient,
		billingClient:   billingClient,
		providerFactory: providerFactory,
		relayUsecase:    relayUsecase,
	}
}

// RegisterRoutes registers HTTP routes to a Kratos *khttp.Server.
func (s *HTTPServer) RegisterRoutes(srv *khttp.Server) {
	srv.HandleFunc("/v1/chat/completions", s.handleChatCompletions)
	srv.HandleFunc("/v1/completions", s.handleRawRelay("/completions", true))
	srv.HandleFunc("/v1/embeddings", s.handleRawRelay("/embeddings", false))
	srv.HandleFunc("/v1/images/generations", s.handleRawRelay("/images/generations", true))
	srv.HandleFunc("/v1/audio/transcriptions", s.handleRawRelay("/audio/transcriptions", true))
	srv.HandleFunc("/v1/audio/translations", s.handleRawRelay("/audio/translations", true))
	srv.HandleFunc("/v1/audio/speech", s.handleRawRelay("/audio/speech", false))
	srv.HandleFunc("/v1/moderations", s.handleRawRelay("/moderations", false))
	srv.HandlePrefix("/v1/oneapi/proxy/", http.HandlerFunc(s.handleOneAPIProxy))
	srv.HandleFunc("/v1/models", s.handleModels)
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

			provider, provErr := s.providerFactory.CreateProvider(ch.Type, ch.BaseURL, ch.Key)
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

			_ = s.commitQuota(ctx, reservation.ReservationId, extractTotalTokens(resp.Body, estimateRawTokens(body)), true)
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

	reservation, err := s.reserveQuota(
		r.Context(),
		fmt.Sprintf("%d", authSnapshot.UserId),
		generateRequestID(),
		estimateRawTokens(body),
		model,
		fmt.Sprintf("%d", channelReply.Channel.Id),
	)
	if err != nil {
		s.writeError(w, http.StatusPaymentRequired, "quota reservation failed")
		return
	}

	provider, err := s.providerFactory.CreateProvider(channelReply.Channel.Type, channelReply.Channel.BaseUrl, channelReply.Channel.Key)
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

	_ = s.commitQuota(r.Context(), reservation.ReservationId, extractTotalTokens(resp.Body, estimateRawTokens(body)), true)
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
		// Reserve quota
		requestID := generateRequestID()
		estimatedTokens := s.estimateTokens(&req)
		reservation, reserveErr := s.reserveQuota(ctx, fmt.Sprintf("%d", plan.Auth.UserID), requestID, estimatedTokens, plan.ResolvedModel, fmt.Sprintf("%d", ch.ID))
		if reserveErr != nil {
			return &relaybiz.RetryableError{Status: http.StatusPaymentRequired, Err: reserveErr}
		}

		provider, provErr := s.providerFactory.CreateProvider(ch.Type, ch.BaseURL, ch.Key)
		if provErr != nil {
			_ = s.releaseQuota(ctx, reservation.ReservationId, "failed to create provider")
			return fmt.Errorf("failed to create provider: %w", provErr)
		}

		if req.Stream {
			s.handleStreamingResponse(w, r, provider, &req, reservation)
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
		_ = s.commitQuota(ctx, reservation.ReservationId, actualTokens, true)
		s.writeJSON(w, http.StatusOK, resp)
		return nil
	})

	if result.Err != nil {
		s.writeError(w, mapUpstreamError(relaybiz.UpstreamStatus(result.Err)), "upstream service error")
	}
}

func (s *HTTPServer) handleStreamingResponse(w http.ResponseWriter, r *http.Request, provider relayprovider.Provider, req *relayprovider.ChatCompletionsRequest, reservation *billingv1.ReserveQuotaResponse) {
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
	streamError := false

	for chunk := range chunkChan {
		// StreamChunk 没有 Usage 字段，我们需要估算 tokens
		// 这里简单使用字符数除以 4 来估算
		for _, choice := range chunk.Choices {
			totalTokens += int64(len(choice.Delta.Content) / 4)
		}

		jsonData, err := sonic.Marshal(chunk)
		if err != nil {
			applogger.Log.Warn("failed to marshal chunk", zap.Error(err))
			continue
		}

		fmt.Fprintf(w, "data: %s\n\n", string(jsonData))
		flusher.Flush()
	}

	fmt.Fprintf(w, "data: [DONE]\n\n")
	flusher.Flush()

	// 流式请求完成，提交配额
	if !streamError {
		_ = s.commitQuota(r.Context(), reservation.ReservationId, totalTokens, true)
	} else {
		_ = s.releaseQuota(r.Context(), reservation.ReservationId, "stream error")
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

func (s *HTTPServer) handleHealth(w http.ResponseWriter, r *http.Request) {
	s.writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
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
	return s.billingClient.ReserveQuota(ctx, req)
}

func (s *HTTPServer) commitQuota(ctx context.Context, reservationID string, actualTokens int64, success bool) error {
	req := &billingv1.CommitQuotaRequest{
		ReservationId: reservationID,
		ActualTokens:  actualTokens,
		Success:       success,
	}
	_, err := s.billingClient.CommitQuota(ctx, req)
	return err
}

func (s *HTTPServer) releaseQuota(ctx context.Context, reservationID, reason string) error {
	req := &billingv1.ReleaseQuotaRequest{
		ReservationId: reservationID,
		Reason:        reason,
	}
	_, err := s.billingClient.ReleaseQuota(ctx, req)
	return err
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

func estimateRawTokens(body []byte) int64 {
	tokens := int64(len(body) / 4)
	if tokens < 1 {
		return 1
	}
	return tokens + 100
}

func extractTotalTokens(body []byte, fallback int64) int64 {
	var payload struct {
		Usage struct {
			TotalTokens int64 `json:"total_tokens"`
		} `json:"usage"`
	}
	if err := sonic.Unmarshal(body, &payload); err != nil {
		return fallback
	}
	if payload.Usage.TotalTokens <= 0 {
		return fallback
	}
	return payload.Usage.TotalTokens
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

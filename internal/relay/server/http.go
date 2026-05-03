package server

import (
	"context"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/bytedance/sonic"
	"go.uber.org/zap"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	identityv1 "micro-one-api/api/identity/v1"
	channelv1 "micro-one-api/api/channel/v1"
	billingv1 "micro-one-api/api/billing/v1"
	"micro-one-api/internal/pkg/errors"
	applogger "micro-one-api/internal/pkg/logger"
	relaybiz "micro-one-api/internal/relay/biz"
	relayprovider "micro-one-api/internal/relay/provider"

	khttp "github.com/go-kratos/kratos/v2/transport/http"
)

// HTTPServer handles HTTP requests for relay-gateway.
type HTTPServer struct {
	identityClient  identityv1.IdentityServiceClient
	channelClient  channelv1.ChannelServiceClient
	billingClient  billingv1.BillingServiceClient
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
	srv.HandleFunc("/v1/models", s.handleModels)
	srv.HandleFunc("/healthz", s.handleHealth)
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
		s.writeError(w, http.StatusBadRequest, fmt.Sprintf("invalid request body: %v", err))
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

	// Use resolved model name for upstream calls
	req.Model = plan.ResolvedModel

	// Use RetryExecutor for upstream calls with channel fallback
	retryExecutor := s.relayUsecase.NewRetryExecutor()
	result := retryExecutor.Execute(r.Context(), plan.Auth.Group, plan.ResolvedModel, func(ctx context.Context, ch *relaybiz.Channel) error {
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
		s.writeError(w, mapUpstreamError(relaybiz.UpstreamStatus(result.Err)), fmt.Sprintf("upstream error after %d attempts: %v", result.Attempt+1, result.Err))
	}
}

func (s *HTTPServer) handleStreamingResponse(w http.ResponseWriter, r *http.Request, provider relayprovider.Provider, req *relayprovider.ChatCompletionsRequest, reservation *billingv1.ReserveQuotaResponse) {
	chunkChan, err := provider.ChatCompletionsStream(r.Context(), req)
	if err != nil {
		// 流式请求失败，释放预扣配额
		_ = s.releaseQuota(r.Context(), reservation.ReservationId, "upstream stream error")
		s.writeError(w, http.StatusBadGateway, fmt.Sprintf("upstream stream error: %v", err))
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
		s.writeError(w, http.StatusUnauthorized, err.Error())
		return
	}
	if errors.IsForbidden(err) {
		s.writeError(w, http.StatusForbidden, err.Error())
		return
	}
	if errors.IsServiceUnavailable(err) {
		s.writeError(w, http.StatusServiceUnavailable, err.Error())
		return
	}

	// Handle gRPC errors from downstream services
	st, ok := status.FromError(err)
	if ok {
		switch st.Code() {
		case codes.NotFound:
			s.writeError(w, http.StatusUnauthorized, st.Message())
		case codes.PermissionDenied:
			s.writeError(w, http.StatusForbidden, st.Message())
		case codes.ResourceExhausted:
			s.writeError(w, http.StatusTooManyRequests, st.Message())
		case codes.Unavailable:
			s.writeError(w, http.StatusServiceUnavailable, st.Message())
		default:
			s.writeError(w, http.StatusInternalServerError, st.Message())
		}
		return
	}

	// Model not allowed (string match from biz layer)
	if strings.Contains(err.Error(), "not allowed") {
		s.writeError(w, http.StatusForbidden, err.Error())
		return
	}

	s.writeError(w, http.StatusInternalServerError, err.Error())
}

func (s *HTTPServer) handleIdentityError(w http.ResponseWriter, err error) {
	// Check for structured errors first
	if errors.IsUnauthorized(err) {
		s.writeError(w, http.StatusUnauthorized, err.Error())
		return
	}
	if errors.IsForbidden(err) {
		s.writeError(w, http.StatusForbidden, err.Error())
		return
	}

	// Handle gRPC errors
	st, ok := status.FromError(err)
	if ok {
		switch st.Code() {
		case codes.NotFound:
			s.writeError(w, http.StatusUnauthorized, st.Message())
		case codes.PermissionDenied:
			s.writeError(w, http.StatusForbidden, st.Message())
		case codes.ResourceExhausted:
			s.writeError(w, http.StatusTooManyRequests, st.Message())
		default:
			s.writeError(w, http.StatusInternalServerError, st.Message())
		}
		return
	}

	s.writeError(w, http.StatusInternalServerError, err.Error())
}

func (s *HTTPServer) handleChannelError(w http.ResponseWriter, err error) {
	if errors.IsServiceUnavailable(err) {
		s.writeError(w, http.StatusServiceUnavailable, err.Error())
		return
	}
	s.writeError(w, http.StatusInternalServerError, err.Error())
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
		UserId:         userID,
		RequestId:      requestID,
		EstimatedTokens: estimatedTokens,
		Model:          model,
		ChannelId:      channelID,
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

func generateRequestID() string {
	return fmt.Sprintf("req_%d", time.Now().UnixNano())
}


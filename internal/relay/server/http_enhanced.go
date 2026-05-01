package server

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"time"

	"go.uber.org/zap"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"micro-one-api/api/identity/v1"
	channelv1 "micro-one-api/api/channel/v1"
	"micro-one-api/internal/pkg/errors"
	applogger "micro-one-api/internal/pkg/logger"
	appmiddleware "micro-one-api/internal/pkg/middleware"
	apptimeout "micro-one-api/internal/pkg/timeout"
	appvalidation "micro-one-api/internal/pkg/validation"
	relayprovider "micro-one-api/internal/relay/provider"

	khttp "github.com/go-kratos/kratos/v2/transport/http"
)

// EnhancedHTTPServer handles HTTP requests with all security features
type EnhancedHTTPServer struct {
	identityClient  identityv1.IdentityServiceClient
	channelClient  channelv1.ChannelServiceClient
	providerFactory *relayprovider.ProviderFactory
}

// NewEnhancedHTTPServer creates a new enhanced HTTP server with all security features
func NewEnhancedHTTPServer(
	identityClient identityv1.IdentityServiceClient,
	channelClient channelv1.ChannelServiceClient,
	providerFactory *relayprovider.ProviderFactory,
) *EnhancedHTTPServer {
	return &EnhancedHTTPServer{
		identityClient:  identityClient,
		channelClient:   channelClient,
		providerFactory: providerFactory,
	}
}

// RegisterRoutesWithSecurity registers HTTP routes with all security middleware
func (s *EnhancedHTTPServer) RegisterRoutesWithSecurity(srv *khttp.Server) {
	// Register routes with middleware
	srv.HandleFunc("/v1/chat/completions", func(w http.ResponseWriter, r *http.Request) {
		chain := chainMiddlewares(
			appmiddleware.SecurityHeaders,
			appmiddleware.RequestID,
			appmiddleware.SimpleCORS(),
			appmiddleware.SimpleMaxBodySize(),
			appmiddleware.SimpleRateLimit(),
			appmiddleware.LoggingMiddleware,
		)
		chain(http.HandlerFunc(s.handleChatCompletions)).ServeHTTP(w, r)
	})

	srv.HandleFunc("/v1/models", func(w http.ResponseWriter, r *http.Request) {
		chain := chainMiddlewares(
			appmiddleware.SecurityHeaders,
			appmiddleware.RequestID,
			appmiddleware.SimpleCORS(),
			appmiddleware.SimpleMaxBodySize(),
			appmiddleware.SimpleRateLimit(),
			appmiddleware.LoggingMiddleware,
		)
		chain(http.HandlerFunc(s.handleModels)).ServeHTTP(w, r)
	})

	srv.HandleFunc("/health", func(w http.ResponseWriter, r *http.Request) {
		chain := chainMiddlewares(
			appmiddleware.SecurityHeaders,
			appmiddleware.RequestID,
			appmiddleware.LoggingMiddleware,
		)
		chain(http.HandlerFunc(s.handleHealth)).ServeHTTP(w, r)
	})
}

// handleChatCompletions handles chat completions with validation and security
func (s *EnhancedHTTPServer) handleChatCompletions(w http.ResponseWriter, r *http.Request) {
	// Set response headers
	w.Header().Set("Content-Type", "application/json")

	// Check method
	if r.Method != http.MethodPost {
		s.writeError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}

	// Validate authorization
	token, err := s.validateAuthorization(r)
	if err != nil {
		s.handleAuthError(w, err)
		return
	}

	// Parse and validate request
	var req relayprovider.ChatCompletionsRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		s.writeError(w, http.StatusBadRequest, fmt.Sprintf("invalid request body: %v", err))
		return
	}

	// Validate request
	if err := appvalidation.ValidateChatCompletionsRequest(&req); err != nil {
		applogger.Log.Warn("Invalid chat completions request",
			zap.Error(err),
			zap.String("request_id", appmiddleware.GetRequestID(r.Context())),
		)
		s.writeValidationError(w, err)
		return
	}

	// Get auth snapshot with timeout
	ctx, cancel := apptimeout.WithGRPCTimeout(r.Context())
	defer cancel()

	authSnapshot, err := s.getAuthSnapshot(ctx, token)
	if err != nil {
		s.handleIdentityError(w, err)
		return
	}

	// Check model permissions
	if !s.isModelAllowed(authSnapshot.AllowedModels, req.Model) {
		applogger.Log.Warn("Model not allowed",
			zap.String("model", req.Model),
			zap.String("user_id", fmt.Sprintf("%d", authSnapshot.UserId)),
			zap.Strings("allowed_models", authSnapshot.AllowedModels),
			zap.String("request_id", appmiddleware.GetRequestID(r.Context())),
		)
		s.writeError(w, http.StatusForbidden, "model not allowed")
		return
	}

	// Select channel with timeout
	channel, err := s.selectChannel(ctx, authSnapshot.Group, req.Model)
	if err != nil {
		s.handleChannelError(w, err)
		return
	}

	// Create provider
	provider, err := s.providerFactory.CreateProvider(channel.Type, channel.BaseUrl, channel.Key)
	if err != nil {
		s.writeError(w, http.StatusInternalServerError, "failed to create provider")
		return
	}

	// Handle streaming or non-streaming response
	if req.Stream {
		s.handleStreamingResponse(w, r, provider, &req)
		return
	}

	// Handle non-streaming with timeout
	ctx, cancel = apptimeout.WithUpstreamTimeout(r.Context())
	defer cancel()

	resp, err := provider.ChatCompletions(ctx, &req)
	if err != nil {
		s.writeError(w, http.StatusBadGateway, "upstream error")
		return
	}

	s.writeJSON(w, http.StatusOK, resp)
}

// handleStreamingResponse handles streaming responses
func (s *EnhancedHTTPServer) handleStreamingResponse(w http.ResponseWriter, r *http.Request, provider relayprovider.Provider, req *relayprovider.ChatCompletionsRequest) {
	ctx, cancel := apptimeout.WithUpstreamTimeout(r.Context())
	defer cancel()

	chunkChan, err := provider.ChatCompletionsStream(ctx, req)
	if err != nil {
		s.writeError(w, http.StatusBadGateway, "upstream stream error")
		return
	}

	flusher, ok := w.(http.Flusher)
	if !ok {
		s.writeError(w, http.StatusInternalServerError, "streaming not supported")
		return
	}

	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	w.Header().Set("Transfer-Encoding", "chunked")

	for chunk := range chunkChan {
		jsonData, err := json.Marshal(chunk)
		if err != nil {
			applogger.Log.Warn("failed to marshal chunk", zap.Error(err))
			continue
		}

		fmt.Fprintf(w, "data: %s\n\n", string(jsonData))
		flusher.Flush()
	}

	fmt.Fprintf(w, "data: [DONE]\n\n")
	flusher.Flush()
}

// handleModels handles model list requests
func (s *EnhancedHTTPServer) handleModels(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")

	if r.Method != http.MethodGet {
		s.writeError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}

	// Validate authorization
	token, err := s.validateAuthorization(r)
	if err != nil {
		s.handleAuthError(w, err)
		return
	}

	// Get auth snapshot with timeout
	ctx, cancel := apptimeout.WithGRPCTimeout(r.Context())
	defer cancel()

	authSnapshot, err := s.getAuthSnapshot(ctx, token)
	if err != nil {
		s.handleIdentityError(w, err)
		return
	}

	// List available models with timeout
	modelsReply, err := s.listAvailableModels(ctx, authSnapshot.Group)
	if err != nil {
		s.handleChannelError(w, err)
		return
	}

	// Apply model whitelist
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

// handleHealth handles health check requests
func (s *EnhancedHTTPServer) handleHealth(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	s.writeJSON(w, http.StatusOK, map[string]string{
		"status":    "ok",
		"timestamp": time.Now().Format(time.RFC3339),
	})
}

// validateAuthorization validates the authorization header
func (s *EnhancedHTTPServer) validateAuthorization(r *http.Request) (string, error) {
	authHeader := r.Header.Get("Authorization")
	if authHeader == "" {
		return "", fmt.Errorf("missing authorization header")
	}

	if !strings.HasPrefix(authHeader, "Bearer ") {
		return "", fmt.Errorf("invalid authorization header format")
	}

	token := strings.TrimPrefix(authHeader, "Bearer ")
	if token == "" {
		return "", fmt.Errorf("missing token")
	}

	return token, nil
}

// writeValidationError writes a validation error response
func (s *EnhancedHTTPServer) writeValidationError(w http.ResponseWriter, err error) {
	if appvalidation.IsValidationError(err) {
		w.WriteHeader(http.StatusBadRequest)
		json.NewEncoder(w).Encode(map[string]interface{}{
			"error": map[string]interface{}{
				"message": err.Error(),
				"code":    400,
				"type":    "validation_error",
			},
		})
		return
	}
	s.writeError(w, http.StatusBadRequest, err.Error())
}

// chainMiddlewares chains multiple middleware functions
func chainMiddlewares(middlewares ...func(http.Handler) http.Handler) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		for i := len(middlewares) - 1; i >= 0; i-- {
			next = middlewares[i](next)
		}
		return next
	}
}

// Remaining methods are the same as in the original HTTPServer
func (s *EnhancedHTTPServer) getAuthSnapshot(ctx context.Context, token string) (*identityv1.GetAuthSnapshotReply, error) {
	req := &identityv1.GetAuthSnapshotRequest{
		Token: token,
	}
	return s.identityClient.GetAuthSnapshot(ctx, req)
}

func (s *EnhancedHTTPServer) selectChannel(ctx context.Context, group, model string) (*channelv1.ChannelInfo, error) {
	req := &channelv1.SelectChannelRequest{
		Group:               group,
		Model:               model,
		ExcludeFirstPriority: false,
	}
	resp, err := s.channelClient.SelectChannel(ctx, req)
	if err != nil {
		return nil, err
	}
	return resp.Channel, nil
}

func (s *EnhancedHTTPServer) listAvailableModels(ctx context.Context, group string) (*channelv1.ListAvailableModelsReply, error) {
	req := &channelv1.ListAvailableModelsRequest{
		Group: group,
	}
	return s.channelClient.ListAvailableModels(ctx, req)
}

func (s *EnhancedHTTPServer) isModelAllowed(allowedModels []string, model string) bool {
	if len(allowedModels) == 0 {
		return true
	}
	for _, allowed := range allowedModels {
		if allowed == model {
			return true
		}
	}
	return false
}

func (s *EnhancedHTTPServer) applyModelWhitelist(availableModels []string, allowedModels []string) []string {
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

func (s *EnhancedHTTPServer) handleIdentityError(w http.ResponseWriter, err error) {
	if errors.IsUnauthorized(err) {
		s.writeError(w, http.StatusUnauthorized, err.Error())
		return
	}
	if errors.IsForbidden(err) {
		s.writeError(w, http.StatusForbidden, err.Error())
		return
	}

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
			s.writeError(w, http.StatusInternalServerError, "internal server error")
		}
		return
	}

	s.writeError(w, http.StatusInternalServerError, "internal server error")
}

func (s *EnhancedHTTPServer) handleChannelError(w http.ResponseWriter, err error) {
	if errors.IsServiceUnavailable(err) {
		s.writeError(w, http.StatusServiceUnavailable, err.Error())
		return
	}
	s.writeError(w, http.StatusInternalServerError, "internal server error")
}

func (s *EnhancedHTTPServer) handleAuthError(w http.ResponseWriter, err error) {
	s.writeError(w, http.StatusUnauthorized, err.Error())
}

func (s *EnhancedHTTPServer) writeError(w http.ResponseWriter, statusCode int, message string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(statusCode)
	json.NewEncoder(w).Encode(map[string]interface{}{
		"error": map[string]interface{}{
			"message": applogger.Sanitize(message),
			"code":    statusCode,
		},
	})
}

func (s *EnhancedHTTPServer) writeJSON(w http.ResponseWriter, statusCode int, data interface{}) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(statusCode)
	json.NewEncoder(w).Encode(data)
}

package server

import (
	"bytes"
	"context"
	"errors"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"

	"micro-one-api/pkg/jsonx"

	relayprovider "micro-one-api/domain/upstream/provider"
	relaybiz "micro-one-api/internal/biz"
)

type chatOrchestratorRequest struct {
	Model    string `json:"model"`
	Messages []any  `json:"messages"`
	Stream   bool   `json:"stream,omitempty"`
}

func (s *HTTPServer) handleChatCompletionsWithOrchestrator(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		s.writeError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}

	token, err := bearerTokenFromRequest(r)
	if err != nil {
		s.writeError(w, http.StatusUnauthorized, err.Error())
		return
	}

	body, err := readRouteRequestBody(r)
	if err != nil {
		s.writeRequestBodyError(w, r, err)
		return
	}

	var req chatOrchestratorRequest
	if err := jsonx.Unmarshal(body, &req); err != nil {
		s.writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	if req.Model == "" {
		s.writeError(w, http.StatusBadRequest, "model is required")
		return
	}
	if len(req.Messages) == 0 {
		s.writeError(w, http.StatusBadRequest, "messages are required")
		return
	}
	if req.Stream {
		// The v0.23 first slice is non-streaming only. Restore the body before
		// delegating so the legacy handler can apply its existing stream path.
		r.Body = io.NopCloser(bytes.NewReader(body))
		s.handleChatCompletions(w, r)
		return
	}
	if s.billingClient == nil {
		s.writeError(w, http.StatusServiceUnavailable, "billing service unavailable")
		return
	}

	executor := NewRelayExecutorWithDependencies(s.relayUsecase, s.providerFactory, httpRelayLifecycleHooks{s: s}, nil)
	result, err := executor.Execute(r.Context(), relaybiz.ExecutorRequest{
		Token:     token,
		Model:     req.Model,
		Endpoint:  string(EndpointChatCompletions),
		Body:      body,
		Headers:   relayExecutorHeaders(r.Header),
		RequestID: generateRequestID(),
	})
	if err != nil {
		status := http.StatusInternalServerError
		if result.StatusCode != 0 {
			status = result.StatusCode
		}
		s.writeError(w, status, orchestratorErrorMessage(status, err))
		return
	}
	writeOrchestratedRelayResult(w, relayResultFromExecutionResponse(result))
}

// relayExecutorHeaders copies only headers that are meaningful to an
// upstream Chat Completions request. Authentication and transport-owned
// headers stay at the HTTP boundary.
func relayExecutorHeaders(headers http.Header) map[string][]string {
	result := make(map[string][]string)
	for key, values := range headers {
		switch strings.ToLower(key) {
		case "accept", "content-type", "openai-beta", "openai-organization", "openai-project", "user-agent", "x-request-id":
			result[key] = append([]string(nil), values...)
		}
	}
	return result
}

func relayResultFromExecutionResponse(result relaybiz.ExecutionResponse) *RelayResult {
	return &RelayResult{
		Response:              io.NopCloser(bytes.NewReader(result.Body)),
		Headers:               httpHeaderToMap(result.Headers),
		StatusCode:            result.StatusCode,
		Usage:                 result.Usage,
		ChannelID:             result.ChannelID,
		SubscriptionAccountID: result.SubscriptionAccountID,
	}
}

func orchestratorErrorMessage(statusCode int, err error) string {
	var upstreamErr *relayprovider.UpstreamHTTPError
	if errors.As(err, &upstreamErr) {
		return sanitizeUpstreamError(statusCode, err)
	}
	return gatewayErrorMessage(statusCode)
}

func bearerTokenFromRequest(r *http.Request) (string, error) {
	authHeader := r.Header.Get("Authorization")
	if authHeader == "" {
		return "", errString("missing authorization header")
	}
	if !strings.HasPrefix(authHeader, "Bearer ") {
		return "", errString("invalid authorization header format")
	}
	token := strings.TrimSpace(strings.TrimPrefix(authHeader, "Bearer "))
	if token == "" {
		return "", errString("missing token")
	}
	return token, nil
}

type errString string

func (e errString) Error() string { return string(e) }

func writeOrchestratedRelayResult(w http.ResponseWriter, result *RelayResult) {
	if result == nil || result.Response == nil {
		http.Error(w, "empty upstream response", http.StatusBadGateway)
		return
	}
	defer result.Response.Close()

	for key, values := range result.Headers {
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
	status := result.StatusCode
	if status == 0 {
		status = http.StatusOK
	}
	w.WriteHeader(status)
	_, _ = io.Copy(w, result.Response)
}

type httpRelayLifecycleHooks struct {
	s *HTTPServer
}

func (h httpRelayLifecycleHooks) ReserveQuota(ctx context.Context, plan *relaybiz.RelayPlan, req *RelayRequest, estimated relaybiz.CanonicalUsage) (*Reservation, error) {
	if h.s == nil || h.s.billingClient == nil {
		return nil, errors.New("billing service unavailable")
	}
	reservation, err := h.s.reserveQuota(ctx,
		strconv.FormatInt(plan.Auth.UserID, 10),
		req.RequestID,
		estimated.TotalTokens,
		h.s.BillingModelName(req.Model, plan.ResolvedModel, plan.ResolvedModel),
		strconv.FormatInt(plan.Channel.ID, 10),
		subscriptionAccountIDFromPlan(plan),
	)
	if err != nil {
		return nil, err
	}
	return &Reservation{ID: reservation.ReservationId}, nil
}

func (h httpRelayLifecycleHooks) CheckUserRateLimit(ctx context.Context, plan *relaybiz.RelayPlan, _ *RelayRequest) error {
	if h.s == nil || plan == nil || plan.Auth == nil {
		return nil
	}
	return h.s.checkUserRPM(ctx, plan.Auth.UserID)
}

func (h httpRelayLifecycleHooks) CommitQuota(ctx context.Context, plan *relaybiz.RelayPlan, req *RelayRequest, reservation *Reservation, usage relaybiz.CanonicalUsage, success bool, latency time.Duration) error {
	if reservation == nil {
		return nil
	}
	if h.s == nil || h.s.billingClient == nil {
		return errors.New("billing service unavailable")
	}
	if usage.TotalTokens == 0 {
		usage.TotalTokens = usage.PromptTokens + usage.CompletionTokens
	}
	logInput := orchestratorUsageLogInput(h, plan, req, usage, latency, req.IsStream)
	return h.s.commitQuota(ctx, reservation.ID, usage.TotalTokens, success, logInput)
}

func (h httpRelayLifecycleHooks) ReleaseQuota(ctx context.Context, reservation *Reservation, reason string) error {
	if reservation == nil {
		return nil
	}
	if h.s == nil || h.s.billingClient == nil {
		return errors.New("billing service unavailable")
	}
	return h.s.releaseQuota(ctx, reservation.ID, reason)
}

func (h httpRelayLifecycleHooks) LogUsage(ctx context.Context, plan *relaybiz.RelayPlan, req *RelayRequest, usage relaybiz.CanonicalUsage, latency time.Duration, stream bool) {
	if h.s == nil {
		return
	}
	logInput := orchestratorUsageLogInput(h, plan, req, usage, latency, stream)
	logUpstreamUsage(logInput)
	h.s.ingestUsageLog(ctx, logInput)
	// Sprint 4: model usage stats are recorded inside commitQuota (which is
	// called by commitReservedQuota just before logUsage). Recording here
	// too would double-count.
}

func orchestratorUsageLogInput(h httpRelayLifecycleHooks, plan *relaybiz.RelayPlan, req *RelayRequest, usage relaybiz.CanonicalUsage, latency time.Duration, stream bool) usageLogInput {
	input := usageLogInput{
		UserID:                plan.Auth.UserID,
		TokenID:               plan.Auth.TokenID,
		TokenName:             plan.Auth.TokenName,
		RequestID:             req.RequestID,
		Endpoint:              "/v1/chat/completions",
		ModelName:             h.s.BillingModelName(req.Model, plan.ResolvedModel, plan.ResolvedModel),
		Quota:                 usage.TotalTokens,
		PromptTokens:          usage.PromptTokens,
		CompletionTokens:      usage.CompletionTokens,
		CacheReadTokens:       usage.CacheReadTokens,
		CacheCreation5mTokens: usage.CacheCreation5mTokens,
		CacheCreation1hTokens: usage.CacheCreation1hTokens,
		ChannelID:             plan.Channel.ID,
		SubscriptionAccountID: subscriptionAccountIDFromPlan(plan),
		ElapsedTime:           latency.Milliseconds(),
		IsStream:              stream,
	}
	// v0.11.0 Phase 2 §2.2 + Phase 0/1 ADR §3.3: thread plan-derived inputs
	// (upstream cost-key + prompt-exclusivity flag).
	input.applyPlanInputs(plan)
	return input
}

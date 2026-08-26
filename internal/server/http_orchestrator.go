package server

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
	"sync"
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
	setRelayObservationStream(r.Context(), req.Stream)
	if s.billingClient == nil {
		s.writeError(w, http.StatusServiceUnavailable, "billing service unavailable")
		return
	}

	executor := s.newStagedRelayExecutor()
	sessionHash := ""
	if s.subscriptionSessionStickyEnabled {
		sessionHash = extractSessionHashFromRequest(r, body)
	}
	result, err := executor.Execute(r.Context(), relaybiz.ExecutorRequest{
		Token:       token,
		Model:       req.Model,
		Endpoint:    string(EndpointChatCompletions),
		Body:        body,
		Headers:     relayExecutorHeaders(r.Header),
		RequestID:   generateRequestID(),
		SessionHash: sessionHash,
		Stream:      req.Stream,
	})
	if err != nil {
		status := http.StatusInternalServerError
		if result.StatusCode != 0 {
			status = result.StatusCode
		}
		s.writeError(w, status, orchestratorErrorMessage(status, err))
		return
	}
	if result.Stream != nil {
		writeOrchestratedRelayStream(w, result)
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
		if isRelayExecutorHeader(key) {
			result[key] = append([]string(nil), values...)
		}
	}
	return result
}

func isRelayExecutorHeader(key string) bool {
	switch strings.ToLower(key) {
	case "accept", "content-type", "anthropic-beta", "anthropic-version", "openai-beta", "openai-organization", "openai-project", "user-agent", "x-request-id":
		return true
	default:
		return false
	}
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
		if isRelayHopByHopHeader(key) || IsRelayCORSResponseHeader(key) {
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

func writeOrchestratedRelayStream(w http.ResponseWriter, result relaybiz.ExecutionResponse) {
	if result.Stream == nil {
		http.Error(w, "empty upstream stream", http.StatusBadGateway)
		return
	}
	defer result.Stream.Close()
	for key, values := range result.Headers {
		if isRelayHopByHopHeader(key) || IsRelayCORSResponseHeader(key) || strings.EqualFold(key, "Content-Length") {
			continue
		}
		for _, value := range values {
			w.Header().Add(key, value)
		}
	}
	if w.Header().Get("Content-Type") == "" {
		w.Header().Set("Content-Type", "text/event-stream")
	}
	w.Header().Set("X-Content-Type-Options", "nosniff")
	status := result.StatusCode
	if status == 0 {
		status = http.StatusOK
	}
	w.WriteHeader(status)
	if flusher, ok := w.(http.Flusher); ok {
		_, err := io.Copy(&flushWriter{w: w, flusher: flusher}, result.Stream)
		markRelayStreamInterrupted(result.Stream, err)
		return
	}
	_, err := io.Copy(w, result.Stream)
	markRelayStreamInterrupted(result.Stream, err)
}

func markRelayStreamInterrupted(stream relaybiz.RelayStream, err error) {
	if err == nil {
		return
	}
	if observed, ok := stream.(interface{ markInterrupted() }); ok {
		observed.markInterrupted()
	}
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

func (h httpRelayLifecycleHooks) CompleteStream(ctx context.Context, plan *relaybiz.RelayPlan, req *RelayRequest, responseID string) {
	if h.s == nil || plan == nil || plan.Channel == nil || plan.Auth == nil || req == nil {
		return
	}
	if req.SessionHash != "" {
		h.s.bindSubscriptionSession(ctx, plan.Auth.Group, req.SessionHash, plan)
	}
	if req.Endpoint != EndpointResponses || responseID == "" {
		return
	}
	route := responseRoute{
		Model: req.Model, GlobalModel: plan.BaseModel(), ResolvedModel: plan.ResolvedModel,
		Channel: *plan.Channel, UserID: plan.Auth.UserID,
		SubscriptionAccountID: subscriptionAccountIDFromPlan(plan),
	}
	if route.SubscriptionAccountID > 0 && plan.Account != nil && plan.Account.ID == route.SubscriptionAccountID {
		route.Account = plan.Account
	}
	h.s.storeResponseRoute(responseID, route)
}

func (h httpRelayLifecycleHooks) AcquireRelayAttempt(ctx context.Context, plan *relaybiz.RelayPlan, req relaybiz.ExecutorRequest) (func(), error) {
	if h.s == nil || plan == nil || plan.Channel == nil || plan.Channel.SubscriptionAccountID <= 0 {
		return func() {}, nil
	}
	accountID := subscriptionAccountIDFromPlan(plan)
	var concurrencyLimit, rpmLimit int32
	var sessionWindowLimitUSD float64
	if plan.Account != nil {
		concurrencyLimit = plan.Account.Concurrency
		rpmLimit = plan.Account.RPMLimit
		sessionWindowLimitUSD = plan.Account.SessionWindowLimitUSD
	}
	releaseSlot, acquired := h.s.accountConcurrency.TryAcquire(ctx, accountID, concurrencyLimit)
	if !acquired {
		return nil, &relaybiz.RetryableError{Status: http.StatusServiceUnavailable, Err: fmt.Errorf("subscription account %d at concurrency limit", accountID)}
	}
	h.s.reportSubscriptionAccountSlot(accountID, true)
	var releaseOnce sync.Once
	release := func() {
		releaseOnce.Do(func() {
			releaseSlot()
			h.s.reportSubscriptionAccountSlot(accountID, false)
		})
	}
	if h.s.accountRPM != nil && !h.s.accountRPM.TryAcquire(ctx, accountID, rpmLimit) {
		release()
		return nil, &relaybiz.RetryableError{Status: http.StatusServiceUnavailable, Err: fmt.Errorf("subscription account %d at rpm limit", accountID)}
	}
	if h.s.sessionWindow != nil && plan.Auth != nil && h.s.sessionWindow.Exceeded(ctx, plan.Auth.Group, req.SessionHash, accountID, sessionWindowLimitUSD) {
		release()
		return nil, &relaybiz.RetryableError{Status: http.StatusServiceUnavailable, Err: fmt.Errorf("subscription account %d at session window limit", accountID)}
	}
	return release, nil
}

func orchestratorUsageLogInput(h httpRelayLifecycleHooks, plan *relaybiz.RelayPlan, req *RelayRequest, usage relaybiz.CanonicalUsage, latency time.Duration, stream bool) usageLogInput {
	endpoint := "/v1" + endpointPath(req.Endpoint)
	if endpoint == "/v1" {
		endpoint = "/v1/chat/completions"
	}
	input := usageLogInput{
		UserID:                plan.Auth.UserID,
		TokenID:               plan.Auth.TokenID,
		TokenName:             plan.Auth.TokenName,
		RequestID:             req.RequestID,
		Endpoint:              endpoint,
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

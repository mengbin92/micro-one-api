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

	"github.com/bytedance/sonic"

	relaybiz "micro-one-api/internal/relay/biz"
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
	if s.billingClient == nil {
		s.writeError(w, http.StatusServiceUnavailable, "billing service unavailable")
		return
	}

	token, err := bearerTokenFromRequest(r)
	if err != nil {
		s.writeError(w, http.StatusUnauthorized, err.Error())
		return
	}

	body, err := io.ReadAll(io.LimitReader(r.Body, 64*1024*1024))
	if err != nil {
		s.writeError(w, http.StatusBadRequest, "failed to read request body")
		return
	}

	var req chatOrchestratorRequest
	if err := sonic.Unmarshal(body, &req); err != nil {
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

	orchestrator := NewRelayOrchestratorWithDependencies(s.relayUsecase, s.providerFactory, httpRelayLifecycleHooks{s: s}, nil)
	result, err := orchestrator.Execute(r.Context(), &RelayRequest{
		Token:     token,
		Model:     req.Model,
		Endpoint:  EndpointChatCompletions,
		Body:      bytes.NewReader(body),
		IsStream:  req.Stream,
		Headers:   r.Header.Clone(),
		RequestID: generateRequestID(),
	})
	if err != nil {
		status := http.StatusInternalServerError
		if result != nil && result.StatusCode != 0 {
			status = result.StatusCode
		}
		s.writeError(w, status, err.Error())
		return
	}
	writeOrchestratedRelayResult(w, result)
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

func (h httpRelayLifecycleHooks) ReserveQuota(ctx context.Context, plan *relaybiz.RelayPlan, req *RelayRequest, estimated Usage) (*Reservation, error) {
	if h.s == nil || h.s.billingClient == nil {
		return nil, errors.New("billing service unavailable")
	}
	reservation, err := h.s.reserveQuota(ctx,
		strconv.FormatInt(plan.Auth.UserID, 10),
		req.RequestID,
		estimated.TotalTokens,
		plan.ResolvedModel,
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

func (h httpRelayLifecycleHooks) CommitQuota(ctx context.Context, plan *relaybiz.RelayPlan, req *RelayRequest, reservation *Reservation, usage Usage, success bool, latency time.Duration) error {
	if reservation == nil {
		return nil
	}
	if h.s == nil || h.s.billingClient == nil {
		return errors.New("billing service unavailable")
	}
	if usage.TotalTokens == 0 {
		usage.TotalTokens = usage.PromptTokens + usage.CompletionTokens
	}
	logInput := orchestratorUsageLogInput(plan, req, usage, latency, req.IsStream)
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

func (h httpRelayLifecycleHooks) LogUsage(ctx context.Context, plan *relaybiz.RelayPlan, req *RelayRequest, usage Usage, latency time.Duration, stream bool) {
	if h.s == nil {
		return
	}
	logInput := orchestratorUsageLogInput(plan, req, usage, latency, stream)
	logUpstreamUsage(logInput)
	h.s.ingestUsageLog(ctx, logInput)
}

func orchestratorUsageLogInput(plan *relaybiz.RelayPlan, req *RelayRequest, usage Usage, latency time.Duration, stream bool) usageLogInput {
	return usageLogInput{
		UserID:                plan.Auth.UserID,
		TokenID:               plan.Auth.TokenID,
		TokenName:             plan.Auth.TokenName,
		RequestID:             req.RequestID,
		Endpoint:              "/v1/chat/completions",
		ModelName:             req.Model,
		Quota:                 usage.TotalTokens,
		PromptTokens:          usage.PromptTokens,
		CompletionTokens:      usage.CompletionTokens,
		ChannelID:             plan.Channel.ID,
		SubscriptionAccountID: subscriptionAccountIDFromPlan(plan),
		ElapsedTime:           latency.Milliseconds(),
		IsStream:              stream,
	}
}

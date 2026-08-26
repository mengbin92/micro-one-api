package server

import (
	"context"
	"net/http"
	"time"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"micro-one-api/internal/apicompat"
	relaybiz "micro-one-api/internal/biz"
	"micro-one-api/pkg/errors"
	"micro-one-api/pkg/jsonx"
)

// handleAnthropicMessages implements POST /v1/messages. Routing, retries and
// quota ownership stay in the server; each channel attempt delegates protocol
// conversion and the upstream request to the unified adaptor layer.
func (s *HTTPServer) handleAnthropicMessages(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		s.writeAnthropicError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}

	token := extractAPIKey(r)
	if token == "" {
		s.writeAnthropicError(w, http.StatusUnauthorized, "missing API key")
		return
	}

	// Preserve the original body for session stickiness and native Anthropic
	// passthrough while using the canonical apicompat DTO for validation.
	originalBody, err := readRouteRequestBody(r)
	if err != nil {
		s.writeRequestBodyError(w, r, err)
		return
	}
	var anthropicReq apicompat.AnthropicRequest
	if err := jsonx.Unmarshal(originalBody, &anthropicReq); err != nil {
		s.writeAnthropicError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	setRelayObservationStream(r.Context(), anthropicReq.Stream)
	if anthropicReq.Model == "" {
		s.writeAnthropicError(w, http.StatusBadRequest, "model is required")
		return
	}
	if len(anthropicReq.Messages) == 0 {
		s.writeAnthropicError(w, http.StatusBadRequest, "messages is required")
		return
	}

	sessionHash := ""
	if s.subscriptionSessionStickyEnabled {
		sessionHash = extractSessionHashFromRequest(r, originalBody)
	}
	plan, err := s.relayUsecase.Plan(r.Context(), relaybiz.RelayRequest{
		Token: token, Model: anthropicReq.Model, SessionHash: sessionHash,
	})
	if err != nil {
		s.handleAnthropicPlanError(w, err)
		return
	}
	if err := s.checkUserRPM(r.Context(), plan.Auth.UserID); err != nil {
		w.Header().Set("Retry-After", "60")
		s.writeAnthropicError(w, http.StatusTooManyRequests, "user rpm limit exceeded")
		return
	}

	if s.hybridAdaptorEnabled && plan.Channel != nil && isSubscriptionChannel(plan.Channel.Type) {
		s.handleAnthropicMessagesViaAdaptor(w, r, plan, anthropicReq.Model, originalBody, sessionHash)
		return
	}

	clientModel := anthropicReq.Model
	retryStartedAt := time.Now()
	retryExecutor := s.relayUsecase.NewRetryExecutor()
	result := retryExecutor.ExecuteWithCandidates(r.Context(), plan, subscriptionAccountIDFromPlan(plan), func(ctx context.Context, channel *relaybiz.Channel) error {
		resolvedModel := relaybiz.ResolveChannelModel(channel, plan.BaseModel())
		return s.executeAnthropicChannelAttempt(
			ctx, w, r, plan, channel, &anthropicReq, originalBody,
			clientModel, resolvedModel,
		)
	})

	s.finalizeSelectionFromResult(plan, result, time.Since(retryStartedAt))
	recordRelayRetryOutcome(r.Context(), result.Fallback, result.Err, result.FallbackReason)
	if result.Err != nil {
		s.writeAnthropicError(w, mapUpstreamError(relaybiz.UpstreamStatus(result.Err)), "upstream service error")
	}
}

// handleAnthropicPlanError maps routing/auth failures to Anthropic's error
// envelope so Anthropic SDKs can parse failures consistently.
func (s *HTTPServer) handleAnthropicPlanError(w http.ResponseWriter, err error) {
	if errors.IsUnauthorized(err) {
		s.writeAnthropicError(w, http.StatusUnauthorized, "authentication_error: invalid API key")
		return
	}
	if errors.IsForbidden(err) {
		s.writeAnthropicError(w, http.StatusForbidden, "permission_error: forbidden")
		return
	}
	if errors.IsServiceUnavailable(err) {
		s.writeAnthropicError(w, http.StatusServiceUnavailable, "api_error: no available channel")
		return
	}

	st, ok := status.FromError(err)
	if ok {
		if isChannelUnavailableMessage(st.Message()) {
			s.writeAnthropicError(w, http.StatusServiceUnavailable, "api_error: no available channel")
			return
		}
		switch st.Code() {
		case codes.Unauthenticated, codes.NotFound:
			s.writeAnthropicError(w, http.StatusUnauthorized, "authentication_error: invalid API key")
		case codes.PermissionDenied:
			s.writeAnthropicError(w, http.StatusForbidden, "permission_error: forbidden")
		case codes.ResourceExhausted:
			s.writeAnthropicError(w, http.StatusTooManyRequests, "rate_limit_error: rate limit exceeded")
		case codes.FailedPrecondition:
			s.writeAnthropicError(w, http.StatusServiceUnavailable, "api_error: no available channel")
		case codes.Unavailable:
			s.writeAnthropicError(w, http.StatusServiceUnavailable, "api_error: service unavailable")
		default:
			s.writeAnthropicError(w, http.StatusInternalServerError, "api_error: internal server error")
		}
		return
	}

	if isChannelUnavailableMessage(err.Error()) {
		s.writeAnthropicError(w, http.StatusServiceUnavailable, "api_error: no available channel")
		return
	}
	s.writeAnthropicError(w, http.StatusInternalServerError, "api_error: internal server error")
}

func (s *HTTPServer) writeAnthropicError(w http.ResponseWriter, statusCode int, message string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(statusCode)
	_ = encodeJSON(w, map[string]interface{}{
		"type":  "error",
		"error": map[string]interface{}{"type": anthropicErrorType(statusCode), "message": message},
	})
}

func anthropicErrorType(statusCode int) string {
	switch statusCode {
	case http.StatusUnauthorized:
		return "authentication_error"
	case http.StatusBadRequest, http.StatusPaymentRequired:
		return "invalid_request_error"
	case http.StatusTooManyRequests:
		return "rate_limit_error"
	default:
		if statusCode >= 500 {
			return "api_error"
		}
		return "invalid_request_error"
	}
}

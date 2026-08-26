package server

import (
	"bytes"
	"io"
	"net/http"

	"micro-one-api/internal/apicompat"
	relaybiz "micro-one-api/internal/biz"
	"micro-one-api/pkg/jsonx"
)

func (s *HTTPServer) newStagedRelayExecutor() relaybiz.Executor {
	return NewRelayExecutorWithForwarder(
		s.relayUsecase,
		s.providerFactory,
		httpRelayLifecycleHooks{s: s},
		newRelayAdaptorForwarder(s.providerFactory, s.accountResolver, s.apiKeyHTTPClient, s.apiKeyStreamHTTPClient, s.oauthHTTPClient),
		nil,
	)
}

func (s *HTTPServer) handleResponsesWithOrchestrator(w http.ResponseWriter, r *http.Request) {
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
	stream := isRawStreamRequest(body)
	setRelayObservationStream(r.Context(), stream)
	model := extractRawModel(body)
	if model == "" {
		// Stateful resource continuation needs the stored response route owned by
		// the legacy Responses handler. Preserve that behavior during migration.
		setRelayObservationPath(r.Context(), relayExecutionPathLegacy)
		r.Body = io.NopCloser(bytes.NewReader(body))
		s.handleResponsesRelay(w, r)
		return
	}
	if s.billingClient == nil {
		s.writeError(w, http.StatusServiceUnavailable, "billing service unavailable")
		return
	}
	result, err := s.newStagedRelayExecutor().Execute(r.Context(), relaybiz.ExecutorRequest{
		Token:       token,
		Model:       model,
		Endpoint:    string(EndpointResponses),
		Body:        body,
		Headers:     relayExecutorHeaders(r.Header),
		RequestID:   generateRequestID(),
		SessionHash: extractSessionHashFromRequest(r, body),
		Stream:      stream,
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

func (s *HTTPServer) handleAnthropicMessagesWithOrchestrator(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		s.writeAnthropicError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	token := extractAPIKey(r)
	if token == "" {
		s.writeAnthropicError(w, http.StatusUnauthorized, "missing API key")
		return
	}
	body, err := readRouteRequestBody(r)
	if err != nil {
		s.writeRequestBodyError(w, r, err)
		return
	}
	var request apicompat.AnthropicRequest
	if err := jsonx.Unmarshal(body, &request); err != nil {
		s.writeAnthropicError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	setRelayObservationStream(r.Context(), request.Stream)
	if request.Model == "" {
		s.writeAnthropicError(w, http.StatusBadRequest, "model is required")
		return
	}
	if len(request.Messages) == 0 {
		s.writeAnthropicError(w, http.StatusBadRequest, "messages is required")
		return
	}
	if s.billingClient == nil {
		s.writeAnthropicError(w, http.StatusServiceUnavailable, "billing service unavailable")
		return
	}
	result, err := s.newStagedRelayExecutor().Execute(r.Context(), relaybiz.ExecutorRequest{
		Token:       token,
		Model:       request.Model,
		Endpoint:    string(EndpointAnthropicMessages),
		Body:        body,
		Headers:     relayExecutorHeaders(r.Header),
		RequestID:   generateRequestID(),
		SessionHash: extractSessionHashFromRequest(r, body),
		Stream:      request.Stream,
	})
	if err != nil {
		status := http.StatusInternalServerError
		if result.StatusCode != 0 {
			status = result.StatusCode
		}
		s.writeAnthropicError(w, status, orchestratorErrorMessage(status, err))
		return
	}
	if result.Stream != nil {
		writeOrchestratedRelayStream(w, result)
		return
	}
	writeOrchestratedRelayResult(w, relayResultFromExecutionResponse(result))
}

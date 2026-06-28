package handler

import (
	"io"
	"net/http"

	"github.com/bytedance/sonic"

	"micro-one-api/internal/relay/server"
)

// CompletionsHandler handles /v1/completions requests.
type CompletionsHandler struct {
	orchestrator server.Orchestrator
}

// NewCompletionsHandler creates a new completions handler.
func NewCompletionsHandler(orchestrator server.Orchestrator) *CompletionsHandler {
	return &CompletionsHandler{
		orchestrator: orchestrator,
	}
}

// ServeHTTP handles the HTTP request for completions.
func (h *CompletionsHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		h.writeError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}

	token, err := extractBearerToken(r)
	if err != nil {
		h.writeError(w, http.StatusUnauthorized, err.Error())
		return
	}

	body, err := io.ReadAll(io.LimitReader(r.Body, 64*1024*1024))
	if err != nil {
		h.writeError(w, http.StatusBadRequest, "failed to read request body")
		return
	}

	var req struct {
		Model    string   `json:"model"`
		Prompt   any      `json:"prompt"`
		MaxTokens *int    `json:"max_tokens,omitempty"`
		Stream   bool     `json:"stream,omitempty"`
	}
	if err := sonic.Unmarshal(body, &req); err != nil {
		h.writeError(w, http.StatusBadRequest, "invalid request body: "+err.Error())
		return
	}

	relayReq := &server.RelayRequest{
		Token:    token,
		Model:    req.Model,
		Endpoint: server.EndpointCompletions,
		Body:     nil,
		IsStream: req.Stream,
		Headers:  r.Header,
	}

	result, err := h.orchestrator.Execute(r.Context(), relayReq)
	if err != nil {
		h.writeError(w, result.StatusCode, err.Error())
		return
	}

	// TODO: Forward response to client
}

func (h *CompletionsHandler) writeError(w http.ResponseWriter, status int, message string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	resp := map[string]any{
		"error": map[string]any{
			"message": message,
			"type":    "invalid_request_error",
			"code":    status,
		},
	}
	data, _ := sonic.Marshal(resp)
	w.Write(data)
}

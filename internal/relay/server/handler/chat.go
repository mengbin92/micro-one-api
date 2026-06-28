package handler

import (
	"encoding/json"
	"io"
	"net/http"

	"github.com/bytedance/sonic"

	"micro-one-api/internal/relay/server"
)

// ChatHandler handles /v1/chat/completions requests.
type ChatHandler struct {
	orchestrator server.Orchestrator
}

// NewChatHandler creates a new chat handler.
func NewChatHandler(orchestrator server.Orchestrator) *ChatHandler {
	return &ChatHandler{
		orchestrator: orchestrator,
	}
}

// ChatCompletionsRequest represents the OpenAI Chat Completions API request.
type ChatCompletionsRequest struct {
	Model            string          `json:"model"`
	Messages         []ChatMessage  `json:"messages"`
	MaxTokens        *int            `json:"max_tokens,omitempty"`
	Temperature      *float64        `json:"temperature,omitempty"`
	TopP             *float64        `json:"top_p,omitempty"`
	N                *int            `json:"n,omitempty"`
	Stream           bool            `json:"stream,omitempty"`
	Stop             any             `json:"stop,omitempty"`
	PresencePenalty  *float64        `json:"presence_penalty,omitempty"`
	FrequencyPenalty *float64        `json:"frequency_penalty,omitempty"`
	LogitBias        any             `json:"logit_bias,omitempty"`
	User             string          `json:"user,omitempty"`
	Functions        any             `json:"functions,omitempty"`
	FunctionCall     any             `json:"function_call,omitempty"`
	Tools            any             `json:"tools,omitempty"`
	ToolChoice       any             `json:"tool_choice,omitempty"`
	ResponseFormat   any             `json:"response_format,omitempty"`
	Seed             *int64          `json:"seed,omitempty"`
}

// ChatMessage represents a message in the chat conversation.
type ChatMessage struct {
	Role         string         `json:"role"`
	Content      any            `json:"content"`
	Name         string         `json:"name,omitempty"`
	FunctionCall any            `json:"function_call,omitempty"`
	ToolCalls    any            `json:"tool_calls,omitempty"`
	ToolCallID   string         `json:"tool_call_id,omitempty"`
}

// ServeHTTP handles the HTTP request for chat completions.
func (h *ChatHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	// Validate method
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	// Extract and validate authorization
	token, err := extractBearerToken(r)
	if err != nil {
		h.writeError(w, http.StatusUnauthorized, err.Error())
		return
	}

	// Read request body
	body, err := io.ReadAll(io.LimitReader(r.Body, 64*1024*1024)) // Limit to 64MB
	if err != nil {
		h.writeError(w, http.StatusBadRequest, "failed to read request body")
		return
	}

	// Parse request
	var req ChatCompletionsRequest
	if err := sonic.Unmarshal(body, &req); err != nil {
		h.writeError(w, http.StatusBadRequest, "invalid request body: "+err.Error())
		return
	}

	// Validate required fields
	if req.Model == "" {
		h.writeError(w, http.StatusBadRequest, "model is required")
		return
	}
	if len(req.Messages) == 0 {
		h.writeError(w, http.StatusBadRequest, "messages are required")
		return
	}

	// Create orchestration request
	relayReq := &server.RelayRequest{
		Token:    token,
		Model:    req.Model,
		Endpoint: server.EndpointChatCompletions,
		Body:     nil, // Will be set by forwarder
		IsStream: req.Stream,
		Headers:  r.Header,
	}

	// Execute orchestration
	result, err := h.orchestrator.Execute(r.Context(), relayReq)
	if err != nil {
		// Handle error - write appropriate response
		h.writeError(w, result.StatusCode, err.Error())
		return
	}

	// TODO: Forward response to client
	// This will be implemented by the forwarder
}

// extractBearerToken extracts the Bearer token from the Authorization header.
func extractBearerToken(r *http.Request) (string, error) {
	authHeader := r.Header.Get("Authorization")
	if authHeader == "" {
		return "", http.ErrNotSupported
	}
	if len(authHeader) < 7 || authHeader[:7] != "Bearer " {
		return "", http.ErrNotSupported
	}
	token := authHeader[7:]
	if token == "" {
		return "", http.ErrNotSupported
	}
	return token, nil
}

// writeError writes an error response in OpenAI format.
func (h *ChatHandler) writeError(w http.ResponseWriter, status int, message string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	resp := map[string]any{
		"error": map[string]any{
			"message": message,
			"type":    "invalid_request_error",
			"code":    status,
		},
	}
	_ = json.NewEncoder(w).Encode(resp)
}

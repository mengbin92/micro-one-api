package server

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/bytedance/sonic"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	billingv1 "micro-one-api/api/billing/v1"
	"micro-one-api/internal/pkg/errors"
	relaybiz "micro-one-api/internal/relay/biz"
	relayprovider "micro-one-api/internal/relay/provider"
)

// ----------------------------------------------------------------------------
// Anthropic Messages API inbound types
// ----------------------------------------------------------------------------

// anthropicInboundRequest represents an Anthropic Messages API request body.
type anthropicInboundRequest struct {
	Model         string                    `json:"model"`
	Messages      []anthropicInboundMessage `json:"messages"`
	System        json.RawMessage           `json:"system,omitempty"`
	MaxTokens     int                       `json:"max_tokens"`
	Stream        bool                      `json:"stream,omitempty"`
	Temperature   *float64                  `json:"temperature,omitempty"`
	TopP          *float64                  `json:"top_p,omitempty"`
	TopK          *int                      `json:"top_k,omitempty"`
	Tools         []map[string]interface{}  `json:"tools,omitempty"`
	ToolChoice    json.RawMessage           `json:"tool_choice,omitempty"`
	StopSequences []string                  `json:"stop_sequences,omitempty"`
}

// anthropicInboundMessage is a single message; content may be a string or an
// array of content blocks (text / tool_use / tool_result / image).
type anthropicInboundMessage struct {
	Role    string          `json:"role"`
	Content json.RawMessage `json:"content"`
}

// anthropicMessagesResponse is the non-streaming Anthropic Messages response.
type anthropicMessagesResponse struct {
	ID           string                 `json:"id"`
	Type         string                 `json:"type"`
	Role         string                 `json:"role"`
	Content      []anthropicRespContent `json:"content"`
	Model        string                 `json:"model"`
	StopReason   *string                `json:"stop_reason"`
	StopSequence *string                `json:"stop_sequence,omitempty"`
	Usage        anthropicRespUsage     `json:"usage"`
}

type anthropicRespContent struct {
	Type     string `json:"type"`
	Text     string `json:"text,omitempty"`
	Thinking string `json:"thinking,omitempty"`
	ID       string `json:"id,omitempty"`
	Name     string `json:"name,omitempty"`
	Input    any    `json:"input,omitempty"`
}

type anthropicRespUsage struct {
	InputTokens  int `json:"input_tokens"`
	OutputTokens int `json:"output_tokens"`
}

// ----------------------------------------------------------------------------
// Auth
// ----------------------------------------------------------------------------

// extractTokenFromAnthropicRequest resolves the access token from either the
// Anthropic-style x-api-key header or a standard Authorization: Bearer header.
func extractTokenFromAnthropicRequest(r *http.Request) string {
	if key := strings.TrimSpace(r.Header.Get("x-api-key")); key != "" {
		return key
	}
	auth := r.Header.Get("Authorization")
	if strings.HasPrefix(auth, "Bearer ") {
		return strings.TrimSpace(strings.TrimPrefix(auth, "Bearer "))
	}
	return ""
}

// ----------------------------------------------------------------------------
// Request conversion: Anthropic Messages → internal ChatCompletionsRequest
// ----------------------------------------------------------------------------

func convertAnthropicToChatCompletions(req *anthropicInboundRequest) (*relayprovider.ChatCompletionsRequest, error) {
	ccReq := &relayprovider.ChatCompletionsRequest{
		Model:       req.Model,
		Stream:      req.Stream,
		Temperature: req.Temperature,
		Tools:       convertAnthropicToolsToOpenAI(req.Tools),
	}

	const anthropicMaxOutputLimit = 64000
	if req.MaxTokens > 0 {
		mt := req.MaxTokens
		if mt > anthropicMaxOutputLimit {
			mt = anthropicMaxOutputLimit
		}
		ccReq.MaxTokens = &mt
	} else {
		mt := 4096
		ccReq.MaxTokens = &mt
	}

	ccReq.ToolChoice = convertAnthropicToolChoiceToOpenAI(req.ToolChoice)

	// Convert system prompt (string or array of content blocks).
	if systemText := extractSystemText(req.System); systemText != "" {
		ccReq.Messages = append(ccReq.Messages, relayprovider.Message{
			Role:    "system",
			Content: systemText,
		})
	}

	// Convert messages.
	for _, msg := range req.Messages {
		converted, err := convertAnthropicMessage(msg)
		if err != nil {
			return nil, err
		}
		ccReq.Messages = append(ccReq.Messages, converted...)
	}

	return ccReq, nil
}

// extractSystemText handles both string and array-of-blocks forms of the
// Anthropic top-level "system" parameter.
func extractSystemText(raw json.RawMessage) string {
	if len(raw) == 0 {
		return ""
	}
	// Try string first.
	var s string
	if err := sonic.Unmarshal(raw, &s); err == nil {
		return s
	}
	// Try array of content blocks.
	var blocks []map[string]interface{}
	if err := sonic.Unmarshal(raw, &blocks); err == nil {
		var b strings.Builder
		for _, blk := range blocks {
			if t, ok := blk["type"].(string); ok && t == "text" {
				if txt, ok := blk["text"].(string); ok {
					b.WriteString(txt)
				}
			}
		}
		return b.String()
	}
	return ""
}

// convertAnthropicMessage converts a single Anthropic message (whose content
// may be a plain string or an array of content blocks) into one or more
// internal OpenAI-format messages.
func convertAnthropicMessage(msg anthropicInboundMessage) ([]relayprovider.Message, error) {
	// Plain string content — direct mapping.
	var plain string
	if err := sonic.Unmarshal(msg.Content, &plain); err == nil {
		return []relayprovider.Message{{Role: msg.Role, Content: plain}}, nil
	}

	// Array of content blocks.
	var blocks []map[string]interface{}
	if err := sonic.Unmarshal(msg.Content, &blocks); err != nil {
		// Fall back to raw string representation.
		return []relayprovider.Message{{Role: msg.Role, Content: string(msg.Content)}}, nil
	}

	// Separate text content from tool_result blocks.
	var textParts []string
	var toolCalls []relayprovider.ToolCall
	var toolResults []relayprovider.Message

	for _, blk := range blocks {
		blkType, _ := blk["type"].(string)
		switch blkType {
		case "text":
			if txt, ok := blk["text"].(string); ok {
				textParts = append(textParts, txt)
			}
		case "tool_use":
			id, _ := blk["id"].(string)
			name, _ := blk["name"].(string)
			toolCalls = append(toolCalls, relayprovider.ToolCall{
				ID:   id,
				Type: "function",
				Function: relayprovider.ToolCallFunction{
					Name:      name,
					Arguments: marshalJSONString(blk["input"]),
				},
			})
		case "tool_result":
			// Anthropic puts tool results inside user messages as content
			// blocks. OpenAI represents them as separate "tool" role messages.
			toolUseID, _ := blk["tool_use_id"].(string)
			resultContent := extractToolResultContent(blk)
			toolResults = append(toolResults, relayprovider.Message{
				Role:       "tool",
				Content:    resultContent,
				ToolCallID: toolUseID,
			})
		}
	}

	role := msg.Role
	if len(toolCalls) > 0 {
		// Assistant message with tool calls — OpenAI expects the text content
		// alongside tool_calls in the same message.
		return []relayprovider.Message{{
			Role:      role,
			Content:   strings.Join(textParts, ""),
			ToolCalls: toolCalls,
		}}, nil
	}

	if len(toolResults) > 0 {
		// Tool results are emitted as separate messages. If there is also text
		// content, prepend it as a user message.
		var result []relayprovider.Message
		if len(textParts) > 0 {
			result = append(result, relayprovider.Message{Role: role, Content: strings.Join(textParts, "")})
		}
		result = append(result, toolResults...)
		return result, nil
	}

	return []relayprovider.Message{{Role: role, Content: strings.Join(textParts, "")}}, nil
}

func extractToolResultContent(blk map[string]interface{}) string {
	if content, ok := blk["content"]; ok {
		switch v := content.(type) {
		case string:
			return v
		case []interface{}:
			var parts []string
			for _, item := range v {
				if m, ok := item.(map[string]interface{}); ok {
					if t, ok := m["type"].(string); ok && t == "text" {
						if txt, ok := m["text"].(string); ok {
							parts = append(parts, txt)
						}
					}
				}
			}
			return strings.Join(parts, "")
		}
	}
	return ""
}

// convertAnthropicToolsToOpenAI converts Anthropic tool definitions to OpenAI
// function-calling format.
func convertAnthropicToolsToOpenAI(tools []map[string]interface{}) []map[string]interface{} {
	if len(tools) == 0 {
		return nil
	}
	result := make([]map[string]interface{}, 0, len(tools))
	for _, tool := range tools {
		name, _ := tool["name"].(string)
		desc, _ := tool["description"].(string)
		schema := tool["input_schema"]
		if schema == nil {
			schema = map[string]interface{}{"type": "object", "properties": map[string]interface{}{}}
		}
		result = append(result, map[string]interface{}{
			"type": "function",
			"function": map[string]interface{}{
				"name":        name,
				"description": desc,
				"parameters":  schema,
			},
		})
	}
	return result
}

func convertAnthropicToolChoiceToOpenAI(raw json.RawMessage) any {
	if len(raw) == 0 {
		return nil
	}
	var choice map[string]interface{}
	if err := sonic.Unmarshal(raw, &choice); err != nil {
		return nil
	}
	choiceType, _ := choice["type"].(string)
	switch choiceType {
	case "auto":
		return "auto"
	case "any":
		return "required"
	case "tool":
		name, _ := choice["name"].(string)
		return map[string]interface{}{
			"type": "function",
			"function": map[string]interface{}{
				"name": name,
			},
		}
	default:
		return nil
	}
}

// ----------------------------------------------------------------------------
// Response conversion: internal ChatCompletionsResponse → Anthropic Messages
// ----------------------------------------------------------------------------

func convertChatCompletionsToAnthropic(resp *relayprovider.ChatCompletionsResponse, model string) *anthropicMessagesResponse {
	var contents []anthropicRespContent

	if len(resp.Choices) > 0 {
		choice := resp.Choices[0]
		// Thinking mode (e.g. DeepSeek-R1, GLM-5.x): reasoning_content comes
		// before the final text answer, mirroring Anthropic's "thinking" block.
		if reasoning := reasoningContentString(choice.Message.ReasoningContent); reasoning != "" {
			contents = append(contents, anthropicRespContent{
				Type:     "thinking",
				Thinking: reasoning,
			})
		}
		if choice.Message.Content != "" {
			contents = append(contents, anthropicRespContent{
				Type: "text",
				Text: choice.Message.Content,
			})
		}
		for _, tc := range choice.Message.ToolCalls {
			input := parseJSONToAny(tc.Function.Arguments)
			contents = append(contents, anthropicRespContent{
				Type:  "tool_use",
				ID:    tc.ID,
				Name:  tc.Function.Name,
				Input: input,
			})
		}
	}

	if len(contents) == 0 {
		contents = []anthropicRespContent{{Type: "text", Text: ""}}
	}

	stopReason := mapFinishReasonToAnthropic(resp.Choices)
	return &anthropicMessagesResponse{
		ID:         resp.ID,
		Type:       "message",
		Role:       "assistant",
		Content:    contents,
		Model:      model,
		StopReason: &stopReason,
		Usage: anthropicRespUsage{
			InputTokens:  resp.Usage.PromptTokens,
			OutputTokens: resp.Usage.CompletionTokens,
		},
	}
}

func mapFinishReasonToAnthropic(choices []relayprovider.Choice) string {
	if len(choices) == 0 {
		return "end_turn"
	}
	switch choices[0].FinishReason {
	case "stop":
		return "end_turn"
	case "length":
		return "max_tokens"
	case "tool_calls", "function_call":
		return "tool_use"
	default:
		return "end_turn"
	}
}

// ----------------------------------------------------------------------------
// Handler
// ----------------------------------------------------------------------------

// handleAnthropicMessages implements the inbound POST /v1/messages endpoint,
// translating between the Anthropic Messages API and the internal
// OpenAI-compatible relay pipeline (auth → channel selection → billing →
// upstream provider).
func (s *HTTPServer) handleAnthropicMessages(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		s.writeAnthropicError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}

	token := extractTokenFromAnthropicRequest(r)
	if token == "" {
		s.writeAnthropicError(w, http.StatusUnauthorized, "missing API key")
		return
	}

	// Read the original body so session_hash (absent from the typed struct)
	// survives for session stickiness; then decode from those bytes.
	originalBody, err := io.ReadAll(io.LimitReader(r.Body, 10*1024*1024))
	if err != nil {
		s.writeAnthropicError(w, http.StatusBadRequest, "failed to read request body")
		return
	}
	var anthropicReq anthropicInboundRequest
	if err := sonic.Unmarshal(originalBody, &anthropicReq); err != nil {
		s.writeAnthropicError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	if anthropicReq.Model == "" {
		s.writeAnthropicError(w, http.StatusBadRequest, "model is required")
		return
	}
	if len(anthropicReq.Messages) == 0 {
		s.writeAnthropicError(w, http.StatusBadRequest, "messages is required")
		return
	}

	ccReq, err := convertAnthropicToChatCompletions(&anthropicReq)
	if err != nil {
		s.writeAnthropicError(w, http.StatusBadRequest, "failed to convert request: "+err.Error())
		return
	}

	sessionHash := ""
	if s.subscriptionSessionStickyEnabled {
		sessionHash = extractSessionHashFromRequest(r, originalBody)
	}

	plan, err := s.relayUsecase.Plan(r.Context(), relaybiz.RelayRequest{
		Token:       token,
		Model:       anthropicReq.Model,
		SessionHash: sessionHash,
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
		rawBody, _ := sonic.Marshal(anthropicReq)
		s.handleAnthropicMessagesViaAdaptor(w, r, plan, anthropicReq.Model, rawBody, sessionHash)
		return
	}

	clientModel := anthropicReq.Model
	ccReq.Model = plan.ResolvedModel

	retryExecutor := s.relayUsecase.NewRetryExecutor()
	result := retryExecutor.ExecuteWithInitialChannel(r.Context(), plan.Auth.Group, plan.ResolvedModel, plan.Channel, func(ctx context.Context, ch *relaybiz.Channel) error {
		startedAt := time.Now()
		requestID := generateRequestID()
		estimatedTokens := s.estimateTokens(ccReq)
		reservation, reserveErr := s.reserveQuota(ctx, fmt.Sprintf("%d", plan.Auth.UserID), requestID, estimatedTokens, plan.ResolvedModel, fmt.Sprintf("%d", ch.ID), subscriptionAccountIDFromPlan(plan))
		if reserveErr != nil {
			return &relaybiz.RetryableError{Status: http.StatusPaymentRequired, Err: reserveErr}
		}

		provider, provErr := s.providerFactory.CreateProviderWithConfig(ch.Type, ch.BaseURL, ch.Key, relayprovider.ProviderConfig{
			APIVersion: ch.Config.APIVersion,
		})
		if provErr != nil {
			_ = s.releaseQuota(ctx, reservation.ReservationId, "failed to create provider")
			return fmt.Errorf("failed to create provider: %w", provErr)
		}

		if ccReq.Stream {
			return s.handleAnthropicStreamingResponse(w, r, provider, ccReq, reservation, usageLogInput{
				UserID:    plan.Auth.UserID,
				TokenID:   plan.Auth.TokenID,
				TokenName: plan.Auth.TokenName,
				RequestID: requestID,
				Endpoint:  "/v1/messages",
				ModelName: clientModel,
				ChannelID: ch.ID,
				IsStream:  true,
			})
		}

		// Non-streaming.
		resp, callErr := provider.ChatCompletions(ctx, ccReq)
		if callErr != nil {
			_ = s.releaseQuota(ctx, reservation.ReservationId, "upstream error")
			return callErr
		}

		actualTokens := s.calculateActualTokens(resp)
		logInput := usageLogInput{
			UserID:                plan.Auth.UserID,
			TokenID:               plan.Auth.TokenID,
			TokenName:             plan.Auth.TokenName,
			RequestID:             requestID,
			Endpoint:              "/v1/messages",
			ModelName:             clientModel,
			Quota:                 actualTokens,
			PromptTokens:          int64(resp.Usage.PromptTokens),
			CompletionTokens:      int64(resp.Usage.CompletionTokens),
			CacheReadTokens:       cacheReadTokensFromProviderUsage(resp.Usage),
			ChannelID:             ch.ID,
			SubscriptionAccountID: subscriptionAccountIDFromPlan(plan),
			ElapsedTime:           time.Since(startedAt).Milliseconds(),
			IsStream:              false,
		}
		if err := s.commitQuota(ctx, reservation.ReservationId, actualTokens, true, logInput); err != nil {
			return err
		}
		logUpstreamUsage(logInput)
		s.ingestUsageLog(ctx, logInput)

		anthropicResp := convertChatCompletionsToAnthropic(resp, clientModel)
		s.writeJSON(w, http.StatusOK, anthropicResp)
		return nil
	})

	if result.Err != nil {
		s.writeAnthropicError(w, mapUpstreamError(relaybiz.UpstreamStatus(result.Err)), "upstream service error")
	}
}

// handleAnthropicStreamingResponse converts an OpenAI-compatible SSE stream
// into the Anthropic Messages streaming event format:
//
//	event: message_start        — message skeleton with empty usage
//	event: content_block_start  — opens content block index 0 (text)
//	event: content_block_delta  — text_delta with incremental text
//	event: content_block_stop   — closes content block index 0
//	event: message_delta        — stop_reason + final usage
//	event: message_stop         — end of stream
func (s *HTTPServer) handleAnthropicStreamingResponse(
	w http.ResponseWriter,
	r *http.Request,
	provider relayprovider.Provider,
	req *relayprovider.ChatCompletionsRequest,
	reservation *billingv1.ReserveQuotaResponse,
	logInput usageLogInput,
) error {
	startedAt := time.Now()
	chunkChan, err := provider.ChatCompletionsStream(r.Context(), req)
	if err != nil {
		_ = s.releaseQuota(r.Context(), reservation.ReservationId, "upstream stream error")
		return err
	}

	flusher, ok := w.(http.Flusher)
	if !ok {
		_ = s.releaseQuota(r.Context(), reservation.ReservationId, "streaming not supported")
		return fmt.Errorf("streaming not supported")
	}

	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	w.Header().Set("Transfer-Encoding", "chunked")

	msgID := "msg_" + generateRequestID()
	var stopReason string
	totalTokens := int64(0)
	promptTokens := int64(0)
	completionTokens := int64(0)
	cacheReadTokens := int64(0)
	estimatedTokens := int64(0)

	// message_start
	startMsg := map[string]interface{}{
		"type": "message_start",
		"message": map[string]interface{}{
			"id":            msgID,
			"type":          "message",
			"role":          "assistant",
			"content":       []interface{}{},
			"model":         logInput.ModelName,
			"stop_reason":   nil,
			"stop_sequence": nil,
			"usage": map[string]interface{}{
				"input_tokens":  0,
				"output_tokens": 0,
			},
		},
	}
	if e := writeSSEEvent(w, "message_start", startMsg); e != nil {
		return e
	}
	flusher.Flush()

	// content_block_start (text block at index 0)
	blockStart := map[string]interface{}{
		"type":  "content_block_start",
		"index": 0,
		"content_block": map[string]interface{}{
			"type": "text",
			"text": "",
		},
	}
	if e := writeSSEEvent(w, "content_block_start", blockStart); e != nil {
		return e
	}
	flusher.Flush()

	blockOpen := true

	// thinkingIndex tracks whether the thinking content block is currently open.
	thinkingIndex := -1

	for chunk := range chunkChan {
		if chunk.Usage.TotalTokens > 0 {
			totalTokens = int64(chunk.Usage.TotalTokens)
			promptTokens = int64(chunk.Usage.PromptTokens)
			completionTokens = int64(chunk.Usage.CompletionTokens)
			cacheReadTokens = cacheReadTokensFromProviderUsage(chunk.Usage)
		}

		for _, choice := range chunk.Choices {
			// Reasoning content (thinking mode) — emit as a separate thinking
			// content block that opens before the text block and closes once
			// normal text starts arriving.
			if reasoning := reasoningContentString(choice.Delta.ReasoningContent); reasoning != "" {
				estimatedTokens += int64(len(reasoning) / 4)
				if thinkingIndex == -1 {
					// close the text block placeholder, open a thinking block
					if blockOpen {
						writeSSEEvent(w, "content_block_stop", map[string]interface{}{"type": "content_block_stop", "index": 0})
						flusher.Flush()
						blockOpen = false
					}
					thinkingIndex = 1
					writeSSEEvent(w, "content_block_start", map[string]interface{}{
						"type":  "content_block_start",
						"index": thinkingIndex,
						"content_block": map[string]interface{}{
							"type":     "thinking",
							"thinking": "",
						},
					})
					flusher.Flush()
				}
				if e := writeSSEEvent(w, "content_block_delta", map[string]interface{}{
					"type":  "content_block_delta",
					"index": thinkingIndex,
					"delta": map[string]interface{}{
						"type":     "thinking_delta",
						"thinking": reasoning,
					},
				}); e != nil {
					// Headers already written; cannot send HTTP error, best-effort abort.
					break
				}
				flusher.Flush()
			}

			if choice.Delta.Content != "" {
				estimatedTokens += int64(len(choice.Delta.Content) / 4)
				// If we were emitting thinking, close that block and reopen text.
				if thinkingIndex != -1 {
					writeSSEEvent(w, "content_block_stop", map[string]interface{}{"type": "content_block_stop", "index": thinkingIndex})
					flusher.Flush()
					thinkingIndex = -1
					writeSSEEvent(w, "content_block_start", map[string]interface{}{
						"type":  "content_block_start",
						"index": 0,
						"content_block": map[string]interface{}{
							"type": "text",
							"text": "",
						},
					})
					flusher.Flush()
					blockOpen = true
				}
				if !blockOpen {
					writeSSEEvent(w, "content_block_start", map[string]interface{}{
						"type":  "content_block_start",
						"index": 0,
						"content_block": map[string]interface{}{
							"type": "text",
							"text": "",
						},
					})
					flusher.Flush()
					blockOpen = true
				}
				if e := writeSSEEvent(w, "content_block_delta", map[string]interface{}{
					"type":  "content_block_delta",
					"index": 0,
					"delta": map[string]interface{}{
						"type": "text_delta",
						"text": choice.Delta.Content,
					},
				}); e != nil {
					break
				}
				flusher.Flush()
			}
			if choice.FinishReason != nil {
				stopReason = mapFinishReasonStringToAnthropic(*choice.FinishReason)
			}
		}
	}

	// If the stream ended while a thinking block was still open, close it.
	if thinkingIndex != -1 {
		writeSSEEvent(w, "content_block_stop", map[string]interface{}{"type": "content_block_stop", "index": thinkingIndex})
		flusher.Flush()
		thinkingIndex = -1
	}

	if stopReason == "" {
		stopReason = "end_turn"
	}

	// content_block_stop
	if blockOpen {
		blockStop := map[string]interface{}{
			"type":  "content_block_stop",
			"index": 0,
		}
		if e := writeSSEEvent(w, "content_block_stop", blockStop); e != nil {
			return e
		}
		flusher.Flush()
	}

	if totalTokens == 0 {
		totalTokens = estimatedTokens
		completionTokens = estimatedTokens
	}

	// message_delta
	deltaMsg := map[string]interface{}{
		"type": "message_delta",
		"delta": map[string]interface{}{
			"stop_reason":   stopReason,
			"stop_sequence": nil,
		},
		"usage": map[string]interface{}{
			"input_tokens":  promptTokens,
			"output_tokens": completionTokens,
		},
	}
	if e := writeSSEEvent(w, "message_delta", deltaMsg); e != nil {
		return e
	}
	flusher.Flush()

	// message_stop
	if e := writeSSEEvent(w, "message_stop", map[string]interface{}{"type": "message_stop"}); e != nil {
		return e
	}
	flusher.Flush()

	// Commit quota.
	logInput.Quota = totalTokens
	logInput.PromptTokens = promptTokens
	logInput.CompletionTokens = completionTokens
	logInput.CacheReadTokens = cacheReadTokens
	logInput.ElapsedTime = time.Since(startedAt).Milliseconds()
	if logInput.Endpoint == "" {
		logInput.Endpoint = "/v1/messages"
	}
	if err := s.commitQuotaAfterResponse(reservation.ReservationId, totalTokens, true, logInput); err != nil {
		s.logPostResponseCommitError(err)
	} else {
		logUpstreamUsage(logInput)
		s.ingestUsageLogAfterResponse(logInput)
	}

	return nil
}

func mapFinishReasonStringToAnthropic(reason string) string {
	switch reason {
	case "stop":
		return "end_turn"
	case "length":
		return "max_tokens"
	case "tool_calls", "function_call":
		return "tool_use"
	default:
		return "end_turn"
	}
}

// ----------------------------------------------------------------------------
// Anthropic error helper
// ----------------------------------------------------------------------------

// handleAnthropicPlanError maps biz-layer Plan() errors to Anthropic-format
// error responses, mirroring the OpenAI-style handleRelayPlanError but emitting
// the Anthropic error envelope so SDK clients can parse it correctly.
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
		switch st.Code() {
		case codes.NotFound:
			s.writeAnthropicError(w, http.StatusUnauthorized, "authentication_error: invalid API key")
		case codes.PermissionDenied:
			s.writeAnthropicError(w, http.StatusForbidden, "permission_error: forbidden")
		case codes.ResourceExhausted:
			s.writeAnthropicError(w, http.StatusTooManyRequests, "rate_limit_error: rate limit exceeded")
		case codes.Unavailable:
			s.writeAnthropicError(w, http.StatusServiceUnavailable, "api_error: service unavailable")
		default:
			if strings.Contains(st.Message(), "no available channel") || strings.Contains(st.Message(), "channel not found") {
				s.writeAnthropicError(w, http.StatusServiceUnavailable, "api_error: no available channel")
				return
			}
			s.writeAnthropicError(w, http.StatusInternalServerError, "api_error: internal server error")
		}
		return
	}

	if strings.Contains(err.Error(), "no available channel") || strings.Contains(err.Error(), "channel not found") {
		s.writeAnthropicError(w, http.StatusServiceUnavailable, "api_error: no available channel")
		return
	}

	// Model not allowed
	if strings.Contains(err.Error(), "not allowed") {
		s.writeAnthropicError(w, http.StatusForbidden, "permission_error: model not allowed")
		return
	}

	s.writeAnthropicError(w, http.StatusInternalServerError, "api_error: internal server error")
}

func (s *HTTPServer) writeAnthropicError(w http.ResponseWriter, statusCode int, message string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(statusCode)
	encodeJSON(w, map[string]interface{}{
		"type":  "error",
		"error": map[string]interface{}{"type": anthropicErrorType(statusCode), "message": message},
	})
}

func anthropicErrorType(statusCode int) string {
	switch statusCode {
	case http.StatusUnauthorized:
		return "authentication_error"
	case http.StatusBadRequest:
		return "invalid_request_error"
	case http.StatusTooManyRequests:
		return "rate_limit_error"
	case http.StatusPaymentRequired:
		return "invalid_request_error"
	default:
		if statusCode >= 500 {
			return "api_error"
		}
		return "invalid_request_error"
	}
}

// ----------------------------------------------------------------------------
// JSON helpers
// ----------------------------------------------------------------------------

func marshalJSONString(v interface{}) string {
	data, err := sonic.Marshal(v)
	if err != nil {
		return "{}"
	}
	return string(data)
}

func parseJSONToAny(s string) interface{} {
	var v interface{}
	if err := sonic.Unmarshal([]byte(s), &v); err != nil {
		return map[string]interface{}{}
	}
	return v
}

// reasoningContentString extracts a string value from the reasoning_content
// field. Upstream providers use various formats: a plain string, or a JSON
// object/array with a "content"/"text" key.
func reasoningContentString(v interface{}) string {
	if v == nil {
		return ""
	}
	switch val := v.(type) {
	case string:
		return val
	default:
		return marshalJSONString(val)
	}
}

// writeSSEEvent writes a single Anthropic SSE event with an optional type.
func writeSSEEvent(w http.ResponseWriter, eventType string, data interface{}) error {
	jsonData, err := sonic.Marshal(data)
	if err != nil {
		return err
	}
	if eventType != "" {
		fmt.Fprintf(w, "event: %s\n", eventType)
	}
	fmt.Fprintf(w, "data: %s\n\n", string(jsonData))
	return nil
}

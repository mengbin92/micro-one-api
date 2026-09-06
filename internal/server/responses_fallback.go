package server

import (
	"bufio"
	"context"
	stderrors "errors"
	"fmt"
	"io"
	"net/http"
	"strings"

	"micro-one-api/pkg/jsonx"
	applogger "micro-one-api/platform/logging"

	relayprovider "micro-one-api/domain/upstream/provider"
	"micro-one-api/internal/apicompat"
	relaybiz "micro-one-api/internal/biz"
	usagepkg "micro-one-api/internal/server/usage"

	"go.uber.org/zap"
)

type responsesFallbackResult struct {
	Response *relayprovider.RawResponse
	Stream   *relayprovider.RawStreamResponse
	Usage    rawUsage
}

func shouldFallbackResponsesToChat(path string, body []byte, err error) bool {
	if path != "/responses" || err == nil {
		return false
	}
	var upstreamErr *relayprovider.UpstreamHTTPError
	if !stderrors.As(err, &upstreamErr) {
		return false
	}
	switch upstreamErr.StatusCode {
	case http.StatusBadRequest, http.StatusNotFound, http.StatusMethodNotAllowed, http.StatusNotImplemented, http.StatusBadGateway, http.StatusServiceUnavailable:
		return true
	case http.StatusUnsupportedMediaType, http.StatusUnprocessableEntity:
		// A 415/422 can mean either an unsupported Responses request shape or
		// a genuine client error. Only try Chat Completions when the inbound
		// request satisfies the converter contract; otherwise preserve the
		// deterministic client failure without issuing a second upstream call.
		_, _, conversionErr := responsesRequestToChatCompletionsBody(body)
		return conversionErr == nil
	default:
		return false
	}
}

func (s *HTTPServer) forwardResponsesViaChatFallback(ctx context.Context, ch *relaybiz.Channel, header http.Header, body []byte) (*responsesFallbackResult, error) {
	chatBody, clientStream, err := responsesRequestToChatCompletionsBody(body)
	if err != nil {
		return nil, err
	}
	if clientStream {
		streamResp, err := s.forwardResponsesRawStream(ctx, ch, http.MethodPost, "/chat/completions", "", header, chatBody)
		if err != nil {
			return nil, err
		}
		fallbackStream := transformChatCompletionStreamToResponses(streamResp)
		return &responsesFallbackResult{
			Stream: &relayprovider.RawStreamResponse{
				StatusCode: streamResp.StatusCode,
				Header:     fallbackStream.Header,
				Body:       fallbackStream.Body,
			},
			Usage: rawUsage{TotalTokens: estimateRawTokens(body)},
		}, nil
	}

	resp, err := s.forwardResponsesRaw(ctx, ch, http.MethodPost, "/chat/completions", "", header, chatBody)
	if err != nil {
		return nil, err
	}
	bodyResp, usage, err := chatCompletionResponseToResponses(resp.Body)
	if err != nil {
		return nil, err
	}
	headerResp := resp.Header.Clone()
	headerResp.Set("Content-Type", "application/json")
	return &responsesFallbackResult{
		Response: &relayprovider.RawResponse{StatusCode: resp.StatusCode, Header: headerResp, Body: bodyResp},
		Usage:    usage,
	}, nil
}

func (s *HTTPServer) forwardResponsesViaChatFallbackObserved(ctx context.Context, ch *relaybiz.Channel, header http.Header, body []byte, triggerErr error) (*responsesFallbackResult, error) {
	result, fallbackErr := s.forwardResponsesViaChatFallback(ctx, ch, header, body)
	fields := []zap.Field{
		zap.Int64("channel_id", ch.ID),
		zap.String("channel", ch.Name),
		zap.Int("responses_status", relaybiz.UpstreamStatus(triggerErr)),
		zap.String("responses_error_category", responsesUpstreamErrorCategory(relaybiz.UpstreamStatus(triggerErr))),
	}
	if fallbackErr != nil {
		fields = append(fields,
			zap.Int("chat_fallback_status", relaybiz.UpstreamStatus(fallbackErr)),
			zap.String("chat_fallback_error_category", responsesUpstreamErrorCategory(relaybiz.UpstreamStatus(fallbackErr))),
		)
		applogger.Log.Warn("responses to chat fallback failed", fields...)
		return result, fallbackErr
	}
	status := 0
	if result != nil && result.Stream != nil {
		status = result.Stream.StatusCode
	} else if result != nil && result.Response != nil {
		status = result.Response.StatusCode
	}
	fields = append(fields, zap.Int("chat_fallback_status", status))
	applogger.Log.Info("responses to chat fallback succeeded", fields...)
	return result, nil
}

func responsesFallbackTerminalError(originalErr, fallbackErr error) error {
	if fallbackErr != nil {
		return fallbackErr
	}
	return originalErr
}

func responsesRequestToChatCompletionsBody(body []byte) ([]byte, bool, error) {
	var raw map[string]any
	if err := jsonx.Unmarshal(body, &raw); err != nil {
		return nil, false, fmt.Errorf("failed to parse responses request: %w", err)
	}
	model, _ := raw["model"].(string)
	if strings.TrimSpace(model) == "" {
		return nil, false, fmt.Errorf("model is required")
	}
	stream, _ := raw["stream"].(bool)

	// Codex sends Responses histories that may contain parallel calls,
	// interrupted calls, orphan tool outputs, or notices between a call and its
	// output. DeepSeek rejects those shapes verbatim; use the shared converter
	// that normalizes tool_call/tool-reply ordering before forwarding.
	var responsesReq apicompat.ResponsesRequest
	if err := jsonx.Unmarshal(body, &responsesReq); err != nil {
		return nil, false, fmt.Errorf("failed to parse responses input: %w", err)
	}
	chatReq, err := apicompat.ResponsesToChatCompletionsRequest(&responsesReq)
	if err != nil {
		return nil, false, err
	}
	messages := chatReq.Messages
	if len(messages) == 0 {
		messages = []apicompat.ChatMessage{{Role: "user", Content: jsonx.RawMessage(`""`)}}
	}

	chat := map[string]any{
		"model":    model,
		"messages": messages,
	}
	copyOptionalRawField(chat, raw, "temperature")
	copyOptionalRawField(chat, raw, "max_tokens")
	if _, ok := chat["max_tokens"]; !ok {
		copyOptionalRawFieldAs(chat, raw, "max_output_tokens", "max_tokens")
	}
	copyOptionalRawField(chat, raw, "top_p")
	copyOptionalRawField(chat, raw, "stop")
	if tools := responsesToolsToChatTools(raw["tools"]); len(tools) > 0 {
		chat["tools"] = tools
		if toolChoice, ok := responsesToolChoiceToChatToolChoice(raw["tool_choice"]); ok {
			chat["tool_choice"] = toolChoice
		}
	}
	if stream {
		chat["stream"] = true
		chat["stream_options"] = map[string]any{"include_usage": true}
	}
	chatBody, err := jsonx.Marshal(chat)
	if err != nil {
		return nil, false, err
	}
	return chatBody, stream, nil
}

func copyOptionalRawField(dst, src map[string]any, key string) {
	if value, ok := src[key]; ok {
		dst[key] = value
	}
}

func copyOptionalRawFieldAs(dst, src map[string]any, srcKey, dstKey string) {
	if value, ok := src[srcKey]; ok {
		dst[dstKey] = value
	}
}

func stringField(m map[string]any, key string) string {
	value, _ := m[key].(string)
	return value
}

func responsesToolsToChatTools(raw any) []map[string]any {
	items, ok := raw.([]any)
	if !ok {
		return nil
	}
	tools := make([]map[string]any, 0, len(items))
	for _, item := range items {
		tool, ok := item.(map[string]any)
		if !ok || stringField(tool, "type") != "function" {
			continue
		}
		function := map[string]any{}
		if nested, ok := tool["function"].(map[string]any); ok {
			copyOptionalRawField(function, nested, "name")
			copyOptionalRawField(function, nested, "description")
			copyOptionalRawField(function, nested, "parameters")
			copyOptionalRawField(function, nested, "strict")
		} else {
			copyOptionalRawField(function, tool, "name")
			copyOptionalRawField(function, tool, "description")
			copyOptionalRawField(function, tool, "parameters")
			copyOptionalRawField(function, tool, "strict")
		}
		if strings.TrimSpace(stringField(function, "name")) == "" {
			continue
		}
		tools = append(tools, map[string]any{
			"type":     "function",
			"function": function,
		})
	}
	return tools
}

func responsesToolChoiceToChatToolChoice(raw any) (any, bool) {
	switch value := raw.(type) {
	case string:
		if strings.TrimSpace(value) == "" {
			return nil, false
		}
		return value, true
	case map[string]any:
		if stringField(value, "type") != "function" {
			return value, true
		}
		name := stringField(value, "name")
		if name == "" {
			if function, ok := value["function"].(map[string]any); ok {
				name = stringField(function, "name")
			}
		}
		if strings.TrimSpace(name) == "" {
			return nil, false
		}
		return map[string]any{
			"type": "function",
			"function": map[string]any{
				"name": name,
			},
		}, true
	default:
		return nil, false
	}
}

func chatCompletionResponseToResponses(body []byte) ([]byte, rawUsage, error) {
	var chat struct {
		ID      string `json:"id"`
		Created int64  `json:"created"`
		Model   string `json:"model"`
		Choices []struct {
			Message struct {
				Role    string `json:"role"`
				Content string `json:"content"`
			} `json:"message"`
			FinishReason string `json:"finish_reason"`
		} `json:"choices"`
		Usage struct {
			PromptTokens        int64                       `json:"prompt_tokens"`
			CompletionTokens    int64                       `json:"completion_tokens"`
			InputTokens         int64                       `json:"input_tokens"`
			OutputTokens        int64                       `json:"output_tokens"`
			TotalTokens         int64                       `json:"total_tokens"`
			PromptTokensDetails *apicompat.ChatTokenDetails `json:"prompt_tokens_details,omitempty"`
		} `json:"usage"`
	}
	if err := jsonx.Unmarshal(body, &chat); err != nil {
		return nil, rawUsage{}, fmt.Errorf("failed to parse chat completion response: %w", err)
	}
	responseID := strings.TrimSpace(chat.ID)
	if responseID == "" {
		responseID = "resp_" + generateRequestID()
	}
	outputText := ""
	finishReason := ""
	if len(chat.Choices) > 0 {
		outputText = chat.Choices[0].Message.Content
		finishReason = chat.Choices[0].FinishReason
	}
	if finishReason == "" {
		finishReason = "stop"
	}
	usage := rawUsage{
		PromptTokens:        chat.Usage.PromptTokens,
		CompletionTokens:    chat.Usage.CompletionTokens,
		TotalTokens:         chat.Usage.TotalTokens,
		ReportedTotalTokens: chat.Usage.TotalTokens,
		Shape:               usagepkg.FieldShapeSignals{HasPromptTokens: true},
	}
	if details := chat.Usage.PromptTokensDetails; details != nil {
		usage.CacheReadTokens = int64(details.CachedTokens)
		usage.CacheCreation5mTokens = int64(details.CacheCreation5mTokens)
		usage.CacheCreation1hTokens = int64(details.CacheCreation1hTokens)
		usage.Shape.HasOpenAICachedDetail = true
	}
	if usage.PromptTokens == 0 {
		usage.PromptTokens = chat.Usage.InputTokens
	}
	if usage.CompletionTokens == 0 {
		usage.CompletionTokens = chat.Usage.OutputTokens
	}
	if usage.TotalTokens == 0 {
		usage.TotalTokens = usage.PromptTokens + usage.CompletionTokens
	}
	resp := map[string]any{
		"id":         responseID,
		"object":     "response",
		"created_at": chat.Created,
		"model":      chat.Model,
		"status":     "completed",
		"output": []map[string]any{
			{
				"id":      "msg_" + responseID,
				"type":    "message",
				"role":    "assistant",
				"status":  "completed",
				"content": []map[string]any{{"type": "output_text", "text": outputText}},
			},
		},
		"output_text": outputText,
		"usage":       responsesUsageMap(usage),
		"metadata": map[string]any{
			"fallback":      "chat_completions",
			"finish_reason": finishReason,
		},
	}
	respBody, err := jsonx.Marshal(resp)
	if err != nil {
		return nil, rawUsage{}, fmt.Errorf("failed to marshal fallback response: %w", err)
	}
	return respBody, usage, nil
}

func transformChatCompletionStreamToResponses(resp *relayprovider.RawStreamResponse) *relayprovider.RawStreamResponse {
	reader, writer := io.Pipe()
	header := resp.Header.Clone()
	header.Set("Content-Type", "text/event-stream")
	go func() {
		defer resp.Body.Close()
		defer writer.Close()
		scanner := bufio.NewScanner(resp.Body)
		scanner.Buffer(make([]byte, 0, 64*1024), 4*1024*1024)
		responseID := "resp_" + generateRequestID()
		outputItemID := "msg_" + responseID
		writeResponsesSSE(writer, map[string]any{
			"type": "response.created",
			"response": map[string]any{
				"id":     responseID,
				"object": "response",
				"status": "in_progress",
				"output": []any{},
			},
		})
		writeResponsesSSE(writer, map[string]any{
			"type":        "response.in_progress",
			"response_id": responseID,
			"response": map[string]any{
				"id":     responseID,
				"object": "response",
				"status": "in_progress",
			},
		})
		state := newResponsesStreamFallbackState(responseID, outputItemID)
		for scanner.Scan() {
			line := scanner.Text()
			data, ok := strings.CutPrefix(line, "data: ")
			if !ok || strings.TrimSpace(data) == "" {
				continue
			}
			if strings.TrimSpace(data) == "[DONE]" {
				break
			}
			state.writeChunk(writer, []byte(data))
		}
		state.finish(writer)
		_, _ = writer.Write([]byte("data: [DONE]\n\n"))
	}()
	return &relayprovider.RawStreamResponse{StatusCode: resp.StatusCode, Header: header, Body: reader}
}

type responsesStreamFallbackState struct {
	responseID    string
	textItemID    string
	textStarted   bool
	text          strings.Builder
	usage         rawUsage
	toolByIndex   map[int]*responsesStreamToolCall
	toolOrder     []int
	nextToolIndex int
	hasToolCalls  bool
}

type responsesStreamToolCall struct {
	Index     int
	ItemID    string
	CallID    string
	Name      string
	Arguments strings.Builder
	Added     bool
}

func newResponsesStreamFallbackState(responseID, textItemID string) *responsesStreamFallbackState {
	return &responsesStreamFallbackState{
		responseID:  responseID,
		textItemID:  textItemID,
		toolByIndex: make(map[int]*responsesStreamToolCall),
	}
}

func (s *responsesStreamFallbackState) writeChunk(w io.Writer, data []byte) bool {
	var chunk chatCompletionStreamChunk
	if err := jsonx.Unmarshal(data, &chunk); err != nil {
		return false
	}
	if chunk.Usage.TotalTokens > 0 || chunk.Usage.PromptTokens > 0 || chunk.Usage.CompletionTokens > 0 || chunk.Usage.InputTokens > 0 || chunk.Usage.OutputTokens > 0 || chunk.Usage.PromptTokensDetails != nil {
		promptTokens := chunk.Usage.PromptTokens
		if promptTokens == 0 {
			promptTokens = chunk.Usage.InputTokens
		}
		completionTokens := chunk.Usage.CompletionTokens
		if completionTokens == 0 {
			completionTokens = chunk.Usage.OutputTokens
		}
		usage := rawUsage{
			PromptTokens:        int64(promptTokens),
			CompletionTokens:    int64(completionTokens),
			TotalTokens:         int64(chunk.Usage.TotalTokens),
			ReportedTotalTokens: int64(chunk.Usage.TotalTokens),
			Shape:               usagepkg.FieldShapeSignals{HasPromptTokens: true},
		}
		if details := chunk.Usage.PromptTokensDetails; details != nil {
			usage.CacheReadTokens = int64(details.CachedTokens)
			usage.CacheCreation5mTokens = int64(details.CacheCreation5mTokens)
			usage.CacheCreation1hTokens = int64(details.CacheCreation1hTokens)
			usage.Shape.HasOpenAICachedDetail = true
		}
		s.usage = mergeRawUsage(usage, s.usage)
	}
	if len(chunk.Choices) == 0 {
		return false
	}
	done := false
	for _, choice := range chunk.Choices {
		if choice.Delta.Content != "" {
			s.writeTextDelta(w, choice.Delta.Content)
		}
		for _, toolCall := range choice.Delta.ToolCalls {
			s.writeToolCallDelta(w, toolCall)
		}
		if choice.FinishReason != nil {
			done = true
		}
	}
	return done
}

func (s *responsesStreamFallbackState) writeTextDelta(w io.Writer, delta string) {
	if !s.textStarted {
		s.textStarted = true
		writeResponsesSSE(w, map[string]any{
			"type":         "response.output_item.added",
			"response_id":  s.responseID,
			"output_index": 0,
			"item": map[string]any{
				"id":      s.textItemID,
				"type":    "message",
				"role":    "assistant",
				"status":  "in_progress",
				"content": []any{},
			},
		})
		writeResponsesSSE(w, map[string]any{
			"type":          "response.content_part.added",
			"response_id":   s.responseID,
			"item_id":       s.textItemID,
			"output_index":  0,
			"content_index": 0,
			"part": map[string]any{
				"type": "output_text",
				"text": "",
			},
		})
	}
	s.text.WriteString(delta)
	writeResponsesSSE(w, map[string]any{
		"type":            "response.output_text.delta",
		"response_id":     s.responseID,
		"item_id":         s.textItemID,
		"output_index":    0,
		"content_index":   0,
		"delta":           delta,
		"fallback_source": "chat_completions",
	})
}

func (s *responsesStreamFallbackState) writeToolCallDelta(w io.Writer, delta chatCompletionStreamToolCallDelta) {
	tool, ok := s.toolByIndex[delta.Index]
	if !ok {
		tool = &responsesStreamToolCall{
			Index:  delta.Index,
			ItemID: fmt.Sprintf("fc_%s_%d", s.responseID, delta.Index),
			CallID: delta.ID,
			Name:   delta.Function.Name,
		}
		if strings.TrimSpace(tool.CallID) == "" {
			tool.CallID = fmt.Sprintf("call_%s_%d", s.responseID, delta.Index)
		}
		s.toolByIndex[delta.Index] = tool
		s.toolOrder = append(s.toolOrder, delta.Index)
	} else {
		if delta.ID != "" {
			tool.CallID = delta.ID
		}
		if delta.Function.Name != "" {
			tool.Name = delta.Function.Name
		}
	}
	if delta.Function.Arguments != "" {
		tool.Arguments.WriteString(delta.Function.Arguments)
	}
	if !tool.Added {
		tool.Added = true
		s.hasToolCalls = true
		writeResponsesSSE(w, map[string]any{
			"type":         "response.output_item.added",
			"response_id":  s.responseID,
			"output_index": s.nextToolIndex,
			"item": map[string]any{
				"id":        tool.ItemID,
				"type":      "function_call",
				"status":    "in_progress",
				"call_id":   tool.CallID,
				"name":      tool.Name,
				"arguments": "",
			},
		})
		s.nextToolIndex++
	}
	if delta.Function.Arguments != "" {
		writeResponsesSSE(w, map[string]any{
			"type":         "response.function_call_arguments.delta",
			"response_id":  s.responseID,
			"item_id":      tool.ItemID,
			"output_index": tool.Index,
			"delta":        delta.Function.Arguments,
		})
	}
}

func (s *responsesStreamFallbackState) finish(w io.Writer) {
	if s.hasToolCalls {
		s.finishToolCalls(w)
		return
	}
	s.finishText(w)
}

func (s *responsesStreamFallbackState) finishText(w io.Writer) {
	if !s.textStarted {
		s.writeTextDelta(w, "")
	}
	text := s.text.String()
	writeResponsesSSE(w, map[string]any{
		"type":          "response.output_text.done",
		"response_id":   s.responseID,
		"item_id":       s.textItemID,
		"output_index":  0,
		"content_index": 0,
	})
	writeResponsesSSE(w, map[string]any{
		"type":          "response.content_part.done",
		"response_id":   s.responseID,
		"item_id":       s.textItemID,
		"output_index":  0,
		"content_index": 0,
		"part": map[string]any{
			"type": "output_text",
			"text": text,
		},
	})
	writeResponsesSSE(w, map[string]any{
		"type":         "response.output_item.done",
		"response_id":  s.responseID,
		"output_index": 0,
		"item": map[string]any{
			"id":     s.textItemID,
			"type":   "message",
			"role":   "assistant",
			"status": "completed",
			"content": []map[string]any{
				{"type": "output_text", "text": text},
			},
		},
	})
	writeResponsesSSE(w, map[string]any{
		"type":        "response.completed",
		"response_id": s.responseID,
		"response": map[string]any{
			"id":     s.responseID,
			"object": "response",
			"status": "completed",
			"output": []map[string]any{
				{
					"id":     s.textItemID,
					"type":   "message",
					"role":   "assistant",
					"status": "completed",
					"content": []map[string]any{
						{"type": "output_text", "text": text},
					},
				},
			},
			"output_text": text,
			"usage":       responsesUsageMap(s.usage),
		},
	})
}

func (s *responsesStreamFallbackState) finishToolCalls(w io.Writer) {
	output := make([]map[string]any, 0, len(s.toolOrder))
	for outputIndex, toolIndex := range s.toolOrder {
		tool := s.toolByIndex[toolIndex]
		arguments := tool.Arguments.String()
		writeResponsesSSE(w, map[string]any{
			"type":         "response.function_call_arguments.done",
			"response_id":  s.responseID,
			"item_id":      tool.ItemID,
			"output_index": outputIndex,
			"arguments":    arguments,
		})
		item := map[string]any{
			"id":        tool.ItemID,
			"type":      "function_call",
			"status":    "completed",
			"call_id":   tool.CallID,
			"name":      tool.Name,
			"arguments": arguments,
		}
		writeResponsesSSE(w, map[string]any{
			"type":         "response.output_item.done",
			"response_id":  s.responseID,
			"output_index": outputIndex,
			"item":         item,
		})
		output = append(output, item)
	}
	writeResponsesSSE(w, map[string]any{
		"type":        "response.completed",
		"response_id": s.responseID,
		"response": map[string]any{
			"id":     s.responseID,
			"object": "response",
			"status": "completed",
			"output": output,
			"usage":  responsesUsageMap(s.usage),
		},
	})
}

type chatCompletionStreamChunk struct {
	Choices []struct {
		Delta struct {
			Content   string                              `json:"content"`
			ToolCalls []chatCompletionStreamToolCallDelta `json:"tool_calls"`
		} `json:"delta"`
		FinishReason *string `json:"finish_reason"`
	} `json:"choices"`
	Usage struct {
		PromptTokens        int                         `json:"prompt_tokens"`
		CompletionTokens    int                         `json:"completion_tokens"`
		InputTokens         int                         `json:"input_tokens"`
		OutputTokens        int                         `json:"output_tokens"`
		TotalTokens         int                         `json:"total_tokens"`
		PromptTokensDetails *apicompat.ChatTokenDetails `json:"prompt_tokens_details,omitempty"`
	} `json:"usage"`
}

func responsesUsageMap(usage rawUsage) map[string]any {
	if usage.TotalTokens == 0 && usage.PromptTokens+usage.CompletionTokens > 0 {
		usage.TotalTokens = usage.PromptTokens + usage.CompletionTokens
	}
	out := map[string]any{
		"input_tokens":  usage.PromptTokens,
		"output_tokens": usage.CompletionTokens,
		"total_tokens":  usage.TotalTokens,
	}
	if usage.CacheReadTokens > 0 || usage.CacheCreation5mTokens > 0 || usage.CacheCreation1hTokens > 0 {
		details := map[string]any{}
		if usage.CacheReadTokens > 0 {
			details["cached_tokens"] = usage.CacheReadTokens
		}
		if usage.CacheCreation5mTokens > 0 {
			details["cache_creation_5m_tokens"] = usage.CacheCreation5mTokens
		}
		if usage.CacheCreation1hTokens > 0 {
			details["cache_creation_1h_tokens"] = usage.CacheCreation1hTokens
		}
		out["input_tokens_details"] = details
	}
	return out
}

type chatCompletionStreamToolCallDelta struct {
	Index    int    `json:"index"`
	ID       string `json:"id"`
	Type     string `json:"type"`
	Function struct {
		Name      string `json:"name"`
		Arguments string `json:"arguments"`
	} `json:"function"`
}

func writeResponsesSSE(w io.Writer, event map[string]any) {
	encoded, err := jsonx.Marshal(event)
	if err != nil {
		return
	}
	if eventType, ok := event["type"].(string); ok && eventType != "" {
		_, _ = w.Write([]byte("event: "))
		_, _ = w.Write([]byte(eventType))
		_, _ = w.Write([]byte("\n"))
	}
	_, _ = w.Write([]byte("data: "))
	_, _ = w.Write(encoded)
	_, _ = w.Write([]byte("\n\n"))
}

// ---------------------------------------------------------------------------
// Responses → Anthropic Messages fallback for ChannelTypeAnthropic API-key
// channels. The Anthropic upstream speaks /v1/messages; this path converts
// the inbound Responses request to an Anthropic Messages request, forwards it
// via the AnthropicProvider (now that Forward/ForwardStream are implemented),
// and converts the Anthropic response/stream back to the Responses format the
// client (Codex) expects.
// ---------------------------------------------------------------------------

// forwardResponsesViaAnthropicFallback converts a Responses API request to an
// Anthropic Messages request, calls the upstream, and converts the response
// back to Responses format. It mirrors forwardResponsesViaChatFallback but
// targets Anthropic channels (type=2) whose upstream is /v1/messages.
func (s *HTTPServer) forwardResponsesViaAnthropicFallback(ctx context.Context, ch *relaybiz.Channel, header http.Header, body []byte) (*responsesFallbackResult, error) {
	anthropicBody, clientStream, err := responsesRequestToAnthropicBody(body)
	if err != nil {
		return nil, err
	}
	if clientStream {
		streamResp, err := s.forwardResponsesRawStream(ctx, ch, http.MethodPost, "/messages", "", header, anthropicBody)
		if err != nil {
			return nil, err
		}
		fallbackStream := transformAnthropicStreamToResponses(streamResp)
		return &responsesFallbackResult{
			Stream: &relayprovider.RawStreamResponse{
				StatusCode: streamResp.StatusCode,
				Header:     fallbackStream.Header,
				Body:       fallbackStream.Body,
			},
			Usage: rawUsage{TotalTokens: estimateRawTokens(body)},
		}, nil
	}

	resp, err := s.forwardResponsesRaw(ctx, ch, http.MethodPost, "/messages", "", header, anthropicBody)
	if err != nil {
		return nil, err
	}
	bodyResp, usage, err := anthropicResponseToResponses(resp.Body)
	if err != nil {
		return nil, err
	}
	headerResp := resp.Header.Clone()
	headerResp.Set("Content-Type", "application/json")
	return &responsesFallbackResult{
		Response: &relayprovider.RawResponse{StatusCode: resp.StatusCode, Header: headerResp, Body: bodyResp},
		Usage:    usage,
	}, nil
}

// responsesRequestToAnthropicBody converts a Responses API request body into
// an Anthropic Messages request body and reports whether the client requested
// streaming.
func responsesRequestToAnthropicBody(body []byte) ([]byte, bool, error) {
	var rr apicompat.ResponsesRequest
	if err := jsonx.Unmarshal(body, &rr); err != nil {
		return nil, false, fmt.Errorf("failed to parse responses request: %w", err)
	}
	if strings.TrimSpace(rr.Model) == "" {
		return nil, false, fmt.Errorf("model is required")
	}
	ar, err := apicompat.ResponsesToAnthropicRequest(&rr)
	if err != nil {
		return nil, false, fmt.Errorf("responses→anthropic: %w", err)
	}
	// API-key channels may target third-party Anthropic-compatible endpoints.
	// Keep the fallback on the common Messages schema; newer Anthropic-only
	// reasoning extensions are rejected by providers such as Kimi Coding.
	ar.Thinking = nil
	ar.OutputConfig = nil
	for index := range ar.Tools {
		// Third-party Messages endpoints generally support client tools, but
		// not Anthropic server-tool type identifiers such as web_search_20250305.
		ar.Tools[index].Type = ""
		if len(ar.Tools[index].InputSchema) == 0 || string(ar.Tools[index].InputSchema) == "null" {
			ar.Tools[index].InputSchema = []byte(`{"type":"object","properties":{}}`)
		}
	}
	out, err := jsonx.Marshal(ar)
	if err != nil {
		return nil, false, err
	}
	return out, rr.Stream, nil
}

// anthropicResponseToResponses converts a non-streaming Anthropic Messages
// response body to a Responses API response body and extracts usage.
func anthropicResponseToResponses(body []byte) ([]byte, rawUsage, error) {
	var ar apicompat.AnthropicResponse
	if err := jsonx.Unmarshal(body, &ar); err != nil {
		return nil, rawUsage{}, fmt.Errorf("failed to parse anthropic response: %w", err)
	}
	rr := apicompat.AnthropicToResponsesResponse(&ar)
	out, err := jsonx.Marshal(rr)
	if err != nil {
		return nil, rawUsage{}, fmt.Errorf("failed to marshal responses response: %w", err)
	}
	usage := rawUsage{
		PromptTokens:          int64(ar.Usage.InputTokens),
		CompletionTokens:      int64(ar.Usage.OutputTokens),
		CacheReadTokens:       int64(ar.Usage.CacheReadInputTokens),
		CacheCreation5mTokens: int64(ar.Usage.CacheCreationInputTokens),
		Shape: usagepkg.FieldShapeSignals{
			HasInputTokens:            true,
			HasAnthropicCacheRead:     true,
			HasAnthropicCacheCreation: ar.Usage.CacheCreationInputTokens != 0,
		},
	}
	usage.TotalTokens = usage.PromptTokens + usage.CacheReadTokens + usage.CacheCreation5mTokens + usage.CompletionTokens
	if usage.TotalTokens <= 0 {
		usage.TotalTokens = estimateRawTokens(body)
	}
	return out, usage, nil
}

// transformAnthropicStreamToResponses wraps an Anthropic SSE stream and
// converts it to a Responses SSE stream via an io.Pipe, mirroring
// transformChatCompletionStreamToResponses. The Anthropic→Responses stream
// conversion uses the apicompat AnthropicEventToResponsesEvents converter.
func transformAnthropicStreamToResponses(resp *relayprovider.RawStreamResponse) *relayprovider.RawStreamResponse {
	reader, writer := io.Pipe()
	header := resp.Header.Clone()
	header.Set("Content-Type", "text/event-stream")
	go func() {
		defer resp.Body.Close()
		defer writer.Close()
		scanner := bufio.NewScanner(resp.Body)
		scanner.Buffer(make([]byte, 0, 64*1024), 4*1024*1024)
		state := apicompat.NewAnthropicEventToResponsesState()
		for scanner.Scan() {
			line := scanner.Text()
			data, ok := sseAnthropicData(line)
			if !ok {
				continue
			}
			var evt apicompat.AnthropicStreamEvent
			if err := jsonx.UnmarshalFromString(data, &evt); err != nil {
				continue
			}
			for _, rse := range apicompat.AnthropicEventToResponsesEvents(&evt, state) {
				sse, err := apicompat.ResponsesEventToSSE(rse)
				if err != nil {
					continue
				}
				if _, err := io.WriteString(writer, sse); err != nil {
					return
				}
			}
		}
		if err := scanner.Err(); err != nil {
			// CR 2026-08-05: only emit a synthetic response.failed when the
			// stream did NOT already reach a normal terminal state. If the
			// upstream sent message_stop (response.completed already emitted)
			// and only then the connection errored, a second terminal event
			// would be contradictory — the client would see completed
			// immediately followed by failed.
			if !state.CompletedSent {
				evt := apicompat.ResponsesStreamEvent{
					Type: "response.failed",
					Response: &apicompat.ResponsesResponse{
						Status: "failed",
						Error:  &apicompat.ResponsesError{Code: "stream_interrupted", Message: "upstream stream interrupted"},
					},
				}
				if sse, err := apicompat.ResponsesEventToSSE(evt); err == nil {
					_, _ = io.WriteString(writer, sse)
				}
			}
			// CR 2026-08-05: the [DONE] sentinel MUST follow the terminal
			// event so the client knows the SSE stream has ended cleanly.
			// Without it the client keeps waiting for more data and reports
			// "stream disconnected before completion".
			_, _ = io.WriteString(writer, "data: [DONE]\n\n")
			return
		}
		// CR 2026-08-05: if the upstream closed before message_start arrived,
		// FinalizeAnthropicResponsesStream returns nil (CreatedSent=false).
		// Without a terminal event the client sees a bare pipe close and
		// reports "stream closed before response.completed". Synthesise a
		// response.failed so the stream always ends with a terminal event.
		terminalEvents := apicompat.FinalizeAnthropicResponsesStream(state)
		if len(terminalEvents) == 0 && !state.CreatedSent {
			terminalEvents = []apicompat.ResponsesStreamEvent{{
				Type: "response.failed",
				Response: &apicompat.ResponsesResponse{
					Status: "failed",
					Error:  &apicompat.ResponsesError{Code: "stream_interrupted", Message: "upstream stream closed before any event"},
				},
			}}
		}
		for _, rse := range terminalEvents {
			sse, err := apicompat.ResponsesEventToSSE(rse)
			if err != nil {
				continue
			}
			if _, err := io.WriteString(writer, sse); err != nil {
				return
			}
		}
		_, _ = io.WriteString(writer, "data: [DONE]\n\n")
	}()
	return &relayprovider.RawStreamResponse{StatusCode: resp.StatusCode, Header: header, Body: reader}
}

// sseAnthropicData extracts the JSON payload from an SSE data line. The space
// after "data:" is optional per the SSE format and is omitted by some
// Anthropic-compatible providers.
func sseAnthropicData(line string) (string, bool) {
	const prefix = "data:"
	if !strings.HasPrefix(line, prefix) {
		return "", false
	}
	data := strings.TrimSpace(line[len(prefix):])
	if data == "" || data == "[DONE]" {
		return "", false
	}
	return data, true
}

package server

import (
	"bufio"
	"context"
	stderrors "errors"
	"fmt"
	"io"
	"net/http"
	"strings"

	"github.com/bytedance/sonic"

	relaybiz "micro-one-api/internal/relay/biz"
	relayprovider "micro-one-api/internal/relay/provider"
)

type responsesFallbackResult struct {
	Response *relayprovider.RawResponse
	Stream   *relayprovider.RawStreamResponse
	Usage    rawUsage
}

func shouldFallbackResponsesToChat(path string, err error) bool {
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

func responsesRequestToChatCompletionsBody(body []byte) ([]byte, bool, error) {
	var raw map[string]interface{}
	if err := sonic.Unmarshal(body, &raw); err != nil {
		return nil, false, fmt.Errorf("failed to parse responses request: %w", err)
	}
	model, _ := raw["model"].(string)
	if strings.TrimSpace(model) == "" {
		return nil, false, fmt.Errorf("model is required")
	}
	stream, _ := raw["stream"].(bool)
	messages := responsesInputToMessages(raw["input"])
	if len(messages) == 0 {
		messages = []map[string]interface{}{{"role": "user", "content": ""}}
	}

	chat := map[string]interface{}{
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
		chat["stream_options"] = map[string]interface{}{"include_usage": true}
	}
	chatBody, err := sonic.Marshal(chat)
	if err != nil {
		return nil, false, err
	}
	return chatBody, stream, nil
}

func copyOptionalRawField(dst, src map[string]interface{}, key string) {
	if value, ok := src[key]; ok {
		dst[key] = value
	}
}

func copyOptionalRawFieldAs(dst, src map[string]interface{}, srcKey, dstKey string) {
	if value, ok := src[srcKey]; ok {
		dst[dstKey] = value
	}
}

func responsesInputToMessages(input interface{}) []map[string]interface{} {
	switch v := input.(type) {
	case string:
		return []map[string]interface{}{{"role": "user", "content": v}}
	case []interface{}:
		messages := make([]map[string]interface{}, 0, len(v))
		for _, item := range v {
			msg, ok := responseInputItemToMessage(item)
			if ok {
				messages = append(messages, msg)
			}
		}
		return messages
	default:
		return nil
	}
}

func responseInputItemToMessage(item interface{}) (map[string]interface{}, bool) {
	m, ok := item.(map[string]interface{})
	if !ok {
		return nil, false
	}
	itemType, _ := m["type"].(string)
	switch itemType {
	case "function_call":
		arguments, _ := m["arguments"].(string)
		if strings.TrimSpace(arguments) == "" {
			arguments = "{}"
		}
		return map[string]interface{}{
			"role": "assistant",
			"tool_calls": []map[string]interface{}{
				{
					"id":   stringField(m, "call_id"),
					"type": "function",
					"function": map[string]interface{}{
						"name":      stringField(m, "name"),
						"arguments": arguments,
					},
				},
			},
		}, true
	case "function_call_output":
		return map[string]interface{}{
			"role":         "tool",
			"tool_call_id": stringField(m, "call_id"),
			"content":      stringField(m, "output"),
		}, true
	}
	role, _ := m["role"].(string)
	role = responsesRoleToChatRole(role)
	if content, ok := m["content"].(string); ok {
		return map[string]interface{}{"role": role, "content": content}, true
	}
	if content, ok := m["content"].([]interface{}); ok {
		return map[string]interface{}{"role": role, "content": responseContentPartsToText(content)}, true
	}
	if text, ok := m["text"].(string); ok {
		return map[string]interface{}{"role": role, "content": text}, true
	}
	return nil, false
}

func responsesRoleToChatRole(role string) string {
	trimmed := strings.TrimSpace(role)
	if trimmed == "" {
		return "user"
	}
	if strings.EqualFold(trimmed, "developer") {
		return "system"
	}
	return trimmed
}

func stringField(m map[string]interface{}, key string) string {
	value, _ := m[key].(string)
	return value
}

func responseContentPartsToText(parts []interface{}) string {
	var b strings.Builder
	for _, part := range parts {
		m, ok := part.(map[string]interface{})
		if !ok {
			continue
		}
		if text, ok := m["text"].(string); ok {
			if b.Len() > 0 {
				b.WriteByte('\n')
			}
			b.WriteString(text)
		}
	}
	return b.String()
}

func responsesToolsToChatTools(raw interface{}) []map[string]interface{} {
	items, ok := raw.([]interface{})
	if !ok {
		return nil
	}
	tools := make([]map[string]interface{}, 0, len(items))
	for _, item := range items {
		tool, ok := item.(map[string]interface{})
		if !ok || stringField(tool, "type") != "function" {
			continue
		}
		function := map[string]interface{}{}
		if nested, ok := tool["function"].(map[string]interface{}); ok {
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
		tools = append(tools, map[string]interface{}{
			"type":     "function",
			"function": function,
		})
	}
	return tools
}

func responsesToolChoiceToChatToolChoice(raw interface{}) (interface{}, bool) {
	switch value := raw.(type) {
	case string:
		if strings.TrimSpace(value) == "" {
			return nil, false
		}
		return value, true
	case map[string]interface{}:
		if stringField(value, "type") != "function" {
			return value, true
		}
		name := stringField(value, "name")
		if name == "" {
			if function, ok := value["function"].(map[string]interface{}); ok {
				name = stringField(function, "name")
			}
		}
		if strings.TrimSpace(name) == "" {
			return nil, false
		}
		return map[string]interface{}{
			"type": "function",
			"function": map[string]interface{}{
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
			PromptTokens     int64 `json:"prompt_tokens"`
			CompletionTokens int64 `json:"completion_tokens"`
			TotalTokens      int64 `json:"total_tokens"`
		} `json:"usage"`
	}
	if err := sonic.Unmarshal(body, &chat); err != nil {
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
		PromptTokens:     chat.Usage.PromptTokens,
		CompletionTokens: chat.Usage.CompletionTokens,
		TotalTokens:      chat.Usage.TotalTokens,
	}
	if usage.TotalTokens == 0 {
		usage.TotalTokens = usage.PromptTokens + usage.CompletionTokens
	}
	resp := map[string]interface{}{
		"id":         responseID,
		"object":     "response",
		"created_at": chat.Created,
		"model":      chat.Model,
		"status":     "completed",
		"output": []map[string]interface{}{
			{
				"id":      "msg_" + responseID,
				"type":    "message",
				"role":    "assistant",
				"status":  "completed",
				"content": []map[string]interface{}{{"type": "output_text", "text": outputText}},
			},
		},
		"output_text": outputText,
		"usage": map[string]interface{}{
			"input_tokens":  usage.PromptTokens,
			"output_tokens": usage.CompletionTokens,
			"total_tokens":  usage.TotalTokens,
		},
		"metadata": map[string]interface{}{
			"fallback":      "chat_completions",
			"finish_reason": finishReason,
		},
	}
	respBody, err := sonic.Marshal(resp)
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
		scanner.Buffer(make([]byte, 0, 64*1024), 1024*1024)
		responseID := "resp_" + generateRequestID()
		outputItemID := "msg_" + responseID
		writeResponsesSSE(writer, map[string]interface{}{
			"type": "response.created",
			"response": map[string]interface{}{
				"id":     responseID,
				"object": "response",
				"status": "in_progress",
				"output": []interface{}{},
			},
		})
		writeResponsesSSE(writer, map[string]interface{}{
			"type":        "response.in_progress",
			"response_id": responseID,
			"response": map[string]interface{}{
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
			done := state.writeChunk(writer, []byte(data))
			if done {
				break
			}
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
	if err := sonic.Unmarshal(data, &chunk); err != nil {
		return false
	}
	if chunk.Usage.TotalTokens > 0 || chunk.Usage.PromptTokens > 0 || chunk.Usage.CompletionTokens > 0 {
		s.usage = rawUsage{
			PromptTokens:     int64(chunk.Usage.PromptTokens),
			CompletionTokens: int64(chunk.Usage.CompletionTokens),
			TotalTokens:      int64(chunk.Usage.TotalTokens),
		}
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
		writeResponsesSSE(w, map[string]interface{}{
			"type":         "response.output_item.added",
			"response_id":  s.responseID,
			"output_index": 0,
			"item": map[string]interface{}{
				"id":      s.textItemID,
				"type":    "message",
				"role":    "assistant",
				"status":  "in_progress",
				"content": []interface{}{},
			},
		})
		writeResponsesSSE(w, map[string]interface{}{
			"type":          "response.content_part.added",
			"response_id":   s.responseID,
			"item_id":       s.textItemID,
			"output_index":  0,
			"content_index": 0,
			"part": map[string]interface{}{
				"type": "output_text",
				"text": "",
			},
		})
	}
	s.text.WriteString(delta)
	writeResponsesSSE(w, map[string]interface{}{
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
		writeResponsesSSE(w, map[string]interface{}{
			"type":         "response.output_item.added",
			"response_id":  s.responseID,
			"output_index": s.nextToolIndex,
			"item": map[string]interface{}{
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
		writeResponsesSSE(w, map[string]interface{}{
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
	writeResponsesSSE(w, map[string]interface{}{
		"type":          "response.output_text.done",
		"response_id":   s.responseID,
		"item_id":       s.textItemID,
		"output_index":  0,
		"content_index": 0,
	})
	writeResponsesSSE(w, map[string]interface{}{
		"type":          "response.content_part.done",
		"response_id":   s.responseID,
		"item_id":       s.textItemID,
		"output_index":  0,
		"content_index": 0,
		"part": map[string]interface{}{
			"type": "output_text",
			"text": text,
		},
	})
	writeResponsesSSE(w, map[string]interface{}{
		"type":         "response.output_item.done",
		"response_id":  s.responseID,
		"output_index": 0,
		"item": map[string]interface{}{
			"id":     s.textItemID,
			"type":   "message",
			"role":   "assistant",
			"status": "completed",
			"content": []map[string]interface{}{
				{"type": "output_text", "text": text},
			},
		},
	})
	writeResponsesSSE(w, map[string]interface{}{
		"type":        "response.completed",
		"response_id": s.responseID,
		"response": map[string]interface{}{
			"id":     s.responseID,
			"object": "response",
			"status": "completed",
			"output": []map[string]interface{}{
				{
					"id":     s.textItemID,
					"type":   "message",
					"role":   "assistant",
					"status": "completed",
					"content": []map[string]interface{}{
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
	output := make([]map[string]interface{}, 0, len(s.toolOrder))
	for outputIndex, toolIndex := range s.toolOrder {
		tool := s.toolByIndex[toolIndex]
		arguments := tool.Arguments.String()
		writeResponsesSSE(w, map[string]interface{}{
			"type":         "response.function_call_arguments.done",
			"response_id":  s.responseID,
			"item_id":      tool.ItemID,
			"output_index": outputIndex,
			"arguments":    arguments,
		})
		item := map[string]interface{}{
			"id":        tool.ItemID,
			"type":      "function_call",
			"status":    "completed",
			"call_id":   tool.CallID,
			"name":      tool.Name,
			"arguments": arguments,
		}
		writeResponsesSSE(w, map[string]interface{}{
			"type":         "response.output_item.done",
			"response_id":  s.responseID,
			"output_index": outputIndex,
			"item":         item,
		})
		output = append(output, item)
	}
	writeResponsesSSE(w, map[string]interface{}{
		"type":        "response.completed",
		"response_id": s.responseID,
		"response": map[string]interface{}{
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
		PromptTokens     int `json:"prompt_tokens"`
		CompletionTokens int `json:"completion_tokens"`
		TotalTokens      int `json:"total_tokens"`
	} `json:"usage"`
}

func responsesUsageMap(usage rawUsage) map[string]interface{} {
	if usage.TotalTokens == 0 && usage.PromptTokens+usage.CompletionTokens > 0 {
		usage.TotalTokens = usage.PromptTokens + usage.CompletionTokens
	}
	return map[string]interface{}{
		"input_tokens":  usage.PromptTokens,
		"output_tokens": usage.CompletionTokens,
		"total_tokens":  usage.TotalTokens,
	}
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

func writeResponsesSSE(w io.Writer, event map[string]interface{}) {
	encoded, err := sonic.Marshal(event)
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

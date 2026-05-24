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
	case http.StatusNotFound, http.StatusMethodNotAllowed, http.StatusNotImplemented, http.StatusBadGateway, http.StatusServiceUnavailable:
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
	copyOptionalRawField(chat, raw, "top_p")
	copyOptionalRawField(chat, raw, "stop")
	copyOptionalRawField(chat, raw, "tools")
	copyOptionalRawField(chat, raw, "tool_choice")
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
	role, _ := m["role"].(string)
	if strings.TrimSpace(role) == "" {
		role = "user"
	}
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
		_, _ = fmt.Fprintf(writer, "data: {\"type\":\"response.created\",\"response\":{\"id\":%q,\"object\":\"response\",\"status\":\"in_progress\"}}\n\n", responseID)
		for scanner.Scan() {
			line := scanner.Text()
			data, ok := strings.CutPrefix(line, "data: ")
			if !ok || strings.TrimSpace(data) == "" {
				continue
			}
			if strings.TrimSpace(data) == "[DONE]" {
				break
			}
			event, done := chatCompletionStreamDataToResponseEvent(responseID, []byte(data))
			if event != nil {
				encoded, err := sonic.Marshal(event)
				if err == nil {
					_, _ = writer.Write([]byte("data: "))
					_, _ = writer.Write(encoded)
					_, _ = writer.Write([]byte("\n\n"))
				}
			}
			if done {
				break
			}
		}
		_, _ = fmt.Fprintf(writer, "data: {\"type\":\"response.completed\",\"response\":{\"id\":%q,\"object\":\"response\",\"status\":\"completed\"}}\n\n", responseID)
		_, _ = writer.Write([]byte("data: [DONE]\n\n"))
	}()
	return &relayprovider.RawStreamResponse{StatusCode: resp.StatusCode, Header: header, Body: reader}
}

func chatCompletionStreamDataToResponseEvent(responseID string, data []byte) (map[string]interface{}, bool) {
	var chunk struct {
		ID      string `json:"id"`
		Model   string `json:"model"`
		Choices []struct {
			Delta struct {
				Content string `json:"content"`
			} `json:"delta"`
			FinishReason *string `json:"finish_reason"`
		} `json:"choices"`
		Usage struct {
			PromptTokens     int64 `json:"prompt_tokens"`
			CompletionTokens int64 `json:"completion_tokens"`
			TotalTokens      int64 `json:"total_tokens"`
		} `json:"usage"`
	}
	if err := sonic.Unmarshal(data, &chunk); err != nil {
		return nil, false
	}
	if chunk.ID != "" {
		responseID = chunk.ID
	}
	if len(chunk.Choices) == 0 {
		return nil, false
	}
	if chunk.Choices[0].Delta.Content != "" {
		return map[string]interface{}{
			"type":            "response.output_text.delta",
			"response_id":     responseID,
			"output_index":    0,
			"content_index":   0,
			"delta":           chunk.Choices[0].Delta.Content,
			"fallback_source": "chat_completions",
		}, false
	}
	if chunk.Choices[0].FinishReason != nil {
		return map[string]interface{}{
			"type":          "response.output_text.done",
			"response_id":   responseID,
			"output_index":  0,
			"content_index": 0,
		}, true
	}
	return nil, false
}

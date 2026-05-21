package provider

import (
	"bufio"
	"bytes"
	"context"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/bytedance/sonic"
	"go.uber.org/zap"
	applogger "micro-one-api/internal/pkg/logger"
)

// AnthropicProvider implements the Provider interface for Anthropic Claude API.
// It translates between OpenAI-compatible requests/responses and the Anthropic API format.
type AnthropicProvider struct {
	httpClient *http.Client
	baseURL    string
	apiKey     string
	timeout    time.Duration
}

// NewAnthropicProvider creates a new Anthropic Claude provider.
func NewAnthropicProvider(baseURL, apiKey string, timeout time.Duration) *AnthropicProvider {
	if timeout == 0 {
		timeout = 30 * time.Second
	}
	if baseURL == "" {
		baseURL = "https://api.anthropic.com"
	}
	return &AnthropicProvider{
		httpClient: &http.Client{Timeout: timeout},
		baseURL:    baseURL,
		apiKey:     apiKey,
		timeout:    timeout,
	}
}

// Forward is not supported for Anthropic because non-chat OpenAI-compatible
// endpoints require endpoint-specific request and response conversion.
func (p *AnthropicProvider) Forward(ctx context.Context, req *RawRequest) (*RawResponse, error) {
	return nil, fmt.Errorf("raw forwarding is not supported by anthropic provider")
}

// Anthropic API request/response structures

type anthropicRequest struct {
	Model     string             `json:"model"`
	Messages  []anthropicMessage `json:"messages"`
	System    string             `json:"system,omitempty"`
	MaxTokens int                `json:"max_tokens"`
	Stream    bool               `json:"stream,omitempty"`
}

type anthropicMessage struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

type anthropicResponse struct {
	ID         string             `json:"id"`
	Type       string             `json:"type"`
	Role       string             `json:"role"`
	Content    []anthropicContent `json:"content"`
	Model      string             `json:"model"`
	StopReason string             `json:"stop_reason"`
	Usage      anthropicUsage     `json:"usage"`
}

type anthropicContent struct {
	Type string `json:"type"`
	Text string `json:"text"`
}

type anthropicUsage struct {
	InputTokens  int `json:"input_tokens"`
	OutputTokens int `json:"output_tokens"`
}

// Anthropic SSE stream event
type anthropicStreamEvent struct {
	Type  string `json:"type"`
	Index int    `json:"index,omitempty"`
	Delta *struct {
		Type string `json:"type"`
		Text string `json:"text"`
	} `json:"delta,omitempty"`
	Message *anthropicResponse `json:"message,omitempty"`
	Usage   *anthropicUsage    `json:"usage,omitempty"`
}

// convertToAnthropicRequest converts an OpenAI-style request to Anthropic format.
func convertToAnthropicRequest(req *ChatCompletionsRequest) *anthropicRequest {
	anthropicReq := &anthropicRequest{
		Model:     req.Model,
		MaxTokens: 4096,
		Stream:    req.Stream,
	}

	if req.MaxTokens != nil && *req.MaxTokens > 0 {
		anthropicReq.MaxTokens = *req.MaxTokens
	}

	// Extract system message and convert roles
	for _, msg := range req.Messages {
		switch msg.Role {
		case "system":
			anthropicReq.System = msg.Content
		case "user":
			anthropicReq.Messages = append(anthropicReq.Messages, anthropicMessage{
				Role:    "user",
				Content: msg.Content,
			})
		case "assistant":
			anthropicReq.Messages = append(anthropicReq.Messages, anthropicMessage{
				Role:    "assistant",
				Content: msg.Content,
			})
		default:
			// Treat unknown roles as user messages
			anthropicReq.Messages = append(anthropicReq.Messages, anthropicMessage{
				Role:    "user",
				Content: msg.Content,
			})
		}
	}

	return anthropicReq
}

// convertFromAnthropicResponse converts an Anthropic response to OpenAI format.
func convertFromAnthropicResponse(resp *anthropicResponse, model string) *ChatCompletionsResponse {
	content := ""
	if len(resp.Content) > 0 {
		content = resp.Content[0].Text
	}

	finishReason := "stop"
	switch resp.StopReason {
	case "end_turn":
		finishReason = "stop"
	case "max_tokens":
		finishReason = "length"
	case "stop_sequence":
		finishReason = "stop"
	}

	return &ChatCompletionsResponse{
		ID:      resp.ID,
		Object:  "chat.completion",
		Created: time.Now().Unix(),
		Model:   model,
		Choices: []Choice{
			{
				Index: 0,
				Message: Message{
					Role:    "assistant",
					Content: content,
				},
				FinishReason: finishReason,
			},
		},
		Usage: Usage{
			PromptTokens:     resp.Usage.InputTokens,
			CompletionTokens: resp.Usage.OutputTokens,
			TotalTokens:      resp.Usage.InputTokens + resp.Usage.OutputTokens,
		},
	}
}

// ChatCompletions sends a chat completions request to the Anthropic API.
func (p *AnthropicProvider) ChatCompletions(ctx context.Context, req *ChatCompletionsRequest) (*ChatCompletionsResponse, error) {
	anthropicReq := convertToAnthropicRequest(req)

	body, err := sonic.Marshal(anthropicReq)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal request: %w", err)
	}

	url := fmt.Sprintf("%s/v1/messages", p.baseURL)
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("failed to create request: %w", err)
	}

	httpReq.Header.Set("Content-Type", "application/json")
	httpReq.Header.Set("x-api-key", p.apiKey)
	httpReq.Header.Set("anthropic-version", "2023-06-01")

	resp, err := p.httpClient.Do(httpReq)
	if err != nil {
		return nil, fmt.Errorf("failed to send request: %w", err)
	}
	defer resp.Body.Close()

	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("failed to read response: %w", err)
	}

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("anthropic error: status=%d, body=%s", resp.StatusCode, string(respBody))
	}

	var anthropicResp anthropicResponse
	if err := sonic.Unmarshal(respBody, &anthropicResp); err != nil {
		return nil, fmt.Errorf("failed to unmarshal response: %w", err)
	}

	return convertFromAnthropicResponse(&anthropicResp, req.Model), nil
}

// ChatCompletionsStream sends a streaming request to the Anthropic API.
func (p *AnthropicProvider) ChatCompletionsStream(ctx context.Context, req *ChatCompletionsRequest) (<-chan StreamChunk, error) {
	anthropicReq := convertToAnthropicRequest(req)
	anthropicReq.Stream = true

	body, err := sonic.Marshal(anthropicReq)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal request: %w", err)
	}

	url := fmt.Sprintf("%s/v1/messages", p.baseURL)
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("failed to create request: %w", err)
	}

	httpReq.Header.Set("Content-Type", "application/json")
	httpReq.Header.Set("x-api-key", p.apiKey)
	httpReq.Header.Set("anthropic-version", "2023-06-01")

	resp, err := p.httpClient.Do(httpReq)
	if err != nil {
		return nil, fmt.Errorf("failed to send request: %w", err)
	}

	if resp.StatusCode != http.StatusOK {
		respBody, _ := io.ReadAll(resp.Body)
		resp.Body.Close()
		return nil, fmt.Errorf("anthropic error: status=%d, body=%s", resp.StatusCode, string(respBody))
	}

	chunkChan := make(chan StreamChunk, 10)

	go func() {
		defer close(chunkChan)
		defer resp.Body.Close()

		scanner := bufio.NewScanner(resp.Body)
		for scanner.Scan() {
			line := scanner.Text()
			if line == "" {
				continue
			}

			// Anthropic SSE format: "event: <type>\ndata: <json>"
			if strings.HasPrefix(line, "event:") {
				continue
			}

			data, ok := strings.CutPrefix(line, "data: ")
			if !ok {
				continue
			}

			var event anthropicStreamEvent
			if err := sonic.Unmarshal([]byte(data), &event); err != nil {
				applogger.Log.Warn("failed to parse Anthropic SSE event",
					zap.Error(err),
					zap.String("data_preview", applogger.TruncateString(data, 100)),
				)
				continue
			}

			// Convert content_block_delta to StreamChunk
			if event.Type == "content_block_delta" && event.Delta != nil {
				chunk := StreamChunk{
					ID:      "",
					Object:  "chat.completion.chunk",
					Created: time.Now().Unix(),
					Model:   req.Model,
					Choices: []struct {
						Index int `json:"index"`
						Delta struct {
							Role             string `json:"role,omitempty"`
							Content          string `json:"content,omitempty"`
							ReasoningContent any    `json:"reasoning_content,omitempty"`
						} `json:"delta"`
						FinishReason *string `json:"finish_reason,omitempty"`
					}{
						{
							Index: 0,
							Delta: struct {
								Role             string `json:"role,omitempty"`
								Content          string `json:"content,omitempty"`
								ReasoningContent any    `json:"reasoning_content,omitempty"`
							}{
								Content: event.Delta.Text,
							},
						},
					},
				}
				chunkChan <- chunk
			}

			// Handle message_stop
			if event.Type == "message_stop" {
				break
			}
		}

		if err := scanner.Err(); err != nil {
			applogger.Log.Error("Anthropic stream scanner error", zap.Error(err))
		}
	}()

	return chunkChan, nil
}

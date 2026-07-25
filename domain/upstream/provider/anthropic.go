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
	applogger "micro-one-api/platform/logging"
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

// anthropicForwardBaseURL resolves the upstream base URL, trimming any trailing
// slash. It mirrors the behaviour of NewAnthropicProvider (defaulting to the
// public Anthropic API) but is shared by Forward / ForwardStream so the raw
// path honours a channel-provided base URL.
func (p *AnthropicProvider) anthropicForwardBaseURL() string {
	base := p.baseURL
	if base == "" {
		base = "https://api.anthropic.com"
	}
	return strings.TrimRight(base, "/")
}

// anthropicForwardEndpoint builds the upstream URL for a raw request. The
// Anthropic Messages API is rooted at /v1; requests that already include the
// /v1 prefix are passed through, others are joined under /v1.
func (p *AnthropicProvider) anthropicForwardEndpoint(path string) string {
	cleaned := strings.TrimLeft(strings.TrimSpace(path), "/")
	if cleaned == "" {
		cleaned = "messages"
	}
	if strings.HasPrefix(cleaned, "v1/") {
		return p.anthropicForwardBaseURL() + "/" + cleaned
	}
	return p.anthropicForwardBaseURL() + "/v1/" + cleaned
}

// anthropicSetForwardHeaders stamps the headers required by the Anthropic
// Messages API (x-api-key + anthropic-version), forwarding non-hop-by-hop
// client headers while stripping any inbound Authorization so the upstream
// only sees the channel's API key.
func (p *AnthropicProvider) anthropicSetForwardHeaders(dst, src http.Header) {
	if src != nil {
		for key, values := range src {
			if isHopByHopHeader(key) || strings.EqualFold(key, "Authorization") || strings.EqualFold(key, "x-api-key") || strings.EqualFold(key, "anthropic-version") {
				continue
			}
			for _, value := range values {
				dst.Add(key, value)
			}
		}
	}
	dst.Set("Content-Type", "application/json")
	if p.apiKey != "" {
		dst.Set("x-api-key", p.apiKey)
	}
	dst.Set("anthropic-version", "2023-06-01")
}

// Forward sends a raw request to the Anthropic Messages API and returns the
// buffered response. The path is resolved under /v1 (e.g. "messages" →
// "/v1/messages"). It is used by the Responses → Anthropic conversion path
// so that API-key channels of type ChannelTypeAnthropic can serve Responses
// protocol clients (Codex) without requiring a separate provider.
func (p *AnthropicProvider) Forward(ctx context.Context, req *RawRequest) (*RawResponse, error) {
	if req == nil {
		return nil, fmt.Errorf("raw request is nil")
	}
	method := req.Method
	if method == "" {
		method = http.MethodPost
	}
	endpoint := p.anthropicForwardEndpoint(req.Path)
	if req.Query != "" {
		endpoint += "?" + req.Query
	}
	httpReq, err := http.NewRequestWithContext(ctx, method, endpoint, bytes.NewReader(req.Body))
	if err != nil {
		return nil, fmt.Errorf("failed to create raw request: %w", err)
	}
	p.anthropicSetForwardHeaders(httpReq.Header, req.Header)

	resp, err := p.httpClient.Do(httpReq)
	if err != nil {
		return nil, fmt.Errorf("failed to send raw request: %w", err)
	}
	defer resp.Body.Close()

	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("failed to read raw response: %w", err)
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, &UpstreamHTTPError{StatusCode: resp.StatusCode, Body: respBody}
	}
	return &RawResponse{
		StatusCode: resp.StatusCode,
		Header:     resp.Header.Clone(),
		Body:       respBody,
	}, nil
}

// ForwardStream sends a raw request to the Anthropic Messages API and returns
// the streaming response body unbuffered. It is the streaming counterpart of
// Forward and is used by the Responses → Anthropic stream conversion path.
func (p *AnthropicProvider) ForwardStream(ctx context.Context, req *RawRequest) (*RawStreamResponse, error) {
	if req == nil {
		return nil, fmt.Errorf("raw request is nil")
	}
	method := req.Method
	if method == "" {
		method = http.MethodPost
	}
	endpoint := p.anthropicForwardEndpoint(req.Path)
	if req.Query != "" {
		endpoint += "?" + req.Query
	}
	httpReq, err := http.NewRequestWithContext(ctx, method, endpoint, bytes.NewReader(req.Body))
	if err != nil {
		return nil, fmt.Errorf("failed to create raw stream request: %w", err)
	}
	p.anthropicSetForwardHeaders(httpReq.Header, req.Header)

	resp, err := p.httpClient.Do(httpReq)
	if err != nil {
		return nil, fmt.Errorf("failed to send raw stream request: %w", err)
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		defer resp.Body.Close()
		respBody, readErr := io.ReadAll(resp.Body)
		if readErr != nil {
			return nil, fmt.Errorf("failed to read raw stream response: %w", readErr)
		}
		return nil, &UpstreamHTTPError{StatusCode: resp.StatusCode, Body: respBody}
	}
	return &RawStreamResponse{
		StatusCode: resp.StatusCode,
		Header:     resp.Header.Clone(),
		Body:       resp.Body,
	}, nil
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
	InputTokens          int `json:"input_tokens"`
	OutputTokens         int `json:"output_tokens"`
	CacheReadInputTokens int `json:"cache_read_input_tokens"`
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
			PromptTokensDetails: UsageTokenDetails{
				CacheReadTokens: resp.Usage.CacheReadInputTokens,
			},
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

		var startUsage anthropicUsage
		scanner := bufio.NewScanner(resp.Body)
		// Anthropic SSE events (e.g. large base64 image blocks in
		// content_block_start) can far exceed bufio's default 64KB line limit;
		// allow up to 4MB per line, matching oauth_stream.go / responses_fallback.go.
		scanner.Buffer(make([]byte, 0, 64*1024), 4*1024*1024)
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
				logProviderWarn("failed to parse Anthropic SSE event",
					zap.Error(err),
					zap.String("data_preview", applogger.TruncateString(data, 100)),
				)
				continue
			}

			// message_start carries input-side usage (input_tokens and, for
			// Anthropic-protocol providers such as GLM, cache_read_input_tokens).
			if event.Type == "message_start" && event.Message != nil {
				startUsage = event.Message.Usage
				continue
			}

			// message_delta carries the final output token count. Emit a single
			// usage-only chunk so stream consumers can bill real tokens.
			//
			// Field-merge strategy: message_delta usually only carries
			// output_tokens; input_tokens and cache_read_input_tokens are
			// reported in message_start. When the delta event omits a field
			// (zero value) we back-fill from startUsage so the emitted chunk
			// always carries the full picture. When the delta event *does*
			// include a field (some Anthropic-compatible providers send the
			// final input_tokens here), the delta value wins — it reflects the
			// actual billed count after upstream adjustments.
			if event.Type == "message_delta" && event.Usage != nil {
				usage := event.Usage
				if usage.InputTokens == 0 {
					usage.InputTokens = startUsage.InputTokens
				}
				if usage.CacheReadInputTokens == 0 {
					usage.CacheReadInputTokens = startUsage.CacheReadInputTokens
				}
				finishReason := "stop"
				chunkChan <- StreamChunk{
					Object: "chat.completion.chunk",
					Model:  req.Model,
					Choices: []StreamChoice{
						{Index: 0, FinishReason: &finishReason},
					},
					Usage: Usage{
						PromptTokens:     usage.InputTokens,
						CompletionTokens: usage.OutputTokens,
						TotalTokens:      usage.InputTokens + usage.OutputTokens,
						PromptTokensDetails: UsageTokenDetails{
							CacheReadTokens: usage.CacheReadInputTokens,
						},
					},
				}
				continue
			}

			// Convert content_block_delta to StreamChunk
			if event.Type == "content_block_delta" && event.Delta != nil {
				chunk := StreamChunk{
					ID:      "",
					Object:  "chat.completion.chunk",
					Created: time.Now().Unix(),
					Model:   req.Model,
					Choices: []StreamChoice{
						{
							Index: 0,
							Delta: StreamDelta{Content: event.Delta.Text},
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
			logProviderError("Anthropic stream scanner error", zap.Error(err))
		}
	}()

	return chunkChan, nil
}

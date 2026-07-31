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

// GeminiProvider implements the Provider interface for Google Gemini API.
// It translates between OpenAI-compatible requests/responses and the Gemini API format.
type GeminiProvider struct {
	httpClient   *http.Client
	streamClient *http.Client // no Client.Timeout; streams rely on ctx deadline (domain-H3)
	baseURL      string
	apiKey       string
	timeout      time.Duration
}

// NewGeminiProvider creates a new Google Gemini provider.
// domain-M3: like the OpenAI/Azure/VoyageAI constructors it now validates the
// base URL against SSRF; previously a malicious Gemini channel record could
// point relay traffic at an internal/metadata endpoint.
func NewGeminiProvider(baseURL, apiKey string, timeout time.Duration) (*GeminiProvider, error) {
	if timeout == 0 {
		timeout = 30 * time.Second
	}
	if baseURL == "" {
		baseURL = "https://generativelanguage.googleapis.com"
	}
	if err := validateBaseURL(baseURL); err != nil {
		return nil, fmt.Errorf("invalid gemini base URL: %w", err)
	}
	return &GeminiProvider{
		httpClient: &http.Client{Timeout: timeout},
		// domain-H3: streaming client has no Client.Timeout so SSE body reads
		// are not killed mid-stream; cancellation is driven by the request ctx.
		streamClient: &http.Client{},
		baseURL:      baseURL,
		apiKey:       apiKey,
		timeout:      timeout,
	}, nil
}

// Forward is not supported for Gemini because non-chat OpenAI-compatible
// endpoints require endpoint-specific request and response conversion.
func (p *GeminiProvider) Forward(ctx context.Context, req *RawRequest) (*RawResponse, error) {
	return nil, fmt.Errorf("raw forwarding is not supported by gemini provider")
}

func (p *GeminiProvider) ForwardStream(ctx context.Context, req *RawRequest) (*RawStreamResponse, error) {
	return nil, fmt.Errorf("raw stream forwarding is not supported by gemini provider")
}

// Gemini API request/response structures

type geminiRequest struct {
	Contents         []geminiContent         `json:"contents"`
	GenerationConfig *geminiGenerationConfig `json:"generationConfig,omitempty"`
}

type geminiContent struct {
	Role  string       `json:"role"`
	Parts []geminiPart `json:"parts"`
}

type geminiPart struct {
	Text string `json:"text"`
}

type geminiGenerationConfig struct {
	Temperature     *float64 `json:"temperature,omitempty"`
	MaxOutputTokens *int     `json:"maxOutputTokens,omitempty"`
}

type geminiResponse struct {
	Candidates    []geminiCandidate   `json:"candidates"`
	UsageMetadata geminiUsageMetadata `json:"usageMetadata"`
}

type geminiCandidate struct {
	Content      geminiContent `json:"content"`
	FinishReason string        `json:"finishReason"`
}

type geminiUsageMetadata struct {
	PromptTokenCount     int `json:"promptTokenCount"`
	CandidatesTokenCount int `json:"candidatesTokenCount"`
	TotalTokenCount      int `json:"totalTokenCount"`
}

// Gemini SSE stream wrapper
type geminiStreamWrapper struct {
	Candidates    []geminiCandidate   `json:"candidates"`
	UsageMetadata geminiUsageMetadata `json:"usageMetadata"`
}

// convertToGeminiRequest converts an OpenAI-style request to Gemini format.
func convertToGeminiRequest(req *ChatCompletionsRequest) *geminiRequest {
	geminiReq := &geminiRequest{
		Contents: make([]geminiContent, 0, len(req.Messages)),
	}

	for _, msg := range req.Messages {
		role := "user"
		if msg.Role == "assistant" {
			role = "model"
		}
		// Gemini does not support system role in contents; treat as user
		geminiReq.Contents = append(geminiReq.Contents, geminiContent{
			Role:  role,
			Parts: []geminiPart{{Text: msg.Content}},
		})
	}

	if req.Temperature != nil || req.MaxTokens != nil {
		geminiReq.GenerationConfig = &geminiGenerationConfig{
			Temperature:     req.Temperature,
			MaxOutputTokens: req.MaxTokens,
		}
	}

	return geminiReq
}

// convertFromGeminiResponse converts a Gemini response to OpenAI format.
func convertFromGeminiResponse(resp *geminiResponse, model string) *ChatCompletionsResponse {
	content := ""
	finishReason := "stop"
	if len(resp.Candidates) > 0 {
		candidate := resp.Candidates[0]
		if len(candidate.Content.Parts) > 0 {
			content = candidate.Content.Parts[0].Text
		}
		switch candidate.FinishReason {
		case "MAX_TOKENS":
			finishReason = "length"
		default:
			finishReason = "stop"
		}
	}

	return &ChatCompletionsResponse{
		ID:      fmt.Sprintf("gemini-%d", time.Now().UnixMilli()),
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
			PromptTokens:     resp.UsageMetadata.PromptTokenCount,
			CompletionTokens: resp.UsageMetadata.CandidatesTokenCount,
			TotalTokens:      resp.UsageMetadata.TotalTokenCount,
		},
	}
}

// ChatCompletions sends a chat completions request to the Gemini API.
func (p *GeminiProvider) ChatCompletions(ctx context.Context, req *ChatCompletionsRequest) (*ChatCompletionsResponse, error) {
	geminiReq := convertToGeminiRequest(req)

	body, err := sonic.Marshal(geminiReq)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal request: %w", err)
	}

	url := fmt.Sprintf("%s/v1beta/models/%s:generateContent", p.baseURL, req.Model)
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("failed to create request: %w", err)
	}
	httpReq.Header.Set("Content-Type", "application/json")
	httpReq.Header.Set("x-goog-api-key", p.apiKey)

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
		return nil, &UpstreamHTTPError{StatusCode: resp.StatusCode, Body: respBody} // domain-L4
	}

	var geminiResp geminiResponse
	if err := sonic.Unmarshal(respBody, &geminiResp); err != nil {
		return nil, fmt.Errorf("failed to unmarshal response: %w", err)
	}

	return convertFromGeminiResponse(&geminiResp, req.Model), nil
}

// ChatCompletionsStream sends a streaming request to the Gemini API.
func (p *GeminiProvider) ChatCompletionsStream(ctx context.Context, req *ChatCompletionsRequest) (<-chan StreamChunk, error) {
	geminiReq := convertToGeminiRequest(req)

	body, err := sonic.Marshal(geminiReq)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal request: %w", err)
	}

	url := fmt.Sprintf("%s/v1beta/models/%s:streamGenerateContent?alt=sse", p.baseURL, req.Model)
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("failed to create request: %w", err)
	}
	httpReq.Header.Set("Content-Type", "application/json")
	httpReq.Header.Set("x-goog-api-key", p.apiKey)

	resp, err := p.streamClient.Do(httpReq)
	if err != nil {
		return nil, fmt.Errorf("failed to send request: %w", err)
	}

	if resp.StatusCode != http.StatusOK {
		respBody, _ := io.ReadAll(resp.Body)
		resp.Body.Close()
		return nil, &UpstreamHTTPError{StatusCode: resp.StatusCode, Body: respBody} // domain-L4
	}

	chunkChan := make(chan StreamChunk, 10)

	go func() {
		defer close(chunkChan)
		defer resp.Body.Close()

		scanner := bufio.NewScanner(resp.Body)
		// Gemini SSE lines can carry large inline_data base64; raise
		// bufio's 64KB default to 4MB, matching the Anthropic reader.
		scanner.Buffer(make([]byte, 0, 64*1024), 4*1024*1024)
		for scanner.Scan() {
			line := scanner.Text()
			if line == "" {
				continue
			}

			data, ok := strings.CutPrefix(line, "data: ")
			if !ok {
				continue
			}

			var wrapper geminiStreamWrapper
			if err := sonic.Unmarshal([]byte(data), &wrapper); err != nil {
				logProviderWarn("failed to parse Gemini SSE chunk",
					zap.Error(err),
					zap.String("data_preview", applogger.TruncateString(data, 100)),
				)
				continue
			}

			if len(wrapper.Candidates) > 0 && len(wrapper.Candidates[0].Content.Parts) > 0 {
				text := wrapper.Candidates[0].Content.Parts[0].Text
				if text == "" {
					continue
				}
				chunk := StreamChunk{
					ID:      fmt.Sprintf("gemini-%d", time.Now().UnixMilli()),
					Object:  "chat.completion.chunk",
					Created: time.Now().Unix(),
					Model:   req.Model,
					Choices: []StreamChoice{
						{
							Index: 0,
							Delta: StreamDelta{Content: text},
						},
					},
				}
				chunkChan <- chunk
			}
		}

		if err := scanner.Err(); err != nil {
			logProviderError("Gemini stream scanner error", zap.Error(err))
		}
	}()

	return chunkChan, nil
}

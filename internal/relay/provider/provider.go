package provider

import (
	"bufio"
	"bytes"
	"context"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"strings"
	"time"

	"github.com/bytedance/sonic"
	"go.uber.org/zap"
	applogger "micro-one-api/internal/pkg/logger"
)

// Provider defines the interface for calling upstream providers
type Provider interface {
	ChatCompletions(ctx context.Context, req *ChatCompletionsRequest) (*ChatCompletionsResponse, error)
	ChatCompletionsStream(ctx context.Context, req *ChatCompletionsRequest) (<-chan StreamChunk, error)
	Forward(ctx context.Context, req *RawRequest) (*RawResponse, error)
	ForwardStream(ctx context.Context, req *RawRequest) (*RawStreamResponse, error)
}

// RawRequest represents an API request that should be forwarded without
// endpoint-specific schema conversion.
type RawRequest struct {
	Method string
	Path   string
	Query  string
	Header http.Header
	Body   []byte
}

// RawResponse is the upstream response for a forwarded raw request.
type RawResponse struct {
	StatusCode int
	Header     http.Header
	Body       []byte
}

// RawStreamResponse is the upstream streaming response for a forwarded raw
// request. The caller owns Body and must close it.
type RawStreamResponse struct {
	StatusCode int
	Header     http.Header
	Body       io.ReadCloser
}

// UpstreamHTTPError preserves the upstream status and response body for callers
// that need endpoint-specific fallback behavior.
type UpstreamHTTPError struct {
	StatusCode int
	Body       []byte
}

func (e *UpstreamHTTPError) Error() string {
	return fmt.Sprintf("upstream error: status=%d, body=%s", e.StatusCode, string(e.Body))
}

// ChatCompletionsRequest represents a standardized chat completions request
type ChatCompletionsRequest struct {
	Model       string    `json:"model"`
	Messages    []Message `json:"messages"`
	Stream      bool      `json:"stream"`
	Temperature *float64  `json:"temperature,omitempty"`
	MaxTokens   *int      `json:"max_tokens,omitempty"`
}

// Message represents a chat message.
//
// ReasoningContent is a passthrough field for upstream "thinking mode" responses
// (e.g. DeepSeek-R1, Xiaomi MiMo). The relay does not interpret it; clients that
// receive it should echo it back on the next turn for upstreams that require it.
type Message struct {
	Role             string `json:"role"`
	Content          string `json:"content"`
	ReasoningContent any    `json:"reasoning_content,omitempty"`
}

// ChatCompletionsResponse represents a standardized chat completions response
type ChatCompletionsResponse struct {
	ID      string   `json:"id"`
	Object  string   `json:"object"`
	Created int64    `json:"created"`
	Model   string   `json:"model"`
	Choices []Choice `json:"choices"`
	Usage   Usage    `json:"usage"`
}

// Choice represents a completion choice
type Choice struct {
	Index        int     `json:"index"`
	Message      Message `json:"message"`
	FinishReason string  `json:"finish_reason"`
}

// Usage represents token usage information
type Usage struct {
	PromptTokens     int `json:"prompt_tokens"`
	CompletionTokens int `json:"completion_tokens"`
	TotalTokens      int `json:"total_tokens"`
}

// StreamChunk represents a single SSE chunk from streaming response
type StreamChunk struct {
	ID      string `json:"id"`
	Object  string `json:"object"`
	Created int64  `json:"created"`
	Model   string `json:"model"`
	Choices []struct {
		Index int `json:"index"`
		Delta struct {
			Role             string `json:"role,omitempty"`
			Content          string `json:"content,omitempty"`
			ReasoningContent any    `json:"reasoning_content,omitempty"`
		} `json:"delta"`
		FinishReason *string `json:"finish_reason,omitempty"`
	} `json:"choices"`
	Usage Usage `json:"usage,omitempty"`
}

// OpenAIProvider implements the Provider interface for OpenAI-compatible APIs
type OpenAIProvider struct {
	httpClient *http.Client
	baseURL    string
	apiKey     string
	timeout    time.Duration
}

// validateBaseURL checks that a base URL is safe from SSRF attacks.
// It rejects non-http(s) schemes and private/internal/reserved IP addresses.
// Set PROVIDER_DISABLE_SSRF_CHECK=true to bypass validation (for testing only).
func validateBaseURL(rawURL string) error {
	if os.Getenv("PROVIDER_DISABLE_SSRF_CHECK") == "true" {
		return nil
	}

	u, err := url.Parse(rawURL)
	if err != nil {
		return fmt.Errorf("invalid URL: %w", err)
	}

	scheme := strings.ToLower(u.Scheme)
	if scheme != "http" && scheme != "https" {
		return fmt.Errorf("URL scheme must be http or https, got: %s", scheme)
	}

	hostname := u.Hostname()
	if hostname == "" {
		return fmt.Errorf("URL has no hostname")
	}

	// Check for localhost
	lower := strings.ToLower(hostname)
	if lower == "localhost" || strings.HasSuffix(lower, ".localhost") {
		return fmt.Errorf("localhost URLs are not allowed")
	}

	// Resolve hostname to IP and check for private/reserved ranges
	ips, err := net.LookupIP(hostname)
	if err != nil {
		return fmt.Errorf("failed to resolve hostname: %w", err)
	}

	for _, ip := range ips {
		if isPrivateOrReservedIP(ip) {
			return fmt.Errorf("URL resolves to private/reserved IP: %s", ip)
		}
	}

	return nil
}

// isPrivateOrReservedIP checks if an IP address is in a private, loopback,
// link-local, or other reserved range.
func isPrivateOrReservedIP(ip net.IP) bool {
	return ip.IsLoopback() ||
		ip.IsLinkLocalUnicast() ||
		ip.IsLinkLocalMulticast() ||
		ip.IsPrivate() ||
		ip.IsUnspecified() ||
		// Cloud metadata endpoint (169.254.169.254)
		ip.Equal(net.IPv4(169, 254, 169, 254))
}

// NewOpenAIProvider creates a new OpenAI-compatible provider
func NewOpenAIProvider(baseURL, apiKey string, timeout time.Duration) (*OpenAIProvider, error) {
	if err := validateBaseURL(baseURL); err != nil {
		return nil, fmt.Errorf("invalid base URL: %w", err)
	}

	if timeout == 0 {
		timeout = 30 * time.Second
	}
	return &OpenAIProvider{
		httpClient: &http.Client{
			Timeout: timeout,
		},
		baseURL: baseURL,
		apiKey:  apiKey,
		timeout: timeout,
	}, nil
}

// Forward sends a raw OpenAI-compatible request to the upstream provider.
func (p *OpenAIProvider) Forward(ctx context.Context, req *RawRequest) (*RawResponse, error) {
	if req == nil {
		return nil, fmt.Errorf("raw request is nil")
	}
	method := req.Method
	if method == "" {
		method = http.MethodPost
	}

	upstreamURL := strings.TrimRight(p.baseURL, "/") + "/" + strings.TrimLeft(req.Path, "/")
	if req.Query != "" {
		upstreamURL += "?" + req.Query
	}

	httpReq, err := http.NewRequestWithContext(ctx, method, upstreamURL, bytes.NewReader(req.Body))
	if err != nil {
		return nil, fmt.Errorf("failed to create raw request: %w", err)
	}
	copyForwardHeaders(httpReq.Header, req.Header)
	httpReq.Header.Set("Authorization", "Bearer "+p.apiKey)

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

func copyForwardHeaders(dst http.Header, src http.Header) {
	for key, values := range src {
		if isHopByHopHeader(key) || strings.EqualFold(key, "Authorization") {
			continue
		}
		for _, value := range values {
			dst.Add(key, value)
		}
	}
}

// ForwardStream sends a raw OpenAI-compatible request and returns the upstream
// response body without buffering it.
func (p *OpenAIProvider) ForwardStream(ctx context.Context, req *RawRequest) (*RawStreamResponse, error) {
	if req == nil {
		return nil, fmt.Errorf("raw request is nil")
	}
	method := req.Method
	if method == "" {
		method = http.MethodPost
	}

	upstreamURL := strings.TrimRight(p.baseURL, "/") + "/" + strings.TrimLeft(req.Path, "/")
	if req.Query != "" {
		upstreamURL += "?" + req.Query
	}

	httpReq, err := http.NewRequestWithContext(ctx, method, upstreamURL, bytes.NewReader(req.Body))
	if err != nil {
		return nil, fmt.Errorf("failed to create raw request: %w", err)
	}
	copyForwardHeaders(httpReq.Header, req.Header)
	httpReq.Header.Set("Authorization", "Bearer "+p.apiKey)

	resp, err := p.httpClient.Do(httpReq)
	if err != nil {
		return nil, fmt.Errorf("failed to send raw request: %w", err)
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		defer resp.Body.Close()
		respBody, readErr := io.ReadAll(resp.Body)
		if readErr != nil {
			return nil, fmt.Errorf("failed to read raw response: %w", readErr)
		}
		return nil, &UpstreamHTTPError{StatusCode: resp.StatusCode, Body: respBody}
	}

	return &RawStreamResponse{
		StatusCode: resp.StatusCode,
		Header:     resp.Header.Clone(),
		Body:       resp.Body,
	}, nil
}

func isHopByHopHeader(key string) bool {
	switch strings.ToLower(key) {
	case "connection", "keep-alive", "proxy-authenticate", "proxy-authorization",
		"te", "trailer", "transfer-encoding", "upgrade":
		return true
	default:
		return false
	}
}

// ChatCompletions sends a chat completions request to the upstream provider
func (p *OpenAIProvider) ChatCompletions(ctx context.Context, req *ChatCompletionsRequest) (*ChatCompletionsResponse, error) {
	url := fmt.Sprintf("%s/chat/completions", p.baseURL)

	body, err := sonic.Marshal(req)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal request: %w", err)
	}

	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("failed to create request: %w", err)
	}

	httpReq.Header.Set("Content-Type", "application/json")
	httpReq.Header.Set("Authorization", "Bearer "+p.apiKey)

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
		return nil, &UpstreamHTTPError{StatusCode: resp.StatusCode, Body: respBody}
	}

	var response ChatCompletionsResponse
	if err := sonic.Unmarshal(respBody, &response); err != nil {
		return nil, fmt.Errorf("failed to unmarshal response: %w", err)
	}

	return &response, nil
}

// ChatCompletionsStream sends a streaming chat completions request to upstream provider
func (p *OpenAIProvider) ChatCompletionsStream(ctx context.Context, req *ChatCompletionsRequest) (<-chan StreamChunk, error) {
	url := fmt.Sprintf("%s/chat/completions", p.baseURL)

	body, err := sonic.Marshal(req)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal request: %w", err)
	}

	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("failed to create request: %w", err)
	}

	httpReq.Header.Set("Content-Type", "application/json")
	httpReq.Header.Set("Authorization", "Bearer "+p.apiKey)

	resp, err := p.httpClient.Do(httpReq)
	if err != nil {
		return nil, fmt.Errorf("failed to send request: %w", err)
	}

	if resp.StatusCode != http.StatusOK {
		respBody, _ := io.ReadAll(resp.Body)
		resp.Body.Close()
		return nil, &UpstreamHTTPError{StatusCode: resp.StatusCode, Body: respBody}
	}

	return readOpenAIStream(resp), nil
}

func readOpenAIStream(resp *http.Response) <-chan StreamChunk {
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

			if data, ok := strings.CutPrefix(line, "data: "); ok {
				if data == "[DONE]" {
					break
				}

				var chunk StreamChunk
				if err := sonic.Unmarshal([]byte(data), &chunk); err != nil {
					applogger.Log.Warn("failed to parse SSE chunk",
						zap.Error(err),
						zap.Int("data_length", len(data)),
						zap.String("data_preview", applogger.TruncateString(data, 100)),
					)
					continue
				}
				chunkChan <- chunk
			}
		}

		if err := scanner.Err(); err != nil {
			applogger.Log.Error("scanner error", zap.Error(err))
		}
	}()

	return chunkChan
}

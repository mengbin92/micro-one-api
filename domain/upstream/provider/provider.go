package provider

import (
	"bufio"
	"bytes"
	"context"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/netip"
	"net/url"
	"os"
	"strings"
	"time"

	"go.uber.org/zap"
	"micro-one-api/pkg/jsonx"
	applogger "micro-one-api/platform/logging"
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

// MaxUpstreamResponseBody caps how many bytes of an upstream *success*
// response body the relay will buffer into memory. 128MB mirrors the
// inbound request cap (64MB) with headroom for large model outputs; an
// upstream exceeding this is treated as malformed (relay-C1: unbounded
// io.ReadAll previously allowed a hostile/buggy upstream to OOM the gateway).
const MaxUpstreamResponseBody = 128 * 1024 * 1024

// MaxUpstreamErrorBody caps an upstream *error* response body. Error bodies
// are only used for diagnostics/status mapping and must never be large, so
// this is far tighter than the success cap (relay-C1).
const MaxUpstreamErrorBody = 1 << 20

// ChatCompletionsRequest represents a standardized chat completions request
type ChatCompletionsRequest struct {
	Model               string    `json:"model"`
	Messages            []Message `json:"messages"`
	Stream              bool      `json:"stream"`
	Temperature         *float64  `json:"temperature,omitempty"`
	MaxTokens           *int      `json:"max_tokens,omitempty"`
	MaxCompletionTokens *int      `json:"max_completion_tokens,omitempty"`
	ReasoningEffort     string    `json:"reasoning_effort,omitempty"`
	Tools               any       `json:"tools,omitempty"`
	ToolChoice          any       `json:"tool_choice,omitempty"`
}

// Message represents a chat message.
//
// ReasoningContent is a passthrough field for upstream "thinking mode" responses
// (e.g. DeepSeek-R1, Xiaomi MiMo). The relay does not interpret it; clients that
// receive it should echo it back on the next turn for upstreams that require it.
type Message struct {
	Role             string     `json:"role"`
	Content          string     `json:"content"`
	ReasoningContent any        `json:"reasoning_content,omitempty"`
	ToolCalls        []ToolCall `json:"tool_calls,omitempty"`
	ToolCallID       string     `json:"tool_call_id,omitempty"`
}

type ToolCall struct {
	ID       string           `json:"id,omitempty"`
	Type     string           `json:"type,omitempty"`
	Function ToolCallFunction `json:"function,omitempty"`
}

type ToolCallFunction struct {
	Name      string `json:"name,omitempty"`
	Arguments string `json:"arguments,omitempty"`
}

type ToolCallDelta struct {
	Index    *int             `json:"index,omitempty"`
	ID       string           `json:"id,omitempty"`
	Type     string           `json:"type,omitempty"`
	Function ToolCallFunction `json:"function,omitempty"`
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
	PromptTokens        int               `json:"prompt_tokens"`
	CompletionTokens    int               `json:"completion_tokens"`
	TotalTokens         int               `json:"total_tokens"`
	PromptTokensDetails UsageTokenDetails `json:"prompt_tokens_details,omitempty"`
	InputTokensDetails  UsageTokenDetails `json:"input_tokens_details,omitempty"`
}

type UsageTokenDetails struct {
	CachedTokens          int `json:"cached_tokens,omitempty"`
	CacheReadTokens       int `json:"cache_read_tokens,omitempty"`
	CacheCreation5mTokens int `json:"cache_creation_5m_tokens,omitempty"`
	CacheCreation1hTokens int `json:"cache_creation_1h_tokens,omitempty"`
}

// StreamChunk represents a single SSE chunk from streaming response
type StreamChunk struct {
	ID      string         `json:"id"`
	Object  string         `json:"object"`
	Created int64          `json:"created"`
	Model   string         `json:"model"`
	Choices []StreamChoice `json:"choices"`
	Usage   Usage          `json:"usage,omitempty"`
}

type StreamChoice struct {
	Index        int         `json:"index"`
	Delta        StreamDelta `json:"delta"`
	FinishReason *string     `json:"finish_reason,omitempty"`
}

type StreamDelta struct {
	Role             string          `json:"role,omitempty"`
	Content          string          `json:"content,omitempty"`
	ReasoningContent any             `json:"reasoning_content,omitempty"`
	ToolCalls        []ToolCallDelta `json:"tool_calls,omitempty"`
}

// OpenAIProvider implements the Provider interface for OpenAI-compatible APIs
type OpenAIProvider struct {
	httpClient   *http.Client
	streamClient *http.Client // no hard total timeout; response-header + idle timeouts are sliding
	baseURL      string
	apiKey       string
	timeout      time.Duration
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

// ValidateBaseURL checks that an upstream URL is safe to use for an outbound
// request. It is shared by provider-backed and adaptor-backed request paths.
func ValidateBaseURL(rawURL string) error {
	return validateBaseURL(rawURL)
}

// ValidateBaseURLForChannel validates an adaptor-built upstream URL while
// preserving the explicit local-network allowance of self-hosted channels.
func ValidateBaseURLForChannel(channelType int32, rawURL string) error {
	if channelType == ChannelTypeOllama {
		return validateBaseURLAllowLocal(rawURL)
	}
	return validateBaseURL(rawURL)
}

// validateBaseURLAllowLocal is the local/self-hosted variant of validateBaseURL.
// It keeps the scheme check (http/https only) and hostname requirement but
// permits loopback and private IP ranges, because self-hosted providers such as
// Ollama legitimately run on localhost or an internal network. It is used only
// for channel types whose default URL is inherently local (domain-M2).
func validateBaseURLAllowLocal(rawURL string) error {
	u, err := url.Parse(rawURL)
	if err != nil {
		return fmt.Errorf("invalid URL: %w", err)
	}
	scheme := strings.ToLower(u.Scheme)
	if scheme != "http" && scheme != "https" {
		return fmt.Errorf("URL scheme must be http or https, got: %s", scheme)
	}
	if u.Hostname() == "" {
		return fmt.Errorf("URL has no hostname")
	}
	return nil
}

// NewOpenAIProviderAllowLocal creates an OpenAI-compatible provider whose base
// URL is validated with validateBaseURLAllowLocal instead of the strict SSRF
// check. It is intended for self-hosted/local channel types (e.g. Ollama) whose
// default endpoint is loopback or a private address (domain-M2).
func NewOpenAIProviderAllowLocal(baseURL, apiKey string, timeout time.Duration) (*OpenAIProvider, error) {
	if err := validateBaseURLAllowLocal(baseURL); err != nil {
		return nil, fmt.Errorf("invalid base URL: %w", err)
	}
	if timeout == 0 {
		timeout = 30 * time.Second
	}
	return &OpenAIProvider{
		httpClient:   newHTTPClient(timeout, true),
		streamClient: newStreamHTTPClientWithLocalAccess(timeout, true),
		baseURL:      baseURL,
		apiKey:       apiKey,
		timeout:      timeout,
	}, nil
}

// isPrivateOrReservedIP checks if an IP address is in a private, loopback,
// link-local, or other reserved range.
var nonPublicPrefixes = []netip.Prefix{
	netip.MustParsePrefix("100.64.0.0/10"),
	netip.MustParsePrefix("192.0.0.0/24"),
	netip.MustParsePrefix("192.0.2.0/24"),
	netip.MustParsePrefix("198.18.0.0/15"),
	netip.MustParsePrefix("198.51.100.0/24"),
	netip.MustParsePrefix("203.0.113.0/24"),
	netip.MustParsePrefix("240.0.0.0/4"),
	netip.MustParsePrefix("2001:db8::/32"),
}

func isPrivateOrReservedIP(ip net.IP) bool {
	addr, ok := netip.AddrFromSlice(ip)
	if !ok {
		return true
	}
	addr = addr.Unmap()
	if !addr.IsGlobalUnicast() || addr.IsPrivate() || addr.IsLoopback() || addr.IsLinkLocalUnicast() || addr.IsUnspecified() {
		return true
	}
	for _, prefix := range nonPublicPrefixes {
		if prefix.Contains(addr) {
			return true
		}
	}
	return false
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
		httpClient: newHTTPClient(timeout, false),
		// domain-H3: the streaming client has NO Client.Timeout. http.Client.Timeout
		// is a hard deadline covering the entire round trip including response-body
		// reads, so it would kill an SSE stream mid-flight once the configured
		// timeout elapsed regardless of whether bytes were still flowing. The custom
		// client instead applies the timeout to response headers and idle periods.
		streamClient: newStreamHTTPClient(timeout),
		baseURL:      baseURL,
		apiKey:       apiKey,
		timeout:      timeout,
	}, nil
}

// Forward sends a raw OpenAI-compatible request to the upstream provider.
func (p *OpenAIProvider) Forward(ctx context.Context, req *RawRequest) (*RawResponse, error) {
	if req == nil {
		return nil, fmt.Errorf("raw request is nil")
	}
	body := req.Body
	if isChatCompletionsPath(req.Path) {
		normalized, err := normalizeKimiK3ChatBody(body)
		if err != nil {
			return nil, fmt.Errorf("normalize Kimi K3 request: %w", err)
		}
		body = normalized
	}
	method := req.Method
	if method == "" {
		method = http.MethodPost
	}

	upstreamURL := strings.TrimRight(p.baseURL, "/") + "/" + strings.TrimLeft(req.Path, "/")
	if req.Query != "" {
		upstreamURL += "?" + req.Query
	}

	httpReq, err := http.NewRequestWithContext(ctx, method, upstreamURL, bytes.NewReader(body))
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

	respBody, err := io.ReadAll(io.LimitReader(resp.Body, MaxUpstreamResponseBody))
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
	body := req.Body
	if isChatCompletionsPath(req.Path) {
		normalized, err := normalizeKimiK3ChatBody(body)
		if err != nil {
			return nil, fmt.Errorf("normalize Kimi K3 request: %w", err)
		}
		body = normalized
	}
	method := req.Method
	if method == "" {
		method = http.MethodPost
	}

	upstreamURL := strings.TrimRight(p.baseURL, "/") + "/" + strings.TrimLeft(req.Path, "/")
	if req.Query != "" {
		upstreamURL += "?" + req.Query
	}

	httpReq, err := http.NewRequestWithContext(ctx, method, upstreamURL, bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("failed to create raw request: %w", err)
	}
	copyForwardHeaders(httpReq.Header, req.Header)
	httpReq.Header.Set("Authorization", "Bearer "+p.apiKey)

	resp, err := p.streamClient.Do(httpReq)
	if err != nil {
		return nil, fmt.Errorf("failed to send raw request: %w", err)
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		defer resp.Body.Close()
		respBody, readErr := io.ReadAll(io.LimitReader(resp.Body, MaxUpstreamErrorBody))
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
	req = normalizeKimiK3ChatRequest(req)

	body, err := jsonx.Marshal(req)
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

	respBody, err := io.ReadAll(io.LimitReader(resp.Body, MaxUpstreamResponseBody))
	if err != nil {
		return nil, fmt.Errorf("failed to read response: %w", err)
	}

	if resp.StatusCode != http.StatusOK {
		return nil, &UpstreamHTTPError{StatusCode: resp.StatusCode, Body: respBody}
	}

	var response ChatCompletionsResponse
	if err := jsonx.Unmarshal(respBody, &response); err != nil {
		return nil, fmt.Errorf("failed to unmarshal response: %w", err)
	}

	return &response, nil
}

// ChatCompletionsStream sends a streaming chat completions request to upstream provider
func (p *OpenAIProvider) ChatCompletionsStream(ctx context.Context, req *ChatCompletionsRequest) (<-chan StreamChunk, error) {
	url := fmt.Sprintf("%s/chat/completions", p.baseURL)
	req = normalizeKimiK3ChatRequest(req)

	body, err := jsonx.Marshal(req)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal request: %w", err)
	}

	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("failed to create request: %w", err)
	}

	httpReq.Header.Set("Content-Type", "application/json")
	httpReq.Header.Set("Authorization", "Bearer "+p.apiKey)

	resp, err := p.streamClient.Do(httpReq)
	if err != nil {
		return nil, fmt.Errorf("failed to send request: %w", err)
	}

	if resp.StatusCode != http.StatusOK {
		respBody, _ := io.ReadAll(io.LimitReader(resp.Body, MaxUpstreamErrorBody))
		_ = resp.Body.Close()
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
		// OpenAI SSE lines can carry large tool-call payloads; raise
		// bufio's 64KB default to 4MB, matching the Anthropic reader.
		scanner.Buffer(make([]byte, 0, 64*1024), 4*1024*1024)
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
				if err := jsonx.Unmarshal([]byte(data), &chunk); err != nil {
					logProviderWarn("failed to parse SSE chunk",
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
			logProviderError("scanner error", zap.Error(err))
		}
	}()

	return chunkChan
}

func logProviderWarn(msg string, fields ...zap.Field) {
	applogger.Log.Warn(msg, fields...)
}

func logProviderError(msg string, fields ...zap.Field) {
	applogger.Log.Error(msg, fields...)
}

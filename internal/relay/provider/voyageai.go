package provider

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/bytedance/sonic"
)

type VoyageAIProvider struct {
	httpClient *http.Client
	baseURL    string
	apiKey     string
	timeout    time.Duration
}

func NewVoyageAIProvider(baseURL, apiKey string, timeout time.Duration) (*VoyageAIProvider, error) {
	if err := validateBaseURL(baseURL); err != nil {
		return nil, fmt.Errorf("invalid base URL: %w", err)
	}
	if timeout == 0 {
		timeout = 30 * time.Second
	}
	return &VoyageAIProvider{
		httpClient: &http.Client{Timeout: timeout},
		baseURL:    strings.TrimRight(baseURL, "/"),
		apiKey:     apiKey,
		timeout:    timeout,
	}, nil
}

func (p *VoyageAIProvider) ChatCompletions(ctx context.Context, req *ChatCompletionsRequest) (*ChatCompletionsResponse, error) {
	return nil, fmt.Errorf("voyageai chat completions are not supported")
}

func (p *VoyageAIProvider) ChatCompletionsStream(ctx context.Context, req *ChatCompletionsRequest) (<-chan StreamChunk, error) {
	return nil, fmt.Errorf("voyageai chat completions stream is not supported")
}

func (p *VoyageAIProvider) Forward(ctx context.Context, req *RawRequest) (*RawResponse, error) {
	if req == nil {
		return nil, fmt.Errorf("raw request is nil")
	}
	path := "/" + strings.TrimLeft(req.Path, "/")
	if path != "/embeddings" && path != "/v1/embeddings" {
		return nil, fmt.Errorf("voyageai raw path %s is not supported", req.Path)
	}
	method := req.Method
	if method == "" {
		method = http.MethodPost
	}
	if method != http.MethodPost {
		return nil, fmt.Errorf("voyageai embeddings require POST")
	}

	upstreamURL := p.baseURL + "/embeddings"
	if req.Query != "" {
		upstreamURL += "?" + req.Query
	}
	httpReq, err := http.NewRequestWithContext(ctx, method, upstreamURL, bytes.NewReader(req.Body))
	if err != nil {
		return nil, fmt.Errorf("failed to create voyageai request: %w", err)
	}
	copyForwardHeaders(httpReq.Header, req.Header)
	httpReq.Header.Set("Content-Type", "application/json")
	httpReq.Header.Set("Authorization", "Bearer "+p.apiKey)

	resp, err := p.httpClient.Do(httpReq)
	if err != nil {
		return nil, fmt.Errorf("failed to send voyageai request: %w", err)
	}
	defer resp.Body.Close()

	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("failed to read voyageai response: %w", err)
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, fmt.Errorf("upstream error: status=%d, body=%s", resp.StatusCode, string(respBody))
	}
	body := normalizeVoyageAIEmbeddingResponse(respBody)
	return &RawResponse{StatusCode: resp.StatusCode, Header: resp.Header.Clone(), Body: body}, nil
}

func normalizeVoyageAIEmbeddingResponse(body []byte) []byte {
	var payload map[string]interface{}
	if err := sonic.Unmarshal(body, &payload); err != nil {
		return body
	}
	usage, ok := payload["usage"].(map[string]interface{})
	if !ok {
		return body
	}
	total, ok := usage["total_tokens"]
	if !ok {
		return body
	}
	if _, exists := usage["prompt_tokens"]; !exists {
		usage["prompt_tokens"] = total
	}
	encoded, err := sonic.Marshal(payload)
	if err != nil {
		return body
	}
	return encoded
}

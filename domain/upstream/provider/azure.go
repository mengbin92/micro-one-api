package provider

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"micro-one-api/pkg/jsonx"
)

const defaultAzureAPIVersion = "2024-02-15-preview"

type AzureProvider struct {
	httpClient   *http.Client
	streamClient *http.Client // no hard total timeout; response-header + idle timeouts are sliding
	baseURL      string
	apiKey       string
	apiVersion   string
	timeout      time.Duration
}

func NewAzureProvider(baseURL, apiKey, apiVersion string, timeout time.Duration) (*AzureProvider, error) {
	if err := validateBaseURL(baseURL); err != nil {
		return nil, fmt.Errorf("invalid base URL: %w", err)
	}
	if timeout == 0 {
		timeout = 30 * time.Second
	}
	if strings.TrimSpace(apiVersion) == "" {
		apiVersion = defaultAzureAPIVersion
	}
	return &AzureProvider{
		httpClient: &http.Client{Timeout: timeout},
		// Keep active SSE streams alive beyond timeout while bounding response-header
		// waits and periods with no upstream bytes.
		streamClient: newStreamHTTPClient(timeout),
		baseURL:      strings.TrimRight(baseURL, "/"),
		apiKey:       apiKey,
		apiVersion:   apiVersion,
		timeout:      timeout,
	}, nil
}

func (p *AzureProvider) ChatCompletions(ctx context.Context, req *ChatCompletionsRequest) (*ChatCompletionsResponse, error) {
	if req == nil {
		return nil, fmt.Errorf("chat completions request is nil")
	}
	endpoint, err := p.endpoint(req.Model, "/chat/completions", "")
	if err != nil {
		return nil, err
	}
	body, err := azureChatBody(req)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal request: %w", err)
	}
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("failed to create request: %w", err)
	}
	p.setHeaders(httpReq.Header, nil)

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
		return nil, &UpstreamHTTPError{StatusCode: resp.StatusCode, Body: respBody} // domain-L4
	}
	var response ChatCompletionsResponse
	if err := jsonx.Unmarshal(respBody, &response); err != nil {
		return nil, fmt.Errorf("failed to unmarshal response: %w", err)
	}
	return &response, nil
}

func (p *AzureProvider) ChatCompletionsStream(ctx context.Context, req *ChatCompletionsRequest) (<-chan StreamChunk, error) {
	if req == nil {
		return nil, fmt.Errorf("chat completions request is nil")
	}
	endpoint, err := p.endpoint(req.Model, "/chat/completions", "")
	if err != nil {
		return nil, err
	}
	body, err := azureChatBody(req)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal request: %w", err)
	}
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("failed to create request: %w", err)
	}
	p.setHeaders(httpReq.Header, nil)

	resp, err := p.streamClient.Do(httpReq)
	if err != nil {
		return nil, fmt.Errorf("failed to send request: %w", err)
	}
	if resp.StatusCode != http.StatusOK {
		respBody, _ := io.ReadAll(io.LimitReader(resp.Body, MaxUpstreamErrorBody))
		_ = resp.Body.Close()
		return nil, &UpstreamHTTPError{StatusCode: resp.StatusCode, Body: respBody} // domain-L4
	}
	return readOpenAIStream(resp), nil
}

func (p *AzureProvider) Forward(ctx context.Context, req *RawRequest) (*RawResponse, error) {
	if req == nil {
		return nil, fmt.Errorf("raw request is nil")
	}
	method := req.Method
	if method == "" {
		method = http.MethodPost
	}
	deployment := extractDeploymentFromRawBody(req.Body)
	endpoint, err := p.endpoint(deployment, req.Path, req.Query)
	if err != nil {
		return nil, err
	}
	body := removeModelFromRawBody(req.Body)
	httpReq, err := http.NewRequestWithContext(ctx, method, endpoint, bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("failed to create raw request: %w", err)
	}
	p.setHeaders(httpReq.Header, req.Header)

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
		return nil, &UpstreamHTTPError{StatusCode: resp.StatusCode, Body: respBody} // domain-L4
	}
	return &RawResponse{StatusCode: resp.StatusCode, Header: resp.Header.Clone(), Body: respBody}, nil
}

func (p *AzureProvider) ForwardStream(ctx context.Context, req *RawRequest) (*RawStreamResponse, error) {
	return nil, fmt.Errorf("raw stream forwarding is not supported by azure provider")
}

func (p *AzureProvider) endpoint(deployment, path, rawQuery string) (string, error) {
	deployment = strings.TrimSpace(deployment)
	u, err := url.Parse(p.baseURL)
	if err != nil {
		return "", fmt.Errorf("invalid azure base URL: %w", err)
	}
	targetPath := strings.TrimLeft(path, "/")
	if strings.HasPrefix(targetPath, "v1/") {
		targetPath = strings.TrimPrefix(targetPath, "v1/")
	}
	targetPath = strings.TrimPrefix(targetPath, "openai/")
	basePath := strings.TrimRight(u.Path, "/")
	if deploymentIndex := strings.Index(basePath, "/openai/deployments/"); deploymentIndex >= 0 {
		parts := strings.Split(strings.Trim(basePath[deploymentIndex:], "/"), "/")
		if len(parts) < 3 || parts[2] == "" {
			return "", fmt.Errorf("azure deployment is required")
		}
		u.Path = strings.TrimRight(basePath[:deploymentIndex], "/") + "/openai/deployments/" + url.PathEscape(parts[2]) + "/" + targetPath
	} else {
		if deployment == "" {
			return "", fmt.Errorf("azure deployment is required")
		}
		if basePath == "" {
			basePath = "/openai"
		} else if !strings.HasSuffix(basePath, "/openai") {
			basePath += "/openai"
		}
		u.Path = strings.TrimRight(basePath, "/") + "/deployments/" + url.PathEscape(deployment) + "/" + targetPath
	}
	q := u.Query()
	requestQuery, err := url.ParseQuery(rawQuery)
	if err != nil {
		return "", fmt.Errorf("invalid raw query: %w", err)
	}
	for key, values := range requestQuery {
		q.Del(key)
		for _, value := range values {
			q.Add(key, value)
		}
	}
	if q.Get("api-version") == "" {
		q.Set("api-version", p.apiVersion)
	}
	u.RawQuery = q.Encode()
	return u.String(), nil
}

func (p *AzureProvider) setHeaders(dst http.Header, src http.Header) {
	copyForwardHeaders(dst, src)
	dst.Set("Content-Type", "application/json")
	dst.Set("api-key", p.apiKey)
	dst.Del("Authorization")
}

func extractDeploymentFromRawBody(body []byte) string {
	var payload map[string]interface{}
	if err := jsonx.Unmarshal(body, &payload); err != nil {
		return ""
	}
	if value, ok := payload["model"].(string); ok {
		return value
	}
	return ""
}

func removeModelFromRawBody(body []byte) []byte {
	var payload map[string]interface{}
	if err := jsonx.Unmarshal(body, &payload); err != nil {
		return body
	}
	delete(payload, "model")
	encoded, err := jsonx.Marshal(payload)
	if err != nil {
		return body
	}
	return encoded
}

func azureChatBody(req *ChatCompletionsRequest) ([]byte, error) {
	body, err := jsonx.Marshal(req)
	if err != nil {
		return nil, err
	}
	return removeModelFromRawBody(body), nil
}

package forwarder

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"

	relaybiz "micro-one-api/internal/relay/biz"
	relayprovider "micro-one-api/internal/relay/provider"
)

// NonStreamForwarder handles non-streaming requests to upstream providers.
type NonStreamForwarder struct {
	providerFactory *relayprovider.ProviderFactory
}

// NewNonStreamForwarder creates a new non-streaming forwarder.
func NewNonStreamForwarder(factory *relayprovider.ProviderFactory) *NonStreamForwarder {
	return &NonStreamForwarder{
		providerFactory: factory,
	}
}

// ForwardRequest forwards a non-streaming request to the upstream provider.
//
// It returns:
// - response: the raw HTTP response from upstream
// - body: the response body
// - usage: token usage information extracted from response
// - err: any error that occurred
func (f *NonStreamForwarder) ForwardRequest(
	ctx context.Context,
	plan *relaybiz.RelayPlan,
	endpoint string,
	body []byte,
	headers http.Header,
) (response *http.Response, bodyReader io.ReadCloser, usage *Usage, err error) {
	if f == nil || f.providerFactory == nil {
		return nil, nil, nil, fmt.Errorf("non-stream forwarder unavailable: no provider factory configured")
	}
	if plan == nil || plan.Channel == nil {
		return nil, nil, nil, fmt.Errorf("non-stream forwarder requires a selected channel")
	}

	provider, err := f.providerFactory.CreateProviderWithConfig(plan.Channel.Type, plan.Channel.BaseURL, plan.Channel.Key, relayprovider.ProviderConfig{
		APIVersion: plan.Channel.Config.APIVersion,
	})
	if err != nil {
		return nil, nil, nil, fmt.Errorf("failed to create provider: %w", err)
	}

	rawResp, err := provider.Forward(ctx, &relayprovider.RawRequest{
		Method: http.MethodPost,
		Path:   endpoint,
		Header: headers,
		Body:   body,
	})
	if err != nil {
		return nil, nil, nil, err
	}

	bodyReader = io.NopCloser(bytes.NewReader(rawResp.Body))
	response = &http.Response{
		StatusCode: rawResp.StatusCode,
		Header:     rawResp.Header.Clone(),
		Body:       io.NopCloser(bytes.NewReader(rawResp.Body)),
	}
	usage = extractUsage(rawResp.Body)
	return response, bodyReader, usage, nil
}

// Usage represents token usage extracted from response.
type Usage struct {
	PromptTokens     int64
	CompletionTokens int64
	TotalTokens      int64
}

// Close closes the forwarder and releases resources.
func (f *NonStreamForwarder) Close() error {
	return nil
}

func extractUsage(body []byte) *Usage {
	var payload struct {
		Usage struct {
			PromptTokens     int64 `json:"prompt_tokens"`
			CompletionTokens int64 `json:"completion_tokens"`
			TotalTokens      int64 `json:"total_tokens"`
		} `json:"usage"`
	}
	if err := json.Unmarshal(body, &payload); err != nil {
		return nil
	}
	if payload.Usage.PromptTokens == 0 && payload.Usage.CompletionTokens == 0 && payload.Usage.TotalTokens == 0 {
		return nil
	}
	return &Usage{
		PromptTokens:     payload.Usage.PromptTokens,
		CompletionTokens: payload.Usage.CompletionTokens,
		TotalTokens:      payload.Usage.TotalTokens,
	}
}

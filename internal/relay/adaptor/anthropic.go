package adaptor

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"strings"

	"micro-one-api/internal/relay/provider"
)

// AnthropicAdaptor wraps the Anthropic API-key provider behind the Adaptor
// interface.
//
// Upstream protocol: anthropic_messages. When the inbound format is already
// anthropic_messages the body is passed through; chat_completions inbound
// conversion will be handled by apicompat once the server layer is wired to
// call ConvertRequest with the inbound format.
type AnthropicAdaptor struct {
	baseAdaptor
	provider provider.Provider
	models   []string
}

var anthropicModels = []string{
	"claude-3-5-sonnet-20241022", "claude-3-5-haiku-20241022",
	"claude-3-opus-20240229", "claude-3-sonnet-20240229", "claude-3-haiku-20240307",
}

// NewAnthropicAdaptor builds an adaptor for an Anthropic API-key channel.
func NewAnthropicAdaptor(p provider.Provider, models []string) *AnthropicAdaptor {
	if len(models) == 0 {
		models = anthropicModels
	}
	return &AnthropicAdaptor{provider: p, models: models}
}

func (a *AnthropicAdaptor) Init(_ *RelayContext) {}

// Name returns the adaptor identifier.
func (a *AnthropicAdaptor) Name() string { return "anthropic" }

// ModelList returns the models this adaptor advertises.
func (a *AnthropicAdaptor) ModelList() []string { return a.models }

// ConvertRequest passes anthropic_messages bodies through unchanged. Other
// inbound formats require the apicompat converters (wired in a later phase).
func (a *AnthropicAdaptor) ConvertRequest(_ *RelayContext, inbound Format, body []byte) (Format, []byte, error) {
	if inbound == FormatAnthropicMessages {
		return FormatAnthropicMessages, body, nil
	}
	return "", nil, fmt.Errorf("anthropic adaptor: inbound format %q is not yet supported by the MVP conversion path", inbound)
}

// GetUpstreamURL returns the Anthropic /v1/messages endpoint.
func (a *AnthropicAdaptor) GetUpstreamURL(ctx *RelayContext) (string, error) {
	base := baseURLFromContext(ctx)
	if base == "" {
		base = "https://api.anthropic.com"
	}
	return strings.TrimRight(base, "/") + "/v1/messages", nil
}

// BuildUpstreamRequest constructs the POST request for /v1/messages using the
// Anthropic API-key auth headers (x-api-key + anthropic-version).
func (a *AnthropicAdaptor) BuildUpstreamRequest(ctx context.Context, rc *RelayContext, _ Format, body []byte) (*http.Request, error) {
	url, err := a.GetUpstreamURL(rc)
	if err != nil {
		return nil, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytesReader(body))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	if key := apiKeyFromContext(rc); key != "" {
		req.Header.Set("x-api-key", key)
	}
	req.Header.Set("anthropic-version", "2023-06-01")
	return req, nil
}

// ConvertResponse returns the upstream body unchanged for anthropic_messages.
func (a *AnthropicAdaptor) ConvertResponse(_ *RelayContext, upstream Format, resp *http.Response) (Format, []byte, error) {
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", nil, fmt.Errorf("read upstream response: %w", err)
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return "", nil, &provider.UpstreamHTTPError{StatusCode: resp.StatusCode, Body: body}
	}
	return FormatAnthropicMessages, body, nil
}

// ConvertStreamResponse returns the upstream stream reader unchanged.
func (a *AnthropicAdaptor) ConvertStreamResponse(_ *RelayContext, upstream Format, resp *http.Response) (Format, io.Reader, error) {
	return FormatAnthropicMessages, resp.Body, nil
}

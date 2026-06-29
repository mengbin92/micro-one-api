package adaptor

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"strings"

	"micro-one-api/internal/relay/provider"
)

// openAIModels is the default model list reported by OpenAI-compatible
// adaptors when the channel carries no explicit model list. The MVP exposes
// only the names the provider layer can already serve; channels can override
// via RelayContext.Channel.Models.
var openAIModels = []string{
	"gpt-4o", "gpt-4o-mini", "gpt-4-turbo", "gpt-3.5-turbo",
}

// OpenAICompatibleAdaptor wraps a provider.Provider (OpenAI-family) behind the
// Adaptor interface. It covers the 20+ OpenAI-compatible API-key channels.
//
// Upstream protocol: chat_completions. When the inbound format is already
// chat_completions the body is passed through unchanged; the responses⇄
// chat_completions conversion is handled by apicompat once the server layer
// is wired to call ConvertRequest with the inbound format.
type OpenAICompatibleAdaptor struct {
	baseAdaptor
	provider provider.Provider
	models   []string
}

// NewOpenAICompatibleAdaptor builds an adaptor for an OpenAI-compatible
// channel. The provider must be pre-constructed with the channel's base URL
// and key.
func NewOpenAICompatibleAdaptor(p provider.Provider, models []string) *OpenAICompatibleAdaptor {
	if len(models) == 0 {
		models = openAIModels
	}
	return &OpenAICompatibleAdaptor{provider: p, models: models}
}

func (a *OpenAICompatibleAdaptor) Init(_ *RelayContext) {}

// Name returns the adaptor identifier.
func (a *OpenAICompatibleAdaptor) Name() string { return "openai_compatible" }

// ModelList returns the models this adaptor advertises.
func (a *OpenAICompatibleAdaptor) ModelList() []string { return a.models }

// ConvertRequest passes chat_completions bodies through unchanged. Non-chat
// inbound formats require the apicompat converters, which will be invoked by
// the server layer in a later phase; for the MVP this returns the body as-is
// so existing behavior is preserved.
func (a *OpenAICompatibleAdaptor) ConvertRequest(_ *RelayContext, inbound Format, body []byte) (Format, []byte, error) {
	if inbound == FormatOpenAIChatCompletions {
		return FormatOpenAIChatCompletions, body, nil
	}
	// Inbound conversion (responses/anthropic -> chat_completions) is owned by
	// apicompat. The adaptor's role is to declare the upstream format; the
	// server layer is responsible for calling the right converter before
	// BuildUpstreamRequest. Until then we surface a clear error rather than
	// silently corrupting a request.
	return "", nil, fmt.Errorf("openai_compatible adaptor: inbound format %q is not yet supported by the MVP conversion path", inbound)
}

// GetUpstreamURL returns the chat/completions endpoint of the channel.
func (a *OpenAICompatibleAdaptor) GetUpstreamURL(ctx *RelayContext) (string, error) {
	base := baseURLFromContext(ctx)
	if base == "" {
		return "", fmt.Errorf("openai_compatible adaptor: channel has no base_url")
	}
	return strings.TrimRight(base, "/") + "/chat/completions", nil
}

// BuildUpstreamRequest constructs the POST request for /chat/completions. For
// the MVP it delegates to the wrapped provider's Forward path: callers that
// already hold a *provider.Provider should keep using it directly; this method
// exists so the Adaptor interface is complete and testable.
func (a *OpenAICompatibleAdaptor) BuildUpstreamRequest(ctx context.Context, rc *RelayContext, _ Format, body []byte) (*http.Request, error) {
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
		req.Header.Set("Authorization", "Bearer "+key)
	}
	return req, nil
}

// ConvertResponse returns the upstream body unchanged. The OpenAI-compatible
// provider already returns chat_completions JSON, which is the default
// outbound format for this adaptor.
func (a *OpenAICompatibleAdaptor) ConvertResponse(_ *RelayContext, upstream Format, resp *http.Response) (Format, []byte, error) {
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", nil, fmt.Errorf("read upstream response: %w", err)
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return "", nil, &provider.UpstreamHTTPError{StatusCode: resp.StatusCode, Body: body}
	}
	return FormatOpenAIChatCompletions, body, nil
}

// ConvertStreamResponse returns the upstream stream reader unchanged. The
// OpenAI-compatible provider emits chat_completions SSE directly.
func (a *OpenAICompatibleAdaptor) ConvertStreamResponse(_ *RelayContext, upstream Format, resp *http.Response) (Format, io.Reader, error) {
	return FormatOpenAIChatCompletions, resp.Body, nil
}

// --- helpers used by the MVP adaptors ---

func bytesReader(body []byte) io.Reader { return &byteReader{data: body} }

type byteReader struct {
	data []byte
	off  int
}

func (r *byteReader) Read(p []byte) (int, error) {
	if r.off >= len(r.data) {
		return 0, io.EOF
	}
	n := copy(p, r.data[r.off:])
	r.off += n
	return n, nil
}

func baseURLFromContext(ctx *RelayContext) string {
	if ctx == nil || ctx.Channel == nil {
		return ""
	}
	return ctx.Channel.BaseURL
}

func apiKeyFromContext(ctx *RelayContext) string {
	if ctx == nil || ctx.Channel == nil {
		return ""
	}
	return ctx.Channel.Key
}

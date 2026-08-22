package adaptor

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"strings"

	"micro-one-api/domain/upstream/provider"
)

// openAIModels is the default model list reported by OpenAI-compatible
// adaptors when the channel carries no explicit model list. Channels can override
// via RelayContext.Channel.Models.
var openAIModels = []string{
	"gpt-4o", "gpt-4o-mini", "gpt-4-turbo", "gpt-3.5-turbo",
}

// OpenAICompatibleAdaptor wraps a provider.Provider (OpenAI-family) behind the
// Adaptor interface. It covers the 20+ OpenAI-compatible API-key channels.
//
// Upstream protocol: chat_completions. Matching requests pass through;
// Responses and Anthropic Messages requests are converted via apicompat.
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

// ConvertRequest bridges the inbound client format to Chat Completions.
func (a *OpenAICompatibleAdaptor) ConvertRequest(rc *RelayContext, inbound Format, body []byte) (Format, []byte, error) {
	return convertRequestToChat(rc, inbound, body)
}

// GetUpstreamURL returns the chat/completions endpoint of the channel.
func (a *OpenAICompatibleAdaptor) GetUpstreamURL(ctx *RelayContext) (string, error) {
	if ctx == nil || ctx.Channel == nil {
		return "", fmt.Errorf("openai_compatible adaptor: channel is required")
	}
	base := provider.ResolveOpenAICompatibleBaseURL(ctx.Channel.Type, ctx.Channel.BaseURL)
	return strings.TrimRight(base, "/") + "/chat/completions", nil
}

// BuildUpstreamRequest constructs the POST request for /chat/completions.
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
func (a *OpenAICompatibleAdaptor) ConvertResponse(rc *RelayContext, upstream Format, resp *http.Response) (Format, []byte, error) {
	return convertChatResponse(rc, resp)
}

// ConvertStreamResponse returns the upstream stream reader unchanged. The
// OpenAI-compatible provider emits chat_completions SSE directly.
func (a *OpenAICompatibleAdaptor) ConvertStreamResponse(rc *RelayContext, upstream Format, resp *http.Response) (Format, io.Reader, error) {
	return convertChatStream(rc, resp)
}

// --- helpers shared by API-key adaptors ---

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

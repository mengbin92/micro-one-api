package adaptor

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"strings"

	"micro-one-api/internal/relay/provider"
)

// GeminiAdaptor wraps the Google Gemini API-key provider behind the Adaptor
// interface.
//
// Upstream protocol: gemini. Inbound conversion to/from chat_completions is
// owned by the provider's existing conversion logic; the adaptor exposes the
// Adaptor interface shape so the registry can dispatch to Gemini channels
// uniformly.
type GeminiAdaptor struct {
	baseAdaptor
	provider provider.Provider
	models   []string
}

var geminiModels = []string{
	"gemini-1.5-pro", "gemini-1.5-flash", "gemini-2.0-flash",
}

// NewGeminiAdaptor builds an adaptor for a Gemini API-key channel.
func NewGeminiAdaptor(p provider.Provider, models []string) *GeminiAdaptor {
	if len(models) == 0 {
		models = geminiModels
	}
	return &GeminiAdaptor{provider: p, models: models}
}

func (a *GeminiAdaptor) Init(_ *RelayContext) {}

// Name returns the adaptor identifier.
func (a *GeminiAdaptor) Name() string { return "gemini" }

// ModelList returns the models this adaptor advertises.
func (a *GeminiAdaptor) ModelList() []string { return a.models }

// ConvertRequest passes gemini-shaped bodies through. chat_completions inbound
// conversion is performed by the provider layer today; full apicompat wiring
// is a later phase.
func (a *GeminiAdaptor) ConvertRequest(_ *RelayContext, inbound Format, body []byte) (Format, []byte, error) {
	if inbound == FormatGemini {
		return FormatGemini, body, nil
	}
	return "", nil, fmt.Errorf("gemini adaptor: inbound format %q is not yet supported by the MVP conversion path", inbound)
}

// GetUpstreamURL returns the Gemini generateContent endpoint for the resolved
// model.
func (a *GeminiAdaptor) GetUpstreamURL(ctx *RelayContext) (string, error) {
	base := baseURLFromContext(ctx)
	if base == "" {
		base = "https://generativelanguage.googleapis.com"
	}
	model := ""
	if ctx != nil {
		model = ctx.ResolvedModel
	}
	if model == "" {
		return "", fmt.Errorf("gemini adaptor: resolved model is required")
	}
	key := apiKeyFromContext(ctx)
	return fmt.Sprintf("%s/v1beta/models/%s:generateContent?key=%s", strings.TrimRight(base, "/"), model, key), nil
}

// BuildUpstreamRequest constructs the POST request for generateContent.
func (a *GeminiAdaptor) BuildUpstreamRequest(ctx context.Context, rc *RelayContext, _ Format, body []byte) (*http.Request, error) {
	url, err := a.GetUpstreamURL(rc)
	if err != nil {
		return nil, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytesReader(body))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	return req, nil
}

// ConvertResponse returns the upstream body unchanged for gemini.
func (a *GeminiAdaptor) ConvertResponse(_ *RelayContext, upstream Format, resp *http.Response) (Format, []byte, error) {
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", nil, fmt.Errorf("read upstream response: %w", err)
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return "", nil, &provider.UpstreamHTTPError{StatusCode: resp.StatusCode, Body: body}
	}
	return FormatGemini, body, nil
}

// ConvertStreamResponse returns the upstream stream reader unchanged.
func (a *GeminiAdaptor) ConvertStreamResponse(_ *RelayContext, upstream Format, resp *http.Response) (Format, io.Reader, error) {
	return FormatGemini, resp.Body, nil
}

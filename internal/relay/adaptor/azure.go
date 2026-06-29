package adaptor

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"strings"

	"micro-one-api/internal/relay/provider"
)

// AzureAdaptor wraps the Azure OpenAI API-key provider behind the Adaptor
// interface.
//
// Upstream protocol: chat_completions (Azure-flavored). Azure uses
// deployment-specific endpoints and api-key auth rather than Bearer tokens;
// the adaptor encodes that shape so the registry can dispatch to Azure
// channels uniformly.
type AzureAdaptor struct {
	baseAdaptor
	provider   provider.Provider
	models     []string
	apiVersion string
}

// NewAzureAdaptor builds an adaptor for an Azure OpenAI channel. apiVersion
// may be empty to fall back to the provider default.
func NewAzureAdaptor(p provider.Provider, models []string, apiVersion string) *AzureAdaptor {
	if len(models) == 0 {
		models = []string{"gpt-4o", "gpt-4o-mini", "gpt-35-turbo"}
	}
	return &AzureAdaptor{provider: p, models: models, apiVersion: apiVersion}
}

func (a *AzureAdaptor) Init(_ *RelayContext) {}

// Name returns the adaptor identifier.
func (a *AzureAdaptor) Name() string { return "azure" }

// ModelList returns the models this adaptor advertises.
func (a *AzureAdaptor) ModelList() []string { return a.models }

// ConvertRequest passes chat_completions bodies through unchanged (Azure
// expects the OpenAI chat schema minus the model field, which the provider
// layer already strips).
func (a *AzureAdaptor) ConvertRequest(_ *RelayContext, inbound Format, body []byte) (Format, []byte, error) {
	if inbound == FormatOpenAIChatCompletions {
		return FormatOpenAIChatCompletions, body, nil
	}
	return "", nil, fmt.Errorf("azure adaptor: inbound format %q is not yet supported by the MVP conversion path", inbound)
}

// GetUpstreamURL returns the Azure deployment chat/completions endpoint. The
// deployment name is taken from the resolved model; the api-version query is
// appended.
func (a *AzureAdaptor) GetUpstreamURL(ctx *RelayContext) (string, error) {
	base := baseURLFromContext(ctx)
	if base == "" {
		return "", fmt.Errorf("azure adaptor: channel has no base_url")
	}
	deployment := ""
	if ctx != nil {
		deployment = ctx.ResolvedModel
	}
	if deployment == "" {
		return "", fmt.Errorf("azure adaptor: resolved model (deployment) is required")
	}
	v := a.apiVersion
	if v == "" {
		v = "2024-02-15-preview"
	}
	// Azure base URLs may already contain /openai/deployments/<dep>; if so we
	// append the chat path, otherwise we build the canonical deployment path.
	trimmed := strings.TrimRight(base, "/")
	if idx := strings.Index(trimmed, "/openai/deployments/"); idx >= 0 {
		return trimmed + "/chat/completions?api-version=" + v, nil
	}
	return trimmed + "/openai/deployments/" + deployment + "/chat/completions?api-version=" + v, nil
}

// BuildUpstreamRequest constructs the POST request using Azure api-key auth.
func (a *AzureAdaptor) BuildUpstreamRequest(ctx context.Context, rc *RelayContext, _ Format, body []byte) (*http.Request, error) {
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
		req.Header.Set("api-key", key)
	}
	req.Header.Del("Authorization")
	return req, nil
}

// ConvertResponse returns the upstream body unchanged for chat_completions.
func (a *AzureAdaptor) ConvertResponse(_ *RelayContext, upstream Format, resp *http.Response) (Format, []byte, error) {
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

// ConvertStreamResponse returns the upstream stream reader unchanged.
func (a *AzureAdaptor) ConvertStreamResponse(_ *RelayContext, upstream Format, resp *http.Response) (Format, io.Reader, error) {
	return FormatOpenAIChatCompletions, resp.Body, nil
}

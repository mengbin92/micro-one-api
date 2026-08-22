package adaptor

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"

	"micro-one-api/domain/upstream/provider"
	"micro-one-api/pkg/jsonx"
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

func (a *AzureAdaptor) Init(rc *RelayContext) {
	if a.apiVersion == "" && rc != nil && rc.Channel != nil {
		a.apiVersion = rc.Channel.Config.APIVersion
	}
}

// Name returns the adaptor identifier.
func (a *AzureAdaptor) Name() string { return "azure" }

// ModelList returns the models this adaptor advertises.
func (a *AzureAdaptor) ModelList() []string { return a.models }

// ConvertRequest bridges to Chat Completions and removes model because Azure
// selects the deployment in the URL.
func (a *AzureAdaptor) ConvertRequest(rc *RelayContext, inbound Format, body []byte) (Format, []byte, error) {
	format, converted, err := convertRequestToChat(rc, inbound, body)
	if err != nil {
		return "", nil, err
	}
	var request map[string]jsonx.RawMessage
	if err := jsonx.Unmarshal(converted, &request); err != nil {
		return "", nil, fmt.Errorf("azure adaptor: parse chat request: %w", err)
	}
	delete(request, "model")
	converted, err = jsonx.Marshal(request)
	if err != nil {
		return "", nil, fmt.Errorf("azure adaptor: marshal chat request: %w", err)
	}
	return format, converted, nil
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
	u, err := url.Parse(base)
	if err != nil {
		return "", fmt.Errorf("azure adaptor: invalid base URL: %w", err)
	}
	basePath := strings.TrimRight(u.Path, "/")
	if index := strings.Index(basePath, "/openai/deployments/"); index >= 0 {
		parts := strings.Split(strings.Trim(basePath[index:], "/"), "/")
		if len(parts) < 3 || parts[2] == "" {
			return "", fmt.Errorf("azure adaptor: deployment is required")
		}
		u.Path = strings.TrimRight(basePath[:index], "/") + "/openai/deployments/" + url.PathEscape(parts[2]) + "/chat/completions"
	} else {
		if basePath == "" {
			basePath = "/openai"
		} else if !strings.HasSuffix(basePath, "/openai") {
			basePath += "/openai"
		}
		u.Path = strings.TrimRight(basePath, "/") + "/deployments/" + url.PathEscape(deployment) + "/chat/completions"
	}
	query := u.Query()
	if query.Get("api-version") == "" {
		query.Set("api-version", v)
	}
	u.RawQuery = query.Encode()
	return u.String(), nil
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
func (a *AzureAdaptor) ConvertResponse(rc *RelayContext, upstream Format, resp *http.Response) (Format, []byte, error) {
	return convertChatResponse(rc, resp)
}

// ConvertStreamResponse returns the upstream stream reader unchanged.
func (a *AzureAdaptor) ConvertStreamResponse(rc *RelayContext, upstream Format, resp *http.Response) (Format, io.Reader, error) {
	return convertChatStream(rc, resp)
}

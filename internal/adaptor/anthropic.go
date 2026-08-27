package adaptor

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"strings"

	"micro-one-api/domain/upstream/provider"
	"micro-one-api/internal/apicompat"
	"micro-one-api/pkg/jsonx"
)

// AnthropicAdaptor wraps the Anthropic API-key provider behind the Adaptor
// interface.
//
// Upstream protocol: anthropic_messages. Native Messages bodies retain
// extension fields while the adaptor rewrites only the resolved model.
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

// ConvertRequest preserves native Messages requests and bridges Responses
// requests to the common subset of the Anthropic Messages schema supported by
// API-key channels, including third-party Anthropic-compatible providers.
func (a *AnthropicAdaptor) ConvertRequest(rc *RelayContext, inbound Format, body []byte) (Format, []byte, error) {
	switch inbound {
	case FormatAnthropicMessages:
		var request map[string]jsonx.RawMessage
		if err := jsonx.Unmarshal(body, &request); err != nil {
			return "", nil, fmt.Errorf("anthropic adaptor: parse request: %w", err)
		}
		if rc != nil && rc.ResolvedModel != "" {
			model, err := jsonx.Marshal(rc.ResolvedModel)
			if err != nil {
				return "", nil, fmt.Errorf("anthropic adaptor: marshal model: %w", err)
			}
			request["model"] = model
		}
		out, err := jsonx.Marshal(request)
		if err != nil {
			return "", nil, fmt.Errorf("anthropic adaptor: marshal request: %w", err)
		}
		return FormatAnthropicMessages, out, nil
	case FormatOpenAIResponses:
		var request apicompat.ResponsesRequest
		if err := jsonx.Unmarshal(body, &request); err != nil {
			return "", nil, fmt.Errorf("anthropic adaptor: parse responses request: %w", err)
		}
		if rc != nil && rc.ResolvedModel != "" {
			request.Model = rc.ResolvedModel
		}
		if strings.TrimSpace(request.Model) == "" {
			return "", nil, fmt.Errorf("anthropic adaptor: model is required")
		}
		converted, err := apicompat.ResponsesToAnthropicRequest(&request)
		if err != nil {
			return "", nil, fmt.Errorf("anthropic adaptor: responses→anthropic: %w", err)
		}
		// API-key channels may target third-party Anthropic-compatible
		// endpoints. Keep the request on their common Messages subset.
		converted.Thinking = nil
		converted.OutputConfig = nil
		for index := range converted.Tools {
			converted.Tools[index].Type = ""
			if len(converted.Tools[index].InputSchema) == 0 || string(converted.Tools[index].InputSchema) == "null" {
				converted.Tools[index].InputSchema = []byte(`{"type":"object","properties":{}}`)
			}
		}
		out, err := jsonx.Marshal(converted)
		if err != nil {
			return "", nil, fmt.Errorf("anthropic adaptor: marshal responses request: %w", err)
		}
		return FormatAnthropicMessages, out, nil
	default:
		return "", nil, fmt.Errorf("anthropic adaptor: inbound format %q is not supported", inbound)
	}
}

// GetUpstreamURL returns the Anthropic /v1/messages endpoint.
func (a *AnthropicAdaptor) GetUpstreamURL(ctx *RelayContext) (string, error) {
	base := baseURLFromContext(ctx)
	if base == "" {
		base = "https://api.anthropic.com"
	}
	base = strings.TrimRight(base, "/")
	base = strings.TrimSuffix(base, "/v1")
	return base + "/v1/messages", nil
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
	if rc != nil {
		a.copyForwardHeaders(req.Header, rc.InboundHeader)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Del("Authorization")
	req.Header.Del("x-api-key")
	if key := apiKeyFromContext(rc); key != "" {
		req.Header.Set("x-api-key", key)
	}
	req.Header.Set("anthropic-version", "2023-06-01")
	return req, nil
}

// ConvertResponse returns native Messages unchanged and converts successful
// Anthropic responses back to Responses when that was the inbound protocol.
func (a *AnthropicAdaptor) ConvertResponse(rc *RelayContext, _ Format, resp *http.Response) (Format, []byte, error) {
	defer resp.Body.Close()
	body, err := io.ReadAll(io.LimitReader(resp.Body, provider.MaxUpstreamResponseBody))
	if err != nil {
		return "", nil, fmt.Errorf("read upstream response: %w", err)
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return "", nil, &provider.UpstreamHTTPError{StatusCode: resp.StatusCode, Body: body}
	}
	if rc != nil && rc.InboundFormat == FormatOpenAIResponses {
		var response apicompat.AnthropicResponse
		if err := jsonx.Unmarshal(body, &response); err != nil {
			return "", nil, fmt.Errorf("anthropic adaptor: parse upstream response: %w", err)
		}
		out, err := jsonx.Marshal(apicompat.AnthropicToResponsesResponse(&response))
		if err != nil {
			return "", nil, fmt.Errorf("anthropic adaptor: marshal responses response: %w", err)
		}
		return FormatOpenAIResponses, out, nil
	}
	return FormatAnthropicMessages, body, nil
}

// ConvertStreamResponse converts Anthropic SSE to Responses SSE when needed.
func (a *AnthropicAdaptor) ConvertStreamResponse(rc *RelayContext, _ Format, resp *http.Response) (Format, io.Reader, error) {
	if rc != nil && rc.InboundFormat == FormatOpenAIResponses {
		reader, writer := io.Pipe()
		go pumpAnthropicToResponses(resp.Body, writer)
		return FormatOpenAIResponses, reader, nil
	}
	return FormatAnthropicMessages, resp.Body, nil
}

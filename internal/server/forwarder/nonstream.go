package forwarder

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"net/http"

	relayprovider "micro-one-api/domain/upstream/provider"
	relaybiz "micro-one-api/internal/biz"
	"micro-one-api/internal/server/usage"
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
// - usage: canonical token usage information extracted from response
// - err: any error that occurred
func (f *NonStreamForwarder) ForwardRequest(
	ctx context.Context,
	plan *relaybiz.RelayPlan,
	endpoint string,
	body []byte,
	headers http.Header,
) (response *http.Response, bodyReader io.ReadCloser, usage *relaybiz.UsageEnvelope, err error) {
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
	usage = extractCanonicalUsage(rawResp.Body, plan)
	return response, bodyReader, usage, nil
}

// extractCanonicalUsage builds the usage envelope from a non-streaming
// upstream response. The parser proves the semantics from the response's
// field shape; routing identity (channel type / platform) is NOT consulted
// (token-usage-billing-semantics-remediation §4.2).
func extractCanonicalUsage(body []byte, plan *relaybiz.RelayPlan) *relaybiz.UsageEnvelope {
	if plan == nil {
		return nil
	}
	env := usage.ExtractEnvelopeFromJSON(body, 0)
	if env.ParseStatus == relaybiz.UsageParseEstimated && env.CanonicalOrZero().IsEmpty() {
		return nil
	}
	return &env
}

// Close closes the forwarder and releases resources.
func (f *NonStreamForwarder) Close() error {
	return nil
}

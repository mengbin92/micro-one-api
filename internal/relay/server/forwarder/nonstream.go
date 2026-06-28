package forwarder

import (
	"context"
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
	// TODO: Implement non-streaming forwarder
	// This will:
	// 1. Create provider instance from plan.Channel
	// 2. Build upstream request
	// 3. Execute call
	// 4. Parse response and extract usage

	return nil, nil, nil, nil
}

// Usage represents token usage extracted from response.
type Usage struct {
	PromptTokens     int64
	CompletionTokens int64
	TotalTokens      int64
}

// Close closes the forwarder and releases resources.
func (f *NonStreamForwarder) Close() error {
	// TODO: Cleanup resources
	return nil
}

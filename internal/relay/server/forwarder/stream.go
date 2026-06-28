package forwarder

import (
	"context"
	"net/http"

	relaybiz "micro-one-api/internal/relay/biz"
	relayprovider "micro-one-api/internal/relay/provider"
)

// StreamForwarder handles streaming requests to upstream providers.
type StreamForwarder struct {
	providerFactory *relayprovider.ProviderFactory
}

// NewStreamForwarder creates a new streaming forwarder.
func NewStreamForwarder(factory *relayprovider.ProviderFactory) *StreamForwarder {
	return &StreamForwarder{
		providerFactory: factory,
	}
}

// ForwardRequest forwards a streaming request to the upstream provider.
//
// It returns:
// - response: the raw HTTP response from upstream
// - chunks: a channel of stream chunks (if SSE)
// - err: any error that occurred
func (f *StreamForwarder) ForwardRequest(
	ctx context.Context,
	plan *relaybiz.RelayPlan,
	endpoint string,
	body []byte,
	headers http.Header,
) (response *http.Response, chunks <-chan []byte, err error) {
	// TODO: Implement streaming forwarder
	// This will:
	// 1. Create provider instance from plan.Channel
	// 2. Build upstream request
	// 3. Execute streaming call
	// 4. Return SSE chunk channel

	return nil, nil, nil
}

// ProcessChunk processes a single stream chunk from upstream.
func (f *StreamForwarder) ProcessChunk(chunk []byte) ([]byte, error) {
	// TODO: Implement chunk processing
	// This may:
	// 1. Parse chunk format
	// 2. Transform data if needed
	// 3. Extract usage information
	return chunk, nil
}

// Close closes the streaming connection.
func (f *StreamForwarder) Close() error {
	// TODO: Cleanup resources
	return nil
}

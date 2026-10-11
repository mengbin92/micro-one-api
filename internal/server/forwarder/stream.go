package forwarder

import (
	"context"
	"fmt"
	"net/http"

	relayprovider "micro-one-api/domain/upstream/provider"
	relaybiz "micro-one-api/internal/biz"
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
// The caller owns response.Body and closes it when the downstream stops reading.
func (f *StreamForwarder) ForwardRequest(
	ctx context.Context,
	plan *relaybiz.RelayPlan,
	endpoint string,
	body []byte,
	headers http.Header,
) (*http.Response, error) {
	if f == nil || f.providerFactory == nil {
		return nil, fmt.Errorf("stream forwarder unavailable: no provider factory configured")
	}
	if plan == nil || plan.Channel == nil {
		return nil, fmt.Errorf("stream forwarder requires a selected channel")
	}

	provider, err := f.providerFactory.CreateProviderWithConfig(plan.Channel.Type, plan.Channel.BaseURL, plan.Channel.Key, relayprovider.ProviderConfig{
		APIVersion: plan.Channel.Config.APIVersion,
	})
	if err != nil {
		return nil, fmt.Errorf("failed to create provider: %w", err)
	}

	streamResp, err := provider.ForwardStream(ctx, &relayprovider.RawRequest{
		Method: http.MethodPost,
		Path:   endpoint,
		Header: headers,
		Body:   body,
	})
	if err != nil {
		return nil, err
	}

	return &http.Response{
		StatusCode: streamResp.StatusCode,
		Header:     streamResp.Header.Clone(),
		Body:       streamResp.Body,
	}, nil
}

// ProcessChunk processes a single stream chunk from upstream.
func (f *StreamForwarder) ProcessChunk(chunk []byte) ([]byte, error) {
	return chunk, nil
}

// Close closes the streaming connection.
func (f *StreamForwarder) Close() error {
	return nil
}

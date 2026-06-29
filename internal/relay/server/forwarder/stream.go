package forwarder

import (
	"context"
	"fmt"
	"io"
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
	if f == nil || f.providerFactory == nil {
		return nil, nil, fmt.Errorf("stream forwarder unavailable: no provider factory configured")
	}
	if plan == nil || plan.Channel == nil {
		return nil, nil, fmt.Errorf("stream forwarder requires a selected channel")
	}

	provider, err := f.providerFactory.CreateProviderWithConfig(plan.Channel.Type, plan.Channel.BaseURL, plan.Channel.Key, relayprovider.ProviderConfig{
		APIVersion: plan.Channel.Config.APIVersion,
	})
	if err != nil {
		return nil, nil, fmt.Errorf("failed to create provider: %w", err)
	}

	streamResp, err := provider.ForwardStream(ctx, &relayprovider.RawRequest{
		Method: http.MethodPost,
		Path:   endpoint,
		Header: headers,
		Body:   body,
	})
	if err != nil {
		return nil, nil, err
	}

	response = &http.Response{
		StatusCode: streamResp.StatusCode,
		Header:     streamResp.Header.Clone(),
		Body:       streamResp.Body,
	}
	return response, readChunks(streamResp.Body), nil
}

// ProcessChunk processes a single stream chunk from upstream.
func (f *StreamForwarder) ProcessChunk(chunk []byte) ([]byte, error) {
	return chunk, nil
}

// Close closes the streaming connection.
func (f *StreamForwarder) Close() error {
	return nil
}

func readChunks(body io.ReadCloser) <-chan []byte {
	chunks := make(chan []byte, 16)
	go func() {
		defer close(chunks)
		defer body.Close()

		buf := make([]byte, 32*1024)
		for {
			n, err := body.Read(buf)
			if n > 0 {
				chunk := make([]byte, n)
				copy(chunk, buf[:n])
				chunks <- chunk
			}
			if err != nil {
				return
			}
		}
	}()
	return chunks
}

package server

import (
	"context"
	"fmt"
	"net/http"

	relayprovider "micro-one-api/domain/upstream/provider"
	relaybiz "micro-one-api/internal/biz"
)

func (s *HTTPServer) forwardResponsesRaw(ctx context.Context, ch *relaybiz.Channel, method, path, query string, header http.Header, body []byte) (*relayprovider.RawResponse, error) {
	if err := relayprovider.ValidateEndpoint(ch.Type, path); err != nil {
		return nil, err
	}
	provider, err := s.providerFactory.CreateProviderWithConfig(ch.Type, ch.BaseURL, ch.Key, relayprovider.ProviderConfig{
		APIVersion: ch.Config.APIVersion,
	})
	if err != nil {
		return nil, fmt.Errorf("failed to create provider: %w", err)
	}
	resp, err := provider.Forward(ctx, &relayprovider.RawRequest{
		Method: method,
		Path:   path,
		Query:  query,
		Header: header,
		Body:   body,
	})
	return resp, classifyExecutorCapabilityError(path, err)
}

func (s *HTTPServer) forwardResponsesRawStream(ctx context.Context, ch *relaybiz.Channel, method, path, query string, header http.Header, body []byte) (*relayprovider.RawStreamResponse, error) {
	if err := relayprovider.ValidateEndpoint(ch.Type, path); err != nil {
		return nil, err
	}
	provider, err := s.providerFactory.CreateProviderWithConfig(ch.Type, ch.BaseURL, ch.Key, relayprovider.ProviderConfig{
		APIVersion: ch.Config.APIVersion,
	})
	if err != nil {
		return nil, fmt.Errorf("failed to create provider: %w", err)
	}
	resp, err := provider.ForwardStream(ctx, &relayprovider.RawRequest{
		Method: method,
		Path:   path,
		Query:  query,
		Header: header,
		Body:   body,
	})
	return resp, classifyExecutorCapabilityError(path, err)
}

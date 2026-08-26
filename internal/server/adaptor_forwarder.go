package server

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"sync"

	relaycredential "micro-one-api/domain/upstream/credential"
	relayprovider "micro-one-api/domain/upstream/provider"
	relayadaptor "micro-one-api/internal/adaptor"
	relaybiz "micro-one-api/internal/biz"
	"micro-one-api/internal/server/forwarder"
	"micro-one-api/internal/server/usage"
)

// relayAdaptorForwarder is the non-stream bridge used by the staged
// transport-neutral executor. It deliberately keeps selection and lifecycle
// ownership in the orchestrator; this type only resolves the selected
// credential, builds the adaptor request, and returns an owned response.
type relayAdaptorForwarder struct {
	fallback               *forwarder.NonStreamForwarder
	streamFallback         *forwarder.StreamForwarder
	accountResolver        relaycredential.SubscriptionAccountResolver
	apiKeyHTTPClient       *http.Client
	apiKeyStreamHTTPClient *http.Client
	oauthHTTPClient        *http.Client
}

func newRelayAdaptorForwarder(
	providerFactory *relayprovider.ProviderFactory,
	accountResolver relaycredential.SubscriptionAccountResolver,
	apiKeyHTTPClient, apiKeyStreamHTTPClient, oauthHTTPClient *http.Client,
) relaybiz.Forwarder {
	return relayAdaptorForwarder{
		fallback:               forwarder.NewNonStreamForwarder(providerFactory),
		streamFallback:         forwarder.NewStreamForwarder(providerFactory),
		accountResolver:        accountResolver,
		apiKeyHTTPClient:       apiKeyHTTPClient,
		apiKeyStreamHTTPClient: apiKeyStreamHTTPClient,
		oauthHTTPClient:        oauthHTTPClient,
	}
}

func (f relayAdaptorForwarder) Forward(ctx context.Context, plan *relaybiz.RelayPlan, req relaybiz.ExecutorRequest) (*relaybiz.ForwardResponse, error) {
	if plan == nil || plan.Channel == nil {
		return nil, fmt.Errorf("adaptor forwarder requires a selected channel")
	}
	ad, ok := relayadaptor.GetAdaptor(plan.Channel.Type)
	if !ok {
		if f.fallback == nil {
			return nil, fmt.Errorf("no adaptor registered for channel type %d", plan.Channel.Type)
		}
		response, err := (relayNonStreamForwarder{forwarder: f.fallback}).Forward(ctx, plan, req)
		return response, classifyExecutorCapabilityError(req.Endpoint, err)
	}

	rc, client, err := f.relayContext(ctx, plan, req)
	if err != nil {
		return nil, err
	}
	ad.Init(rc)
	upstreamFormat, upstreamBody, err := ad.ConvertRequest(rc, rc.InboundFormat, req.Body)
	if err != nil {
		return nil, fmt.Errorf("adaptor convert request: %w", err)
	}
	upstreamReq, err := ad.BuildUpstreamRequest(ctx, rc, upstreamFormat, upstreamBody)
	if err != nil {
		return nil, fmt.Errorf("adaptor build request: %w", err)
	}
	if upstreamReq == nil || upstreamReq.URL == nil {
		return nil, fmt.Errorf("adaptor returned an incomplete upstream request")
	}
	if rc.Account == nil {
		copyRelayExecutorHeaders(upstreamReq.Header, rc.InboundHeader)
	}
	if err := relayprovider.ValidateBaseURLForChannel(plan.Channel.Type, upstreamReq.URL.String()); err != nil {
		return nil, fmt.Errorf("validate upstream URL: %w", err)
	}
	if client == nil {
		client = http.DefaultClient
	}
	resp, err := client.Do(upstreamReq) // #nosec G704 -- adaptor URL is validated above.
	if err != nil {
		return nil, fmt.Errorf("upstream call: %w", err)
	}
	defer resp.Body.Close()
	responseHeaders := resp.Header.Clone()
	if resp.StatusCode < http.StatusOK || resp.StatusCode >= http.StatusMultipleChoices {
		body, readErr := io.ReadAll(io.LimitReader(resp.Body, relayprovider.MaxUpstreamErrorBody))
		if readErr != nil {
			return nil, fmt.Errorf("read upstream error response: %w", readErr)
		}
		return nil, classifyExecutorCapabilityError(req.Endpoint, &relayprovider.UpstreamHTTPError{StatusCode: resp.StatusCode, Body: body})
	}

	_, body, err := ad.ConvertResponse(rc, upstreamFormat, resp)
	if err != nil {
		return nil, fmt.Errorf("adaptor convert response: %w", err)
	}
	u := usage.ExtractFromJSON(body, 0, relaybiz.IsPromptExclusiveChannel(plan))
	var canonical *relaybiz.CanonicalUsage
	if !u.IsEmpty() {
		canonical = &u
	}
	return &relaybiz.ForwardResponse{
		StatusCode: resp.StatusCode,
		Headers:    httpHeaderToMap(responseHeaders),
		Body:       body,
		Usage:      canonical,
	}, nil
}

func (f relayAdaptorForwarder) ForwardStream(ctx context.Context, plan *relaybiz.RelayPlan, req relaybiz.ExecutorRequest) (*relaybiz.StreamForwardResponse, error) {
	if plan == nil || plan.Channel == nil {
		return nil, fmt.Errorf("adaptor stream forwarder requires a selected channel")
	}
	ad, ok := relayadaptor.GetAdaptor(plan.Channel.Type)
	if !ok {
		if f.streamFallback == nil {
			return nil, fmt.Errorf("no adaptor registered for channel type %d", plan.Channel.Type)
		}
		response, err := (relayProviderStreamForwarder{forwarder: f.streamFallback}).ForwardStream(ctx, plan, req)
		return response, classifyExecutorCapabilityError(req.Endpoint, err)
	}
	rc, client, err := f.relayContext(ctx, plan, req)
	if err != nil {
		return nil, err
	}
	ad.Init(rc)
	upstreamFormat, upstreamBody, err := ad.ConvertRequest(rc, rc.InboundFormat, req.Body)
	if err != nil {
		return nil, fmt.Errorf("adaptor convert stream request: %w", err)
	}
	upstreamReq, err := ad.BuildUpstreamRequest(ctx, rc, upstreamFormat, upstreamBody)
	if err != nil {
		return nil, fmt.Errorf("adaptor build stream request: %w", err)
	}
	if upstreamReq == nil || upstreamReq.URL == nil {
		return nil, fmt.Errorf("adaptor returned an incomplete stream request")
	}
	if rc.Account == nil {
		copyRelayExecutorHeaders(upstreamReq.Header, rc.InboundHeader)
	}
	if err := relayprovider.ValidateBaseURLForChannel(plan.Channel.Type, upstreamReq.URL.String()); err != nil {
		return nil, fmt.Errorf("validate upstream URL: %w", err)
	}
	if client == nil {
		client = http.DefaultClient
	}
	client = streamHTTPClient(client)
	resp, err := client.Do(upstreamReq) // #nosec G704 -- adaptor URL is validated above.
	if err != nil {
		return nil, fmt.Errorf("upstream stream call: %w", err)
	}
	if resp.StatusCode < http.StatusOK || resp.StatusCode >= http.StatusMultipleChoices {
		defer resp.Body.Close()
		body, readErr := io.ReadAll(io.LimitReader(resp.Body, relayprovider.MaxUpstreamErrorBody))
		if readErr != nil {
			return nil, fmt.Errorf("read upstream stream error response: %w", readErr)
		}
		return nil, classifyExecutorCapabilityError(req.Endpoint, &relayprovider.UpstreamHTTPError{StatusCode: resp.StatusCode, Body: body})
	}
	_, reader, err := ad.ConvertStreamResponse(rc, upstreamFormat, resp)
	if err != nil {
		_ = resp.Body.Close()
		return nil, fmt.Errorf("adaptor convert stream response: %w", err)
	}
	return &relaybiz.StreamForwardResponse{
		StatusCode: resp.StatusCode,
		Headers:    httpHeaderToMap(resp.Header),
		Stream:     newConvertedRelayStream(reader, resp.Body),
	}, nil
}

func classifyExecutorCapabilityError(endpoint string, err error) error {
	if err == nil || relaybiz.UpstreamStatus(err) != http.StatusMethodNotAllowed {
		return err
	}
	switch endpoint {
	case string(EndpointResponses), "/responses", "/v1/responses":
		return relaybiz.MarkProtocolCapabilityError(err)
	default:
		return err
	}
}

func (f relayAdaptorForwarder) relayContext(ctx context.Context, plan *relaybiz.RelayPlan, req relaybiz.ExecutorRequest) (*relayadaptor.RelayContext, *http.Client, error) {
	rc := &relayadaptor.RelayContext{
		InboundFormat: executorInboundFormat(req.Endpoint),
		ClientModel:   req.Model,
		ResolvedModel: plan.ResolvedModel,
		Channel:       plan.Channel,
		IsStream:      req.Stream,
		RequestID:     req.RequestID,
		RawBody:       append([]byte(nil), req.Body...),
		InboundHeader: headerMapToHTTP(req.Headers),
	}
	if plan.Auth != nil {
		rc.UserID = plan.Auth.UserID
	}
	if plan.Account == nil && plan.Channel.SubscriptionAccountID == 0 {
		if req.Stream && f.apiKeyStreamHTTPClient != nil {
			return rc, f.apiKeyStreamHTTPClient, nil
		}
		return rc, f.apiKeyHTTPClient, nil
	}

	meta := fallbackSubscriptionAccountMetadata(plan, plan.Channel)
	resolverChannelID := plan.Channel.ID
	if plan.Account != nil {
		meta = subscriptionAccountMetadataFromPlan(plan.Account)
	}
	if f.accountResolver != nil {
		resolved, err := f.accountResolver.Resolve(ctx, resolverChannelID)
		if err != nil {
			return nil, nil, fmt.Errorf("resolve subscription account credential: %w", err)
		}
		if resolved != nil {
			meta = resolved
		}
	}
	if meta == nil {
		return nil, nil, fmt.Errorf("subscription account metadata is unavailable")
	}
	rc.Account = &relayadaptor.AccountRef{
		ID:          meta.ID,
		Platform:    string(meta.Platform),
		AccountType: accountTypeOrDefault(meta.AccountType),
		GroupID:     meta.GroupID,
		AccessToken: meta.AccessToken,
		AccountID:   meta.AccountID,
		Fingerprint: meta.Fingerprint,
	}
	rc.HTTPClient = f.oauthHTTPClient
	return rc, f.oauthHTTPClient, nil
}

func executorInboundFormat(endpoint string) relayadaptor.Format {
	switch APIEndpoint(endpoint) {
	case EndpointResponses:
		return relayadaptor.FormatOpenAIResponses
	case EndpointAnthropicMessages:
		return relayadaptor.FormatAnthropicMessages
	default:
		return relayadaptor.FormatOpenAIChatCompletions
	}
}

func streamHTTPClient(client *http.Client) *http.Client {
	if client == nil || client.Timeout == 0 {
		return client
	}
	return &http.Client{
		Transport:     client.Transport,
		CheckRedirect: client.CheckRedirect,
		Jar:           client.Jar,
	}
}

type convertedRelayStream struct {
	reader  io.Reader
	closers []io.Closer
	once    sync.Once
	err     error
}

func newConvertedRelayStream(reader io.Reader, upstream io.Closer) relaybiz.RelayStream {
	stream := &convertedRelayStream{reader: reader}
	if closer, ok := reader.(io.Closer); ok {
		stream.closers = append(stream.closers, closer)
	}
	if upstream != nil {
		stream.closers = append(stream.closers, upstream)
	}
	return stream
}

func (s *convertedRelayStream) Read(p []byte) (int, error) {
	return s.reader.Read(p)
}

func (s *convertedRelayStream) Close() error {
	s.once.Do(func() {
		for _, closer := range s.closers {
			if err := closer.Close(); s.err == nil {
				s.err = err
			}
		}
	})
	return s.err
}

func copyRelayExecutorHeaders(dst, src http.Header) {
	for key, values := range src {
		if !isRelayExecutorHeader(key) {
			continue
		}
		dst.Del(key)
		for _, value := range values {
			dst.Add(key, value)
		}
	}
}

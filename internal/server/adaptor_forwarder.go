package server

import (
	"context"
	"fmt"
	"io"
	"net/http"

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
	fallback         *forwarder.NonStreamForwarder
	accountResolver  relaycredential.SubscriptionAccountResolver
	apiKeyHTTPClient *http.Client
	oauthHTTPClient  *http.Client
}

func newRelayAdaptorForwarder(
	providerFactory *relayprovider.ProviderFactory,
	accountResolver relaycredential.SubscriptionAccountResolver,
	apiKeyHTTPClient, oauthHTTPClient *http.Client,
) relaybiz.Forwarder {
	return relayAdaptorForwarder{
		fallback:         forwarder.NewNonStreamForwarder(providerFactory),
		accountResolver:  accountResolver,
		apiKeyHTTPClient: apiKeyHTTPClient,
		oauthHTTPClient:  oauthHTTPClient,
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
		return (relayNonStreamForwarder{forwarder: f.fallback}).Forward(ctx, plan, req)
	}

	rc, client, err := f.relayContext(ctx, plan, req)
	if err != nil {
		return nil, err
	}
	ad.Init(rc)
	upstreamFormat, upstreamBody, err := ad.ConvertRequest(rc, relayadaptor.FormatOpenAIChatCompletions, req.Body)
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
		return nil, &relayprovider.UpstreamHTTPError{StatusCode: resp.StatusCode, Body: body}
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

func (f relayAdaptorForwarder) relayContext(ctx context.Context, plan *relaybiz.RelayPlan, req relaybiz.ExecutorRequest) (*relayadaptor.RelayContext, *http.Client, error) {
	rc := &relayadaptor.RelayContext{
		InboundFormat: relayadaptor.FormatOpenAIChatCompletions,
		ClientModel:   req.Model,
		ResolvedModel: plan.ResolvedModel,
		Channel:       plan.Channel,
		IsStream:      false,
		RequestID:     req.RequestID,
		RawBody:       append([]byte(nil), req.Body...),
		InboundHeader: headerMapToHTTP(req.Headers),
	}
	if plan.Auth != nil {
		rc.UserID = plan.Auth.UserID
	}
	if plan.Account == nil && plan.Channel.SubscriptionAccountID == 0 {
		return rc, f.apiKeyHTTPClient, nil
	}

	meta := fallbackSubscriptionAccountMetadata(plan, plan.Channel)
	accountID := plan.Channel.ID
	if plan.Account != nil {
		meta = subscriptionAccountMetadataFromPlan(plan.Account)
		accountID = plan.Account.ID
	}
	if f.accountResolver != nil {
		resolved, err := f.accountResolver.Resolve(ctx, accountID)
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

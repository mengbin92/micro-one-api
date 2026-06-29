package server

import (
	"fmt"
	"io"
	"net/http"

	"github.com/bytedance/sonic"

	billingv1 "micro-one-api/api/billing/v1"
	relayadaptor "micro-one-api/internal/relay/adaptor"
	relaybiz "micro-one-api/internal/relay/biz"
	relaycredential "micro-one-api/internal/relay/credential"
	relayprovider "micro-one-api/internal/relay/provider"
)

// handleChatCompletionsViaAdaptor is the feature-flag-gated request path for
// subscription-account channels (Codex / Claude OAuth). It resolves the real
// subscription-account metadata from the selected channel, builds a
// RelayContext, and drives the full ConvertRequest → BuildUpstreamRequest →
// upstream call → ConvertResponse / ConvertStreamResponse pipeline.
//
// It is intentionally a thin, self-contained path: it does not participate in
// the RetryExecutor (subscription accounts are selected explicitly and retried
// via a different mechanism in later phases), and it performs only
// best-effort quota accounting. The goal of the MVP is to validate that the
// Responses-hub + mimicry + credential layers compose end-to-end.
func (s *HTTPServer) handleChatCompletionsViaAdaptor(
	w http.ResponseWriter,
	r *http.Request,
	plan *relaybiz.RelayPlan,
	clientModel string,
	rawBody []byte,
) {
	s.handleSubscriptionAccountViaAdaptor(w, r, plan, clientModel, rawBody, relayadaptor.FormatOpenAIChatCompletions)
}

func (s *HTTPServer) handleAnthropicMessagesViaAdaptor(
	w http.ResponseWriter,
	r *http.Request,
	plan *relaybiz.RelayPlan,
	clientModel string,
	rawBody []byte,
) {
	s.handleSubscriptionAccountViaAdaptor(w, r, plan, clientModel, rawBody, relayadaptor.FormatAnthropicMessages)
}

func (s *HTTPServer) handleResponsesCreateLikeViaAdaptor(
	w http.ResponseWriter,
	r *http.Request,
	plan *relaybiz.RelayPlan,
	clientModel string,
	rawBody []byte,
) {
	s.handleSubscriptionAccountViaAdaptor(w, r, plan, clientModel, rawBody, relayadaptor.FormatOpenAIResponses)
}

func (s *HTTPServer) handleSubscriptionAccountViaAdaptor(
	w http.ResponseWriter,
	r *http.Request,
	plan *relaybiz.RelayPlan,
	clientModel string,
	rawBody []byte,
	inbound relayadaptor.Format,
) {
	if plan == nil || plan.Channel == nil {
		s.writeError(w, http.StatusInternalServerError, "no channel selected")
		return
	}

	ad, ok := relayadaptor.GetAdaptor(plan.Channel.Type)
	if !ok {
		s.writeError(w, http.StatusBadGateway, "no adaptor registered for subscription channel type")
		return
	}

	// Prefer the first-class subscription account selected during planning
	// (plan.Account), then the resolver, then the channel-fallback metadata.
	// The account carries the access token; the channel view intentionally
	// does not (see biz.RelayPlan.Account).
	meta := fallbackSubscriptionAccountMetadata(plan, plan.Channel)
	if plan.Account != nil {
		meta = subscriptionAccountMetadataFromPlan(plan.Account)
	} else if s.accountResolver != nil {
		if resolved, err := s.accountResolver.Resolve(r.Context(), plan.Channel.ID); err == nil && resolved != nil {
			meta = resolved
		}
	}

	// Build the relay context with the real account identity. Account.ID keys
	// the credential/identity caches; AccountID is the upstream account id
	// (chatgpt-account-id / Claude metadata user_id).
	rc := &relayadaptor.RelayContext{
		InboundFormat: inbound,
		ClientModel:   clientModel,
		ResolvedModel: plan.ResolvedModel,
		Channel:       plan.Channel,
		Account: &relayadaptor.AccountRef{
			ID:          meta.ID,
			Platform:    string(meta.Platform),
			AccountType: accountTypeOrDefault(meta.AccountType),
			GroupID:     meta.GroupID,
			AccountID:   meta.AccountID,
			AccessToken: meta.AccessToken,
			Fingerprint: meta.Fingerprint,
		},
		UserID:        plan.Auth.UserID,
		InboundHeader: r.Header.Clone(),
		RawBody:       rawBody,
	}
	// Carry the configured upstream HTTP client so the OAuth path respects the
	// gateway's timeout/proxy/transport settings instead of silently falling
	// back to http.DefaultClient.
	if s.oauthHTTPClient != nil {
		rc.HTTPClient = s.oauthHTTPClient
	}
	ad.Init(rc)

	// Convert the inbound request body to the upstream format.
	upstreamFmt, upstreamBody, err := ad.ConvertRequest(rc, inbound, rawBody)
	if err != nil {
		s.writeError(w, http.StatusBadGateway, fmt.Sprintf("adaptor convert request: %v", err))
		return
	}
	// (reservation happens after BuildUpstreamRequest so a build error does not
	//  leak a reservation; see below.)

	// Build the upstream http.Request (includes identity mimicry + OAuth token).
	upstreamReq, err := ad.BuildUpstreamRequest(r.Context(), rc, upstreamFmt, upstreamBody)
	if err != nil {
		s.writeError(w, http.StatusBadGateway, fmt.Sprintf("adaptor build request: %v", err))
		return
	}

	// Determine whether the client requested streaming.
	isStream := false
	var probe map[string]any
	if err := sonic.Unmarshal(rawBody, &probe); err == nil {
		if v, ok := probe["stream"].(bool); ok {
			isStream = v
		}
	}

	// Use the relay context's client (configured timeout/transport) rather than
	// http.DefaultClient so the OAuth path inherits the gateway's upstream
	// settings. Fall back to DefaultClient only when none is configured.
	client := rc.HTTPClient
	if client == nil {
		client = http.DefaultClient
	}
	resp, err := client.Do(upstreamReq)
	if err != nil {
		s.writeError(w, http.StatusBadGateway, fmt.Sprintf("upstream call: %v", err))
		return
	}
	defer resp.Body.Close()

	// Non-2xx: surface a sanitized upstream error. Upstream error bodies may
	// leak internal identifiers (the upstream's view of the subscription,
	// account-scoped request ids, etc.), so we never forward them verbatim to
	// the client.
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		io.Copy(io.Discard, io.LimitReader(resp.Body, 1<<20)) // drain so the connection can be reused
		s.writeError(w, resp.StatusCode, fmt.Sprintf("upstream returned status %d", resp.StatusCode))
		return
	}

	// Quota reservation for the subscription call (plan §5 step 8). We reserve
	// an estimate up front, then commit the real usage after the upstream
	// responds. Failures release the reservation. This mirrors the
	// API-key path; it is best-effort so a billing hiccup never blocks a
	// successful relay, but it ensures subscription accounts are no longer
	// free/unmetered. When the billing client is not configured (e.g. in tests
	// or a billing-less deployment) accounting is skipped.
	requestID := generateRequestID()
	channelID := fmt.Sprintf("%d", plan.Channel.ID)
	var reservation *billingv1.ReserveQuotaResponse
	accountUsage := s.billingClient != nil
	if accountUsage {
		var reserveErr error
		reservation, reserveErr = s.reserveQuota(
			r.Context(),
			fmt.Sprintf("%d", plan.Auth.UserID),
			requestID,
			estimateRawTokens(rawBody),
			plan.ResolvedModel,
			channelID,
			subscriptionAccountIDFromPlan(plan),
		)
		if reserveErr != nil {
			s.writeError(w, http.StatusPaymentRequired, fmt.Sprintf("reserve quota: %v", reserveErr))
			return
		}
	}

	if isStream {
		_, reader, err := ad.ConvertStreamResponse(rc, upstreamFmt, resp)
		if err != nil {
			if accountUsage {
				_ = s.releaseQuota(r.Context(), reservation.ReservationId, "adaptor convert stream error")
			}
			s.writeError(w, http.StatusInternalServerError, fmt.Sprintf("adaptor convert stream: %v", err))
			return
		}
		// Tee the converted SSE through a usage tracker so we can commit real
		// token counts. The converted output is already in the client's
		// protocol (chat/anthropic/responses), whose usage objects
		// extractRawUsage understands.
		usageTracker := newRawStreamUsageTracker(estimateRawUsage(rawBody))
		w.Header().Set("Content-Type", "text/event-stream")
		w.Header().Set("Cache-Control", "no-cache")
		w.Header().Set("Connection", "keep-alive")
		w.WriteHeader(http.StatusOK)
		if flusher, ok := w.(http.Flusher); ok {
			_, _ = io.Copy(&flushWriter{w: w, flusher: flusher, usageTracker: usageTracker}, reader)
		} else {
			_, _ = io.Copy(&streamUsageWriter{w: w, usageTracker: usageTracker}, reader)
		}
		if accountUsage {
			actualUsage := usageTracker.Usage()
			logInput := usageLogInput{
				UserID:           plan.Auth.UserID,
				TokenID:          plan.Auth.TokenID,
				TokenName:        plan.Auth.TokenName,
				RequestID:        requestID,
				Endpoint:         string(inbound),
				ModelName:        plan.ResolvedModel,
				Quota:            actualUsage.TotalTokens,
				PromptTokens:     actualUsage.PromptTokens,
				CompletionTokens: actualUsage.CompletionTokens,
				CacheReadTokens:       actualUsage.CacheReadTokens,
				ChannelID:             plan.Channel.ID,
				SubscriptionAccountID: subscriptionAccountIDFromPlan(plan),
				IsStream:              true,
			}
			if err := s.commitQuotaAfterResponse(reservation.ReservationId, actualUsage.TotalTokens, true, logInput); err != nil {
				s.logPostResponseCommitError(err)
			} else {
				s.ingestUsageLogAfterResponse(logInput)
			}
		}
		return
	}

	// Non-streaming: convert and write.
	_, outBody, err := ad.ConvertResponse(rc, upstreamFmt, resp)
	if err != nil {
		if accountUsage {
			_ = s.releaseQuota(r.Context(), reservation.ReservationId, "adaptor convert response error")
		}
		s.writeError(w, http.StatusInternalServerError, fmt.Sprintf("adaptor convert response: %v", err))
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(outBody)

	// Commit real usage from the converted response body.
	if accountUsage {
		usage := extractRawUsage(outBody, estimateRawTokens(rawBody))
		logInput := usageLogInput{
			UserID:           plan.Auth.UserID,
			TokenID:          plan.Auth.TokenID,
			TokenName:        plan.Auth.TokenName,
			RequestID:        requestID,
			Endpoint:         string(inbound),
			ModelName:        plan.ResolvedModel,
			Quota:            usage.TotalTokens,
			PromptTokens:     usage.PromptTokens,
			CompletionTokens: usage.CompletionTokens,
			CacheReadTokens:      usage.CacheReadTokens,
			ChannelID:            plan.Channel.ID,
			SubscriptionAccountID: subscriptionAccountIDFromPlan(plan),
		}
		if err := s.commitQuotaAfterResponse(reservation.ReservationId, usage.TotalTokens, true, logInput); err != nil {
			s.logPostResponseCommitError(err)
		} else {
			s.ingestUsageLogAfterResponse(logInput)
		}
	}
}

// accountTypeOrDefault returns the subscription account type, defaulting to
// "oauth" for legacy records that do not carry an explicit account_type.
func accountTypeOrDefault(t string) string {
	if t == "" {
		return "oauth"
	}
	return t
}

// subscriptionAccountMetadataFromPlan projects the first-class subscription
// account selected during planning into the metadata the relay context needs.
// This is the canonical path: the access token, upstream account id and
// fingerprint all come from the account entity, not from the generic channel.
func subscriptionAccountMetadataFromPlan(a *relaybiz.SubscriptionAccount) *relaycredential.SubscriptionAccountMetadata {
	if a == nil {
		return nil
	}
	platform := relaycredential.PlatformClaude
	switch a.Platform {
	case "codex":
		platform = relaycredential.PlatformCodex
	case "claude":
		platform = relaycredential.PlatformClaude
	}
	return &relaycredential.SubscriptionAccountMetadata{
		ID:          a.ID,
		AccessToken: a.AccessToken,
		AccountID:   a.AccountID,
		Platform:    platform,
		AccountType: accountTypeOrDefault(a.AccountType),
		Fingerprint: a.Fingerprint,
		GroupID:     a.Group,
	}
}

// subscriptionAccountIDFromPlan returns the real subscription account id
// selected during planning, or 0 for ordinary API-key channels.
func subscriptionAccountIDFromPlan(plan *relaybiz.RelayPlan) int64 {
	if plan == nil || plan.Account == nil {
		return 0
	}
	return plan.Account.ID
}

func fallbackSubscriptionAccountMetadata(plan *relaybiz.RelayPlan, ch *relaybiz.Channel) *relaycredential.SubscriptionAccountMetadata {
	meta := &relaycredential.SubscriptionAccountMetadata{
		ID:       ch.ID,
		GroupID:  ch.Group,
		Platform: relaycredential.PlatformClaude,
	}
	meta.AccountID = fmt.Sprintf("%d", ch.ID)
	switch ch.Type {
	case relayprovider.ChannelTypeCodexOAuth:
		meta.Platform = relaycredential.PlatformCodex
	case relayprovider.ChannelTypeClaudeOAuth:
		meta.Platform = relaycredential.PlatformClaude
	}
	meta.AccessToken = ch.Key
	return meta
}

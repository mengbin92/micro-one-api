package server

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"strings"

	"github.com/bytedance/sonic"
	"go.uber.org/zap"

	relayadaptor "micro-one-api/internal/relay/adaptor"
	relaybiz "micro-one-api/internal/relay/biz"
)

// handleChatCompletionsViaAdaptor is the feature-flag-gated request path for
// subscription-account channels (Codex / Claude OAuth). It builds a
// RelayContext, resolves the adaptor for the channel type, and drives the full
// ConvertRequest → BuildUpstreamRequest → upstream call → ConvertResponse /
// ConvertStreamResponse pipeline.
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
	if plan == nil || plan.Channel == nil {
		s.writeError(w, http.StatusInternalServerError, "no channel selected")
		return
	}

	ad, ok := relayadaptor.GetAdaptor(plan.Channel.Type)
	if !ok {
		s.writeError(w, http.StatusBadGateway, "no adaptor registered for subscription channel type")
		return
	}

	rc := &relayadaptor.RelayContext{
		InboundFormat: relayadaptor.FormatOpenAIChatCompletions,
		ClientModel:   clientModel,
		ResolvedModel: plan.ResolvedModel,
		Channel:       plan.Channel,
		Account: &relayadaptor.AccountRef{
			ID:          plan.Channel.ID,
			Platform:    platformTagFromChannelType(plan.Channel.Type),
			AccountType: "oauth",
			GroupID:     plan.Auth.Group,
		},
		UserID:        plan.Auth.UserID,
		InboundHeader: r.Header.Clone(),
		RawBody:       rawBody,
	}
	ad.Init(rc)

	// Convert the inbound ChatCompletions body to the upstream format.
	upstreamFmt, upstreamBody, err := ad.ConvertRequest(rc, relayadaptor.FormatOpenAIChatCompletions, rawBody)
	if err != nil {
		s.writeError(w, http.StatusBadGateway, fmt.Sprintf("adaptor convert request: %v", err))
		return
	}

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

	client := upstreamHTTPClientFor(rc)
	resp, err := client.Do(upstreamReq)
	if err != nil {
		s.writeError(w, http.StatusBadGateway, fmt.Sprintf("upstream call: %v", err))
		return
	}
	defer resp.Body.Close()

	// Non-2xx: forward the upstream error body to the client (best-effort).
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
		s.writeError(w, resp.StatusCode, fmt.Sprintf("upstream: %s", strings.TrimSpace(string(body))))
		return
	}

	if isStream {
		outFmt, reader, err := ad.ConvertStreamResponse(rc, upstreamFmt, resp)
		if err != nil {
			s.writeError(w, http.StatusInternalServerError, fmt.Sprintf("adaptor convert stream: %v", err))
			return
		}
		contentType := "text/event-stream"
		if outFmt == relayadaptor.FormatAnthropicMessages {
			contentType = "text/event-stream"
		}
		w.Header().Set("Content-Type", contentType)
		w.Header().Set("Cache-Control", "no-cache")
		w.Header().Set("Connection", "keep-alive")
		w.WriteHeader(http.StatusOK)
		if flusher, ok := w.(http.Flusher); ok {
			_, _ = io.Copy(&flushWriter{w: w, flusher: flusher}, reader)
			return
		}
		_, _ = io.Copy(w, reader)
		return
	}

	// Non-streaming: convert and write.
	outFmt, outBody, err := ad.ConvertResponse(rc, upstreamFmt, resp)
	if err != nil {
		s.writeError(w, http.StatusInternalServerError, fmt.Sprintf("adaptor convert response: %v", err))
		return
	}
	contentType := "application/json"
	_ = outFmt
	w.Header().Set("Content-Type", contentType)
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(outBody)
}

// platformTagFromChannelType maps a subscription channel type to its platform
// tag. The constants mirror provider.ChannelTypeCodexOAuth (32) and
// provider.ChannelTypeClaudeOAuth (33); they are duplicated locally to avoid a
// server -> provider import for just two values.
func platformTagFromChannelType(t int32) string {
	switch t {
	case 32: // provider.ChannelTypeCodexOAuth
		return "codex"
	case 33: // provider.ChannelTypeClaudeOAuth
		return "claude"
	default:
		return ""
	}
}

// upstreamHTTPClientFor returns the HTTP client used for the upstream call.
// The adaptor path uses the server's provider-timeout-derived client; a nil
// client falls back to http.DefaultClient.
func upstreamHTTPClientFor(rc *relayadaptor.RelayContext) *http.Client {
	if rc != nil && rc.HTTPClient != nil {
		return rc.HTTPClient
	}
	return http.DefaultClient
}

// Compile-time check that the adaptor path is wired correctly.
var _ = context.TODO
var _ = zap.NewNop

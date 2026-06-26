package adaptor

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"strings"

	"github.com/bytedance/sonic"

	"micro-one-api/internal/relay/apicompat"
	"micro-one-api/internal/relay/credential"
	"micro-one-api/internal/relay/identity"
)

// ClaudeOAuthAdaptor serves Claude Code subscription accounts. Unlike the
// API-key adaptors it does not wrap a provider.Provider; instead it owns the
// full upstream interaction:
//
//   - ConvertRequest bridges any inbound format to Anthropic Messages via the
//     Responses hub (apicompat), because the upstream speaks anthropic_messages.
//   - BuildUpstreamRequest applies the three-dimension mimicry (headers,
//     metadata, fingerprint) and injects a refreshed OAuth access token.
//   - ConvertStreamResponse bridges the Anthropic SSE stream back to the
//     client's inbound format (Responses or ChatCompletions).
//
// MVP scope (plan §十): inbound formats supported are chat_completions and
// responses (both via Responses→AnthropicRequest) and a direct
// anthropic_messages passthrough.
type ClaudeOAuthAdaptor struct {
	baseAdaptor
	tokens   credential.TokenProvider
	identity *identity.IdentityService
	httpc    *http.Client
	models   []string
}

var claudeOAuthModels = []string{
	"claude-sonnet-4-20250514",
	"claude-opus-4-20250514",
	"claude-3-5-sonnet-20241022",
}

// NewClaudeOAuthAdaptor builds an adaptor for Claude Code subscription
// accounts. tokens resolves the OAuth access token; svc resolves the account
// fingerprint and drives mimicry. httpc is used for the upstream call; a nil
// client falls back to http.DefaultClient.
func NewClaudeOAuthAdaptor(tokens credential.TokenProvider, svc *identity.IdentityService, httpc *http.Client, models []string) *ClaudeOAuthAdaptor {
	if len(models) == 0 {
		models = claudeOAuthModels
	}
	if httpc == nil {
		httpc = http.DefaultClient
	}
	return &ClaudeOAuthAdaptor{tokens: tokens, identity: svc, httpc: httpc, models: models}
}

func (a *ClaudeOAuthAdaptor) Init(_ *RelayContext) {}

func (a *ClaudeOAuthAdaptor) Name() string { return "claude_oauth" }

func (a *ClaudeOAuthAdaptor) ModelList() []string { return a.models }

// ConvertRequest bridges the inbound format to the upstream
// anthropic_messages format via the Responses hub.
func (a *ClaudeOAuthAdaptor) ConvertRequest(_ *RelayContext, inbound Format, body []byte) (Format, []byte, error) {
	switch inbound {
	case FormatAnthropicMessages:
		// Already the upstream format: pass through (mimicry rewrites happen in
		// BuildUpstreamRequest).
		return FormatAnthropicMessages, body, nil
	case FormatOpenAIResponses:
		// Responses → Anthropic Messages.
		var rr apicompat.ResponsesRequest
		if err := sonic.Unmarshal(body, &rr); err != nil {
			return "", nil, fmt.Errorf("claude_oauth: parse responses request: %w", err)
		}
		ar, err := apicompat.ResponsesToAnthropicRequest(&rr)
		if err != nil {
			return "", nil, fmt.Errorf("claude_oauth: responses→anthropic: %w", err)
		}
		out, err := sonic.Marshal(ar)
		if err != nil {
			return "", nil, err
		}
		return FormatAnthropicMessages, out, nil
	case FormatOpenAIChatCompletions:
		// ChatCompletions → Responses → Anthropic Messages.
		var cr apicompat.ChatCompletionsRequest
		if err := sonic.Unmarshal(body, &cr); err != nil {
			return "", nil, fmt.Errorf("claude_oauth: parse chat request: %w", err)
		}
		rr, err := apicompat.ChatCompletionsToResponses(&cr)
		if err != nil {
			return "", nil, fmt.Errorf("claude_oauth: chat→responses: %w", err)
		}
		ar, err := apicompat.ResponsesToAnthropicRequest(rr)
		if err != nil {
			return "", nil, fmt.Errorf("claude_oauth: responses→anthropic: %w", err)
		}
		out, err := sonic.Marshal(ar)
		if err != nil {
			return "", nil, err
		}
		return FormatAnthropicMessages, out, nil
	default:
		return "", nil, fmt.Errorf("claude_oauth: inbound format %q not supported", inbound)
	}
}

// GetUpstreamURL returns the Anthropic Messages endpoint with the beta flag.
func (a *ClaudeOAuthAdaptor) GetUpstreamURL(ctx *RelayContext) (string, error) {
	base := "https://api.anthropic.com"
	if ctx != nil && ctx.Channel != nil && ctx.Channel.BaseURL != "" {
		base = ctx.Channel.BaseURL
	}
	return strings.TrimRight(base, "/") + "/v1/messages?beta=true", nil
}

// BuildUpstreamRequest constructs the POST /v1/messages request, applying the
// three-dimension mimicry (headers, metadata, fingerprint) and injecting the
// refreshed OAuth access token.
func (a *ClaudeOAuthAdaptor) BuildUpstreamRequest(ctx context.Context, rc *RelayContext, upstream Format, body []byte) (*http.Request, error) {
	if rc == nil || rc.Account == nil {
		return nil, fmt.Errorf("claude_oauth: relay context has no subscription account")
	}
	if a.tokens == nil {
		return nil, fmt.Errorf("claude_oauth: no token provider configured")
	}

	token, err := a.tokens.GetAccessToken(ctx, rc.Account.ID)
	if err != nil {
		return nil, fmt.Errorf("claude_oauth: resolve access token: %w", err)
	}

	// Resolve fingerprint for the account.
	var fp identity.Fingerprint
	if a.identity != nil {
		fp, err = a.identity.GetOrCreateFingerprint(identity.AccountKey{
			ID:       rc.Account.ID,
			Platform: identity.PlatformClaude,
			Snapshot: identity.FingerprintSnapshot(rc.Account.Fingerprint),
			IsOAuth:  rc.Account.AccountType == "oauth",
		})
		if err != nil {
			return nil, fmt.Errorf("claude_oauth: resolve fingerprint: %w", err)
		}
	} else {
		fp = identity.DefaultClaudeCodeFingerprint()
	}

	// Apply mimicry to the body when the inbound client is not a genuine
	// Claude Code client.
	mimic := identity.ShouldMimic(identity.PlatformClaude, rc.Account.AccountType == "oauth", rc.InboundHeader)
	if mimic {
		if body, err = identity.InjectClaudeCodeSystemPrompt(body); err != nil {
			return nil, err
		}
		if body, err = identity.RewriteMetadataUserID(body, rc.Account.AccountID, fp.ClientID); err != nil {
			return nil, err
		}
		if body, err = identity.NormalizeClaudeOAuthRequestBody(body); err != nil {
			return nil, err
		}
	}

	url, err := a.GetUpstreamURL(rc)
	if err != nil {
		return nil, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytesReader(body))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+token)
	// anthropic-beta: computed to match a Claude Code session.
	betaInbound := ""
	if rc.InboundHeader != nil {
		betaInbound = rc.InboundHeader.Get("anthropic-beta")
	}
	req.Header.Set("anthropic-beta", identity.ComputeAnthropicBeta(betaInbound))
	if mimic {
		applyClaudeFingerprintHeaders(req.Header, fp)
	}
	return req, nil
}

// ConvertResponse converts a non-streaming Anthropic Messages response back to
// the client's inbound format.
func (a *ClaudeOAuthAdaptor) ConvertResponse(rc *RelayContext, upstream Format, resp *http.Response) (Format, []byte, error) {
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", nil, fmt.Errorf("claude_oauth: read upstream response: %w", err)
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return "", nil, fmt.Errorf("claude_oauth: upstream status=%d body=%s", resp.StatusCode, string(body))
	}
	if rc == nil {
		return FormatAnthropicMessages, body, nil
	}
	switch rc.InboundFormat {
	case FormatOpenAIResponses:
		var ar apicompat.AnthropicResponse
		if err := sonic.Unmarshal(body, &ar); err != nil {
			return FormatAnthropicMessages, body, nil
		}
		rr := apicompat.AnthropicToResponsesResponse(&ar)
		out, err := sonic.Marshal(rr)
		if err != nil {
			return FormatAnthropicMessages, body, nil
		}
		return FormatOpenAIResponses, out, nil
	case FormatOpenAIChatCompletions:
		var ar apicompat.AnthropicResponse
		if err := sonic.Unmarshal(body, &ar); err != nil {
			return FormatAnthropicMessages, body, nil
		}
		rr := apicompat.AnthropicToResponsesResponse(&ar)
		cr := apicompat.ResponsesToChatCompletions(rr, rc.ClientModel)
		out, err := sonic.Marshal(cr)
		if err != nil {
			return FormatAnthropicMessages, body, nil
		}
		return FormatOpenAIChatCompletions, out, nil
	default:
		return FormatAnthropicMessages, body, nil
	}
}

// ConvertStreamResponse converts a streaming Anthropic Messages SSE response
// back to the client's inbound format.
func (a *ClaudeOAuthAdaptor) ConvertStreamResponse(rc *RelayContext, upstream Format, resp *http.Response) (Format, io.Reader, error) {
	if rc == nil {
		return FormatAnthropicMessages, resp.Body, nil
	}
	switch rc.InboundFormat {
	case FormatOpenAIResponses:
		// Anthropic SSE → Responses SSE.
		pr, pw := io.Pipe()
		go pumpAnthropicToResponses(resp.Body, pw)
		return FormatOpenAIResponses, pr, nil
	case FormatOpenAIChatCompletions:
		// Anthropic SSE → Responses SSE → ChatCompletions SSE.
		pr, pw := io.Pipe()
		go pumpAnthropicToChat(resp.Body, pw, rc.ClientModel)
		return FormatOpenAIChatCompletions, pr, nil
	default:
		return FormatAnthropicMessages, resp.Body, nil
	}
}

// applyClaudeFingerprintHeaders stamps the x-stainless-* and User-Agent headers
// so the upstream sees a first-party Claude Code SDK request.
func applyClaudeFingerprintHeaders(h http.Header, fp identity.Fingerprint) {
	h.Set("User-Agent", fp.UserAgent)
	h.Set("x-stainless-lang", fp.StainlessLang)
	h.Set("x-stainless-package-version", fp.StainlessPackageVersion)
	h.Set("x-stainless-os", fp.StainlessOS)
	h.Set("x-stainless-arch", fp.StainlessArch)
	h.Set("x-stainless-runtime", fp.StainlessRuntime)
	h.Set("x-stainless-runtime-version", fp.StainlessRuntimeVersion)
	h.Set("x-app", "cli")
}

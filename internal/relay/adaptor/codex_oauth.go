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

// CodexOAuthAdaptor serves ChatGPT / Codex subscription accounts. The upstream
// speaks the OpenAI Responses API, so inbound requests are bridged to
// responses via apicompat; responses streams are bridged back to the client's
// inbound format.
//
// Mimicry (plan §十 dimension 3): when the inbound client is not a genuine
// codex_cli_rs client, the request is stamped with the codex_cli_rs
// headers (originator, chatgpt-account-id, OpenAI-Beta, User-Agent) so the
// upstream treats it as a first-party Codex CLI session.
type CodexOAuthAdaptor struct {
	baseAdaptor
	tokens   credential.TokenProvider
	identity *identity.IdentityService
	httpc    *http.Client
	models   []string
}

var codexOAuthModels = []string{
	"gpt-5",
	"gpt-5-codex",
	"codex-mini-latest",
	"o4-mini",
}

// NewCodexOAuthAdaptor builds an adaptor for Codex/ChatGPT subscription
// accounts.
func NewCodexOAuthAdaptor(tokens credential.TokenProvider, svc *identity.IdentityService, httpc *http.Client, models []string) *CodexOAuthAdaptor {
	if len(models) == 0 {
		models = codexOAuthModels
	}
	if httpc == nil {
		httpc = http.DefaultClient
	}
	return &CodexOAuthAdaptor{tokens: tokens, identity: svc, httpc: httpc, models: models}
}

func (a *CodexOAuthAdaptor) Init(_ *RelayContext) {}

func (a *CodexOAuthAdaptor) Name() string { return "codex_oauth" }

func (a *CodexOAuthAdaptor) ModelList() []string { return a.models }

// ConvertRequest bridges the inbound format to the upstream responses format
// via apicompat.
func (a *CodexOAuthAdaptor) ConvertRequest(_ *RelayContext, inbound Format, body []byte) (Format, []byte, error) {
	switch inbound {
	case FormatOpenAIResponses:
		return FormatOpenAIResponses, body, nil
	case FormatOpenAIChatCompletions:
		var cr apicompat.ChatCompletionsRequest
		if err := sonic.Unmarshal(body, &cr); err != nil {
			return "", nil, fmt.Errorf("codex_oauth: parse chat request: %w", err)
		}
		rr, err := apicompat.ChatCompletionsToResponses(&cr)
		if err != nil {
			return "", nil, fmt.Errorf("codex_oauth: chat→responses: %w", err)
		}
		out, err := sonic.Marshal(rr)
		if err != nil {
			return "", nil, err
		}
		return FormatOpenAIResponses, out, nil
	case FormatAnthropicMessages:
		var ar apicompat.AnthropicRequest
		if err := sonic.Unmarshal(body, &ar); err != nil {
			return "", nil, fmt.Errorf("codex_oauth: parse anthropic request: %w", err)
		}
		rr, err := apicompat.AnthropicToResponses(&ar)
		if err != nil {
			return "", nil, fmt.Errorf("codex_oauth: anthropic→responses: %w", err)
		}
		out, err := sonic.Marshal(rr)
		if err != nil {
			return "", nil, err
		}
		return FormatOpenAIResponses, out, nil
	default:
		return "", nil, fmt.Errorf("codex_oauth: inbound format %q not supported", inbound)
	}
}

// GetUpstreamURL returns the Codex backend responses endpoint.
func (a *CodexOAuthAdaptor) GetUpstreamURL(ctx *RelayContext) (string, error) {
	base := "https://chatgpt.com/backend-api/codex"
	if ctx != nil && ctx.Channel != nil && ctx.Channel.BaseURL != "" {
		base = ctx.Channel.BaseURL
	}
	return strings.TrimRight(base, "/") + "/responses", nil
}

// BuildUpstreamRequest constructs the POST /responses request, injecting the
// refreshed OAuth access token and the codex_cli_rs identity headers.
func (a *CodexOAuthAdaptor) BuildUpstreamRequest(ctx context.Context, rc *RelayContext, upstream Format, body []byte) (*http.Request, error) {
	if rc == nil || rc.Account == nil {
		return nil, fmt.Errorf("codex_oauth: relay context has no subscription account")
	}
	if a.tokens == nil {
		return nil, fmt.Errorf("codex_oauth: no token provider configured")
	}

	token, err := a.tokens.GetAccessToken(ctx, rc.Account.ID)
	if err != nil {
		return nil, fmt.Errorf("codex_oauth: resolve access token: %w", err)
	}

	// Resolve fingerprint (used for the User-Agent / originator headers).
	var fp identity.Fingerprint
	if a.identity != nil {
		fp, err = a.identity.GetOrCreateFingerprint(identity.AccountKey{
			ID:       rc.Account.ID,
			Platform: identity.PlatformCodex,
			Snapshot: identity.FingerprintSnapshot(rc.Account.Fingerprint),
			IsOAuth:  rc.Account.AccountType == "oauth",
		})
		if err != nil {
			return nil, fmt.Errorf("codex_oauth: resolve fingerprint: %w", err)
		}
	} else {
		fp = identity.DefaultCodexFingerprint()
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
	// chatgpt-account-id: required by the Codex backend to scope the request to
	// the subscription's account.
	if rc.Account.AccountID != "" {
		req.Header.Set("chatgpt-account-id", rc.Account.AccountID)
	}
	// originator + OpenAI-Beta make the request look like it came from the
	// official codex_cli_rs client.
	req.Header.Set("originator", "codex_cli_rs")
	req.Header.Set("OpenAI-Beta", "responses=experimental")
	mimic := identity.ShouldMimic(identity.PlatformCodex, rc.Account.AccountType == "oauth", rc.InboundHeader)
	if mimic {
		applyCodexFingerprintHeaders(req.Header, fp)
	} else if rc.InboundHeader != nil {
		// Genuine codex client: forward its original identity headers.
		if ua := rc.InboundHeader.Get("User-Agent"); ua != "" {
			req.Header.Set("User-Agent", ua)
		}
		if o := rc.InboundHeader.Get("originator"); o != "" {
			req.Header.Set("originator", o)
		}
	}
	return req, nil
}

// ConvertResponse converts a non-streaming Responses response back to the
// client's inbound format.
func (a *CodexOAuthAdaptor) ConvertResponse(rc *RelayContext, upstream Format, resp *http.Response) (Format, []byte, error) {
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", nil, fmt.Errorf("codex_oauth: read upstream response: %w", err)
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return "", nil, fmt.Errorf("codex_oauth: upstream status=%d body=%s", resp.StatusCode, string(body))
	}
	if rc == nil {
		return FormatOpenAIResponses, body, nil
	}
	switch rc.InboundFormat {
	case FormatOpenAIChatCompletions:
		var rr apicompat.ResponsesResponse
		if err := sonic.Unmarshal(body, &rr); err != nil {
			return FormatOpenAIResponses, body, nil
		}
		cr := apicompat.ResponsesToChatCompletions(&rr, rc.ClientModel)
		out, err := sonic.Marshal(cr)
		if err != nil {
			return FormatOpenAIResponses, body, nil
		}
		return FormatOpenAIChatCompletions, out, nil
	case FormatAnthropicMessages:
		var rr apicompat.ResponsesResponse
		if err := sonic.Unmarshal(body, &rr); err != nil {
			return FormatOpenAIResponses, body, nil
		}
		ar := apicompat.ResponsesToAnthropic(&rr, rc.ClientModel)
		out, err := sonic.Marshal(ar)
		if err != nil {
			return FormatOpenAIResponses, body, nil
		}
		return FormatAnthropicMessages, out, nil
	default:
		return FormatOpenAIResponses, body, nil
	}
}

// ConvertStreamResponse converts a streaming Responses SSE response back to
// the client's inbound format.
func (a *CodexOAuthAdaptor) ConvertStreamResponse(rc *RelayContext, upstream Format, resp *http.Response) (Format, io.Reader, error) {
	if rc == nil {
		return FormatOpenAIResponses, resp.Body, nil
	}
	switch rc.InboundFormat {
	case FormatOpenAIChatCompletions:
		pr, pw := io.Pipe()
		go pumpResponsesToChat(resp.Body, pw, rc.ClientModel)
		return FormatOpenAIChatCompletions, pr, nil
	case FormatAnthropicMessages:
		pr, pw := io.Pipe()
		go pumpResponsesToAnthropic(resp.Body, pw)
		return FormatAnthropicMessages, pr, nil
	default:
		return FormatOpenAIResponses, resp.Body, nil
	}
}

// applyCodexFingerprintHeaders stamps the codex_cli_rs identity headers.
func applyCodexFingerprintHeaders(h http.Header, fp identity.Fingerprint) {
	h.Set("User-Agent", fp.UserAgent)
	h.Set("version", "0.39.0")
	h.Set("originator", "codex_cli_rs")
}

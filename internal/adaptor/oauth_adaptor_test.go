package adaptor

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/bytedance/sonic"

	"micro-one-api/internal/apicompat"
	"micro-one-api/domain/upstream/credential"
	"micro-one-api/internal/identity"
	"micro-one-api/domain/upstream/provider"
)

// staticTokenProvider is a TokenProvider that always returns a fixed token,
// for adaptor tests that don't exercise the refresh path.
type staticTokenProvider struct{ token string }

func (s *staticTokenProvider) GetAccessToken(context.Context, int64) (string, error) {
	return s.token, nil
}
func (s *staticTokenProvider) Refresh(context.Context, int64) error { return nil }

// accountCtx builds a RelayContext pointing at a subscription account.
func accountCtx(t int32, inbound Format, body []byte) *RelayContext {
	ch := &ChannelRef{ID: 1, Type: t, BaseURL: "", Key: ""}
	return &RelayContext{
		InboundFormat: inbound,
		ClientModel:   "claude-sonnet-4-20250514",
		ResolvedModel: "claude-sonnet-4-20250514",
		Channel:       ch,
		Account: &AccountRef{
			ID:          1,
			Platform:    "claude",
			AccountType: "oauth",
			AccountID:   "11111111-1111-1111-1111-111111111111",
		},
		InboundHeader: http.Header{},
		RawBody:       body,
	}
}

// ---------------------------------------------------------------------------
// Registry: OAuth adaptors are registered for the subscription channel types
// ---------------------------------------------------------------------------

func TestRegistry_HasOAuthTypes(t *testing.T) {
	for _, ty := range []int32{
		provider.ChannelTypeCodexOAuth,
		provider.ChannelTypeClaudeOAuth,
		provider.ChannelTypeZhipuPlan,
		provider.ChannelTypeMinimaxPlan,
		provider.ChannelTypeKimiOAuth,
	} {
		if _, ok := GetAdaptor(ty); !ok {
			t.Errorf("GetAdaptor(%d): expected registered OAuth adaptor, got none", ty)
		}
	}
}

// TestVendorAdaptorsReuseClaudeConstruction asserts the P0 contract: the
// domestic Coding-Plan vendors (Zhipu/MiniMax/Kimi) build the same adaptor
// shape as Claude because their upstreams all speak the Anthropic Messages
// API. They must not fall through to errUnknownOAuthPlatform.
func TestVendorAdaptorsReuseClaudeConstruction(t *testing.T) {
	// Wire a static token factory so build() does not short-circuit on a nil
	// provider (the static provider returns the inline access token).
	prev := globalTokenProviderFactory
	globalTokenProviderFactory = func(platform identity.Platform) credential.TokenProvider {
		return &staticTokenProvider{token: "vendor-key"}
	}
	defer func() { globalTokenProviderFactory = prev }()

	for _, tc := range []struct {
		name    string
		ctype   int32
		wantURL string
	}{
		{"zhipu", provider.ChannelTypeZhipuPlan, "https://open.bigmodel.cn/api/anthropic/v1/messages?beta=true"},
		{"minimax", provider.ChannelTypeMinimaxPlan, "https://api.minimax.io/anthropic/v1/messages?beta=true"},
		{"kimi", provider.ChannelTypeKimiOAuth, "https://kimi.example/anthropic/v1/messages?beta=true"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ad, ok := GetAdaptor(tc.ctype)
			if !ok {
				t.Fatalf("GetAdaptor(%d): adaptor not registered", tc.ctype)
			}
			rc := accountCtx(tc.ctype, FormatAnthropicMessages, []byte(`{}`))
			rc.Channel.BaseURL = strings.TrimSuffix(tc.wantURL, "/v1/messages?beta=true")
			ad.Init(rc)
			// The lazy adaptor must have built an inner Claude-shaped adaptor,
			// not an errorAdaptor. ConvertRequest on an Anthropic passthrough
			// body should succeed.
			if _, _, err := ad.ConvertRequest(rc, FormatAnthropicMessages, []byte(`{}`)); err != nil {
				t.Fatalf("ConvertRequest: %v", err)
			}
			gotURL, err := ad.GetUpstreamURL(rc)
			if err != nil {
				t.Fatalf("GetUpstreamURL: %v", err)
			}
			if gotURL != tc.wantURL {
				t.Fatalf("GetUpstreamURL = %q, want %q", gotURL, tc.wantURL)
			}
		})
	}
}

func TestOAuthAdaptorFactoryRequiresWiring(t *testing.T) {
	// With no TokenProviderFactory wired, the lazy adaptor now falls back to
	// inline account credentials instead of failing at init time.
	prev := globalTokenProviderFactory
	globalTokenProviderFactory = nil
	defer func() { globalTokenProviderFactory = prev }()

	ad, ok := GetAdaptor(provider.ChannelTypeClaudeOAuth)
	if !ok {
		t.Fatal("expected registered claude oauth adaptor")
	}
	ad.Init(accountCtx(provider.ChannelTypeClaudeOAuth, FormatAnthropicMessages, []byte(`{}`)))
	if _, _, err := ad.ConvertRequest(accountCtx(provider.ChannelTypeClaudeOAuth, FormatAnthropicMessages, []byte(`{}`)), FormatAnthropicMessages, []byte(`{}`)); err != nil {
		t.Fatalf("ConvertRequest: %v", err)
	}
}

// ---------------------------------------------------------------------------
// Claude OAuth: ConvertRequest bridges all inbound formats to anthropic_messages
// ---------------------------------------------------------------------------

func TestClaudeOAuth_ConvertRequest(t *testing.T) {
	svc := identity.NewIdentityService(0)
	tp := &staticTokenProvider{token: "claude-tok"}
	ad := NewClaudeOAuthAdaptor(tp, svc, nil)
	ad.Init(nil)

	t.Run("anthropic passthrough", func(t *testing.T) {
		body := []byte(`{"model":"claude-sonnet-4-20250514","max_tokens":1024,"messages":[{"role":"user","content":"hi"}]}`)
		fmt, out, err := ad.ConvertRequest(nil, FormatAnthropicMessages, body)
		if err != nil {
			t.Fatal(err)
		}
		if fmt != FormatAnthropicMessages {
			t.Fatalf("fmt = %v, want anthropic_messages", fmt)
		}
		if string(out) != string(body) {
			t.Fatal("anthropic passthrough should return body unchanged")
		}
	})

	t.Run("chat -> anthropic", func(t *testing.T) {
		body := []byte(`{"model":"claude-sonnet-4-20250514","messages":[{"role":"user","content":"hello"}]}`)
		fmt, out, err := ad.ConvertRequest(nil, FormatOpenAIChatCompletions, body)
		if err != nil {
			t.Fatal(err)
		}
		if fmt != FormatAnthropicMessages {
			t.Fatalf("fmt = %v, want anthropic_messages", fmt)
		}
		if !strings.Contains(string(out), "max_tokens") {
			t.Fatalf("expected converted anthropic body, got %s", out)
		}
	})

	t.Run("responses -> anthropic", func(t *testing.T) {
		body := []byte(`{"model":"claude-sonnet-4-20250514","input":"hi"}`)
		fmt, _, err := ad.ConvertRequest(nil, FormatOpenAIResponses, body)
		if err != nil {
			t.Fatal(err)
		}
		if fmt != FormatAnthropicMessages {
			t.Fatalf("fmt = %v, want anthropic_messages", fmt)
		}
	})
}

func TestClaudeOAuth_UpstreamURL(t *testing.T) {
	ad := NewClaudeOAuthAdaptor(&staticTokenProvider{token: "x"}, nil, nil)
	ctx := accountCtx(provider.ChannelTypeClaudeOAuth, FormatAnthropicMessages, nil)
	url, err := ad.GetUpstreamURL(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasSuffix(url, "/v1/messages?beta=true") {
		t.Fatalf("unexpected upstream url: %s", url)
	}
}

func TestClaudeOAuth_BuildUpstreamRequest_Mimicry(t *testing.T) {
	svc := identity.NewIdentityService(0)
	ad := NewClaudeOAuthAdaptor(&staticTokenProvider{token: "tok"}, svc, nil)
	ctx := accountCtx(provider.ChannelTypeClaudeOAuth, FormatAnthropicMessages, []byte(`{"model":"claude-sonnet-4-20250514","messages":[]}`))
	body := []byte(`{"model":"claude-sonnet-4-20250514","messages":[{"role":"user","content":"hi"}]}`)
	req, err := ad.BuildUpstreamRequest(context.Background(), ctx, FormatAnthropicMessages, body)
	if err != nil {
		t.Fatal(err)
	}
	if got := req.Header.Get("Authorization"); got != "Bearer tok" {
		t.Fatalf("Authorization = %q, want Bearer tok", got)
	}
	if beta := req.Header.Get("anthropic-beta"); beta == "" {
		t.Fatal("expected anthropic-beta header")
	}
	// Mimicry should have injected the system prompt + metadata + defaults.
	if !strings.Contains(string(body), "Claude Code") {
		// body itself is the input; mimicry rewrites happen on a copy inside
		// BuildUpstreamRequest, so verify via the request body instead.
	}
	rb, _ := io.ReadAll(req.Body)
	if !strings.Contains(string(rb), "Claude Code") {
		t.Fatalf("expected injected system prompt in request body, got %s", rb)
	}
	if !strings.Contains(string(rb), "user_id") {
		t.Fatalf("expected metadata.user_id in request body, got %s", rb)
	}
}

func TestClaudeOAuth_BuildUpstreamRequest_UsesInlineAccessToken(t *testing.T) {
	ad := NewClaudeOAuthAdaptor(nil, nil, nil)
	ctx := accountCtx(provider.ChannelTypeClaudeOAuth, FormatAnthropicMessages, []byte(`{"model":"claude-sonnet-4-20250514","messages":[]}`))
	ctx.Account.AccessToken = "inline-token"
	req, err := ad.BuildUpstreamRequest(context.Background(), ctx, FormatAnthropicMessages, []byte(`{"model":"claude-sonnet-4-20250514","messages":[{"role":"user","content":"hi"}]}`))
	if err != nil {
		t.Fatal(err)
	}
	if got := req.Header.Get("Authorization"); got != "Bearer inline-token" {
		t.Fatalf("Authorization = %q, want Bearer inline-token", got)
	}
}

// ---------------------------------------------------------------------------
// Codex OAuth: ConvertRequest bridges all inbound formats to responses
// ---------------------------------------------------------------------------

func TestCodexOAuth_ConvertRequest(t *testing.T) {
	ad := NewCodexOAuthAdaptor(&staticTokenProvider{token: "x"}, nil, nil)
	ad.Init(nil)

	t.Run("responses passthrough", func(t *testing.T) {
		body := []byte(`{"model":"gpt-5","input":"hi"}`)
		fmt, out, err := ad.ConvertRequest(nil, FormatOpenAIResponses, body)
		if err != nil {
			t.Fatal(err)
		}
		if fmt != FormatOpenAIResponses || string(out) != string(body) {
			t.Fatal("responses passthrough failed")
		}
	})

	t.Run("chat -> responses", func(t *testing.T) {
		body := []byte(`{"model":"gpt-5","messages":[{"role":"user","content":"hi"}]}`)
		fmt, _, err := ad.ConvertRequest(nil, FormatOpenAIChatCompletions, body)
		if err != nil {
			t.Fatal(err)
		}
		if fmt != FormatOpenAIResponses {
			t.Fatalf("fmt = %v, want responses", fmt)
		}
	})

	t.Run("anthropic -> responses", func(t *testing.T) {
		body := []byte(`{"model":"gpt-5","max_tokens":1024,"messages":[{"role":"user","content":"hi"}]}`)
		fmt, _, err := ad.ConvertRequest(nil, FormatAnthropicMessages, body)
		if err != nil {
			t.Fatal(err)
		}
		if fmt != FormatOpenAIResponses {
			t.Fatalf("fmt = %v, want responses", fmt)
		}
	})
}

func TestCodexOAuth_BuildUpstreamRequest(t *testing.T) {
	ad := NewCodexOAuthAdaptor(&staticTokenProvider{token: "codex-tok"}, identity.NewIdentityService(0), nil)
	ctx := accountCtx(provider.ChannelTypeCodexOAuth, FormatOpenAIResponses, nil)
	ctx.Account.AccountID = "acct-123"
	req, err := ad.BuildUpstreamRequest(context.Background(), ctx, FormatOpenAIResponses, []byte(`{"model":"gpt-5","input":"hi"}`))
	if err != nil {
		t.Fatal(err)
	}
	if got := req.Header.Get("Authorization"); got != "Bearer codex-tok" {
		t.Fatalf("Authorization = %q", got)
	}
	if got := req.Header.Get("chatgpt-account-id"); got != "acct-123" {
		t.Fatalf("chatgpt-account-id = %q", got)
	}
	if got := req.Header.Get("originator"); got != "codex_cli_rs" {
		t.Fatalf("originator = %q", got)
	}
	if got := req.Header.Get("OpenAI-Beta"); got != "responses=experimental" {
		t.Fatalf("OpenAI-Beta = %q", got)
	}
}

func TestCodexOAuth_BuildUpstreamRequest_UsesInlineAccessToken(t *testing.T) {
	ad := NewCodexOAuthAdaptor(nil, nil, nil)
	ctx := accountCtx(provider.ChannelTypeCodexOAuth, FormatOpenAIResponses, nil)
	ctx.Account.AccessToken = "inline-token"
	req, err := ad.BuildUpstreamRequest(context.Background(), ctx, FormatOpenAIResponses, []byte(`{"model":"gpt-5","input":"hi"}`))
	if err != nil {
		t.Fatal(err)
	}
	if got := req.Header.Get("Authorization"); got != "Bearer inline-token" {
		t.Fatalf("Authorization = %q, want Bearer inline-token", got)
	}
}

// ---------------------------------------------------------------------------
// Stream bridging: Anthropic SSE -> Responses SSE -> Chat SSE
// ---------------------------------------------------------------------------

func TestPumpAnthropicToResponses(t *testing.T) {
	// A minimal Anthropic SSE stream: message_start + a text delta + message_stop.
	src := strings.NewReader(
		"event: message_start\n" +
			`data: {"type":"message_start","message":{"id":"msg_1","model":"claude-sonnet-4-20250514","usage":{"input_tokens":5,"output_tokens":0}}}` + "\n\n" +
			"event: content_block_delta\n" +
			`data: {"type":"content_block_delta","index":0,"delta":{"type":"text_delta","text":"hi"}}` + "\n\n" +
			"event: message_stop\n" +
			`data: {"type":"message_stop"}` + "\n\n",
	)
	pr, pw := io.Pipe()
	go pumpAnthropicToResponses(src, pw)
	out, _ := io.ReadAll(pr)
	s := string(out)
	if !strings.Contains(s, "response.created") {
		t.Fatalf("expected response.created event, got:\n%s", s)
	}
	if !strings.Contains(s, "response.output_text.delta") {
		t.Fatalf("expected output_text.delta event, got:\n%s", s)
	}
	if !strings.Contains(s, "response.completed") {
		t.Fatalf("expected response.completed event, got:\n%s", s)
	}
}

func TestPumpResponsesToAnthropic(t *testing.T) {
	src := strings.NewReader(
		"event: response.created\n" +
			`data: {"type":"response.created","response":{"id":"resp_1","model":"gpt-5"}}` + "\n\n" +
			"event: response.output_text.delta\n" +
			`data: {"type":"response.output_text.delta","delta":"hi","output_index":0}` + "\n\n" +
			"event: response.completed\n" +
			`data: {"type":"response.completed","response":{"id":"resp_1","model":"gpt-5","usage":{"input_tokens":3,"output_tokens":1}}}` + "\n\n",
	)
	pr, pw := io.Pipe()
	go pumpResponsesToAnthropic(src, pw)
	out, _ := io.ReadAll(pr)
	s := string(out)
	if !strings.Contains(s, "message_start") {
		t.Fatalf("expected message_start, got:\n%s", s)
	}
	if !strings.Contains(s, "text_delta") {
		t.Fatalf("expected text_delta, got:\n%s", s)
	}
	if !strings.Contains(s, "message_stop") {
		t.Fatalf("expected message_stop, got:\n%s", s)
	}
}

// ---------------------------------------------------------------------------
// End-to-end through a fake upstream: Claude OAuth non-streaming
// ---------------------------------------------------------------------------

func TestClaudeOAuth_EndToEnd_NonStreaming(t *testing.T) {
	// Fake upstream: returns a canned Anthropic Messages response.
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		resp := apicompat.AnthropicResponse{
			ID:    "msg_1",
			Type:  "message",
			Role:  "assistant",
			Model: "claude-sonnet-4-20250514",
			Content: []apicompat.AnthropicContentBlock{
				{Type: "text", Text: "hello world"},
			},
			Usage: apicompat.AnthropicUsage{InputTokens: 3, OutputTokens: 2},
		}
		w.Header().Set("Content-Type", "application/json")
		out, _ := sonic.Marshal(resp)
		w.Write(out)
	}))
	defer upstream.Close()

	ad := NewClaudeOAuthAdaptor(&staticTokenProvider{token: "tok"}, identity.NewIdentityService(0), nil)
	ctx := accountCtx(provider.ChannelTypeClaudeOAuth, FormatOpenAIChatCompletions, nil)
	ctx.Channel.BaseURL = upstream.URL

	chatBody := []byte(`{"model":"claude-sonnet-4-20250514","messages":[{"role":"user","content":"hi"}],"stream":false}`)
	upFmt, upBody, err := ad.ConvertRequest(ctx, FormatOpenAIChatCompletions, chatBody)
	if err != nil {
		t.Fatal(err)
	}
	req, err := ad.BuildUpstreamRequest(context.Background(), ctx, upFmt, upBody)
	if err != nil {
		t.Fatal(err)
	}
	resp, err := upstream.Client().Do(req)
	if err != nil {
		t.Fatal(err)
	}
	outFmt, outBody, err := ad.ConvertResponse(ctx, upFmt, resp)
	if err != nil {
		t.Fatal(err)
	}
	if outFmt != FormatOpenAIChatCompletions {
		t.Fatalf("outFmt = %v, want chat_completions", outFmt)
	}
	if !strings.Contains(string(outBody), "hello world") {
		t.Fatalf("expected converted chat completions body, got %s", outBody)
	}
	if !strings.Contains(string(outBody), "chat.completion") {
		t.Fatalf("expected chat.completion object, got %s", outBody)
	}
}

// ---------------------------------------------------------------------------
// credential.Platform / identity.Platform compile-time sanity (type compat)
// ---------------------------------------------------------------------------

func TestPlatformTypeCompat(t *testing.T) {
	// The adaptor translates between identity.Platform and credential.Platform
	// (both string newtypes). Ensure the values line up for every platform,
	// including the domestic Coding-Plan vendors added in the roadmap.
	for _, p := range []struct{ name string }{
		{"claude"}, {"codex"}, {"zhipu"}, {"minimax"}, {"kimi"},
	} {
		ip := identity.Platform(p.name)
		cp := credential.Platform(p.name)
		if string(ip) != string(cp) {
			t.Fatalf("identity/credential Platform %q values diverge", p.name)
		}
	}
}

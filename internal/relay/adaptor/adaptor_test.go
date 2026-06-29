package adaptor

import (
	"context"
	"io"
	"net/http"
	"strings"
	"testing"

	relaybiz "micro-one-api/internal/relay/biz"
	"micro-one-api/internal/relay/provider"
)

// channelRef builds a RelayContext for an OpenAI-compatible channel with the
// given type/base/key.
func channelRef(chType int32, base, key string) *RelayContext {
	return &RelayContext{
		InboundFormat: FormatOpenAIChatCompletions,
		ClientModel:   "gpt-4o",
		ResolvedModel: "gpt-4o",
		Channel: &relaybiz.Channel{
			ID:      1,
			Type:    chType,
			BaseURL: base,
			Key:     key,
			Models:  []string{"gpt-4o"},
		},
	}
}

func TestRegistry_HasAllExpectedTypes(t *testing.T) {
	want := map[int32]bool{
		provider.ChannelTypeOpenAI:      true,
		provider.ChannelTypeDeepSeek:    true,
		provider.ChannelTypeAnthropic:   true,
		provider.ChannelTypeGemini:      true,
		provider.ChannelTypeAzure:       true,
		provider.ChannelTypeOpenRouter:  true,
		provider.ChannelTypeMoonshot:    true,
		provider.ChannelTypeDoubao:      true,
		provider.ChannelTypeVoyageAI:    true,
		provider.ChannelTypeSiliconFlow: true,
	}
	for ty := range want {
		if _, ok := GetAdaptor(ty); !ok {
			t.Errorf("GetAdaptor(%d): expected registered adaptor, got none", ty)
		}
	}
}

func TestRegistry_UnknownTypeReturnsFalse(t *testing.T) {
	if _, ok := GetAdaptor(99999); ok {
		t.Fatal("GetAdaptor(99999): expected not ok for unregistered type")
	}
}

func TestOpenAICompatibleAdaptor_PassthroughAndURL(t *testing.T) {
	// No provider factory wired: OpenAICompatibleAdaptor can still be built
	// directly with a nil provider for URL/ConvertRequest checks that don't
	// touch the provider.
	a := NewOpenAICompatibleAdaptor(nil, []string{"gpt-4o"})
	rc := channelRef(provider.ChannelTypeOpenAI, "https://api.openai.com/v1", "sk-test")
	a.Init(rc)

	up, body, err := a.ConvertRequest(rc, FormatOpenAIChatCompletions, []byte(`{"model":"gpt-4o"}`))
	if err != nil {
		t.Fatalf("ConvertRequest: %v", err)
	}
	if up != FormatOpenAIChatCompletions {
		t.Errorf("upstream format = %q, want %q", up, FormatOpenAIChatCompletions)
	}
	if string(body) != `{"model":"gpt-4o"}` {
		t.Errorf("body changed: %s", body)
	}

	url, err := a.GetUpstreamURL(rc)
	if err != nil {
		t.Fatalf("GetUpstreamURL: %v", err)
	}
	if want := "https://api.openai.com/v1/chat/completions"; url != want {
		t.Errorf("url = %q, want %q", url, want)
	}
}

func TestOpenAICompatibleAdaptor_BuildUpstreamRequest(t *testing.T) {
	a := NewOpenAICompatibleAdaptor(nil, nil)
	rc := channelRef(provider.ChannelTypeOpenAI, "https://api.openai.com/v1", "sk-test")
	a.Init(rc)

	req, err := a.BuildUpstreamRequest(context.Background(), rc, FormatOpenAIChatCompletions, []byte(`{}`))
	if err != nil {
		t.Fatalf("BuildUpstreamRequest: %v", err)
	}
	if got := req.Header.Get("Authorization"); got != "Bearer sk-test" {
		t.Errorf("Authorization = %q, want Bearer sk-test", got)
	}
	if got := req.Header.Get("Content-Type"); got != "application/json" {
		t.Errorf("Content-Type = %q, want application/json", got)
	}
}

func TestOpenAICompatibleAdaptor_ConvertResponsePassesThroughAndErrors(t *testing.T) {
	a := NewOpenAICompatibleAdaptor(nil, nil)
	rc := channelRef(provider.ChannelTypeOpenAI, "https://api.openai.com/v1", "sk-test")
	a.Init(rc)

	// 200 OK passes body through. Build the response manually to avoid
	// binding a listener (sandbox-safe).
	resp := &http.Response{
		StatusCode: http.StatusOK,
		Header:     http.Header{"Content-Type": []string{"application/json"}},
		Body:       io.NopCloser(strings.NewReader(`{"id":"x"}`)),
	}
	out, body, err := a.ConvertResponse(rc, FormatOpenAIChatCompletions, resp)
	if err != nil {
		t.Fatalf("ConvertResponse: %v", err)
	}
	if out != FormatOpenAIChatCompletions {
		t.Errorf("out = %q, want %q", out, FormatOpenAIChatCompletions)
	}
	if string(body) != `{"id":"x"}` {
		t.Errorf("body = %s", body)
	}

	// 500 surfaces an UpstreamHTTPError.
	respErr := &http.Response{
		StatusCode: http.StatusInternalServerError,
		Header:     http.Header{},
		Body:       io.NopCloser(strings.NewReader(`{"error":"boom"}`)),
	}
	_, _, err = a.ConvertResponse(rc, FormatOpenAIChatCompletions, respErr)
	if err == nil {
		t.Fatal("expected error for 500 response")
	}
	var ue *provider.UpstreamHTTPError
	if !asUpstreamError(err, &ue) || ue.StatusCode != http.StatusInternalServerError {
		t.Errorf("expected UpstreamHTTPError 500, got %v", err)
	}
}

func TestAnthropicAdaptor_URLAndHeaders(t *testing.T) {
	a := NewAnthropicAdaptor(nil, nil)
	rc := &RelayContext{
		InboundFormat: FormatAnthropicMessages,
		ResolvedModel: "claude-3-5-sonnet-20241022",
		Channel: &relaybiz.Channel{
			ID:      2,
			Type:    provider.ChannelTypeAnthropic,
			BaseURL: "https://api.anthropic.com",
			Key:     "ant-test",
		},
	}
	a.Init(rc)

	url, err := a.GetUpstreamURL(rc)
	if err != nil {
		t.Fatalf("GetUpstreamURL: %v", err)
	}
	if want := "https://api.anthropic.com/v1/messages"; url != want {
		t.Errorf("url = %q, want %q", url, want)
	}

	req, err := a.BuildUpstreamRequest(context.Background(), rc, FormatAnthropicMessages, []byte(`{}`))
	if err != nil {
		t.Fatalf("BuildUpstreamRequest: %v", err)
	}
	if got := req.Header.Get("x-api-key"); got != "ant-test" {
		t.Errorf("x-api-key = %q, want ant-test", got)
	}
	if got := req.Header.Get("anthropic-version"); got != "2023-06-01" {
		t.Errorf("anthropic-version = %q, want 2023-06-01", got)
	}
}

func TestGeminiAdaptor_URL(t *testing.T) {
	a := NewGeminiAdaptor(nil, nil)
	rc := &RelayContext{
		ResolvedModel: "gemini-1.5-pro",
		Channel: &relaybiz.Channel{
			ID:      3,
			Type:    provider.ChannelTypeGemini,
			BaseURL: "https://generativelanguage.googleapis.com",
			Key:     "gem-key",
		},
	}
	a.Init(rc)

	url, err := a.GetUpstreamURL(rc)
	if err != nil {
		t.Fatalf("GetUpstreamURL: %v", err)
	}
	if want := "https://generativelanguage.googleapis.com/v1beta/models/gemini-1.5-pro:generateContent?key=gem-key"; url != want {
		t.Errorf("url = %q, want %q", url, want)
	}
}

func TestAzureAdaptor_URL(t *testing.T) {
	a := NewAzureAdaptor(nil, nil, "")
	rc := &RelayContext{
		ResolvedModel: "gpt-4o",
		Channel: &relaybiz.Channel{
			ID:      4,
			Type:    provider.ChannelTypeAzure,
			BaseURL: "https://example.openai.azure.com",
			Key:     "az-key",
		},
	}
	a.Init(rc)

	url, err := a.GetUpstreamURL(rc)
	if err != nil {
		t.Fatalf("GetUpstreamURL: %v", err)
	}
	if want := "https://example.openai.azure.com/openai/deployments/gpt-4o/chat/completions?api-version=2024-02-15-preview"; url != want {
		t.Errorf("url = %q, want %q", url, want)
	}

	req, err := a.BuildUpstreamRequest(context.Background(), rc, FormatOpenAIChatCompletions, []byte(`{}`))
	if err != nil {
		t.Fatalf("BuildUpstreamRequest: %v", err)
	}
	if got := req.Header.Get("api-key"); got != "az-key" {
		t.Errorf("api-key = %q, want az-key", got)
	}
	if got := req.Header.Get("Authorization"); got != "" {
		t.Errorf("Authorization should be absent for Azure, got %q", got)
	}
}

func TestRegistry_WithProviderFactory_DispatchesAndInits(t *testing.T) {
	// Wire a real factory so lazyAdaptor can construct a provider on Init.
	SetProviderFactory(provider.NewProviderFactory(0))
	t.Cleanup(func() { globalProviderFactory = nil })
	// Provider construction validates base URLs via DNS (SSRF guard); bypass
	// for unit tests that do not exercise SSRF behavior.
	t.Setenv("PROVIDER_DISABLE_SSRF_CHECK", "true")

	a, ok := GetAdaptor(provider.ChannelTypeDeepSeek)
	if !ok {
		t.Fatal("GetAdaptor(DeepSeek): expected registered")
	}
	// Before Init, ModelList returns nil and methods surface errNotInitialized.
	if a.ModelList() != nil {
		t.Errorf("pre-Init ModelList = %v, want nil", a.ModelList())
	}
	rc := channelRef(provider.ChannelTypeDeepSeek, "https://api.deepseek.com/v1", "sk-deep")
	a.Init(rc)

	if got := a.Name(); got != "openai_compatible" {
		t.Errorf("Name = %q, want openai_compatible", got)
	}
	if ml := a.ModelList(); len(ml) == 0 || ml[0] != "gpt-4o" {
		t.Errorf("ModelList = %v, want first element gpt-4o from channel", ml)
	}
	url, err := a.GetUpstreamURL(rc)
	if err != nil {
		t.Fatalf("GetUpstreamURL: %v", err)
	}
	if want := "https://api.deepseek.com/v1/chat/completions"; url != want {
		t.Errorf("url = %q, want %q", url, want)
	}
}

func TestRegistry_WithProviderFactory_UnknownChannelTypeFallsBackToOpenAI(t *testing.T) {
	// The provider factory's default branch returns an OpenAI provider for
	// unknown types, so the adaptor should still construct. We use a channel
	// type that the factory treats as OpenAI-compatible by default.
	SetProviderFactory(provider.NewProviderFactory(0))
	t.Cleanup(func() { globalProviderFactory = nil })

	a, ok := GetAdaptor(provider.ChannelTypeOllama)
	if !ok {
		t.Fatal("GetAdaptor(Ollama): expected registered")
	}
	rc := channelRef(provider.ChannelTypeOllama, "http://localhost:11434/v1", "")
	// Ollama resolves to localhost; the SSRF guard rejects localhost unless
	// bypassed. Provide env override for the test only.
	t.Setenv("PROVIDER_DISABLE_SSRF_CHECK", "true")
	a.Init(rc)
	if got := a.Name(); got != "openai_compatible" {
		t.Errorf("Name = %q, want openai_compatible", got)
	}
}

// asUpstreamError is a small errors.As helper to avoid importing errors just
// for one call in the test file.
func asUpstreamError(err error, target **provider.UpstreamHTTPError) bool {
	for err != nil {
		if ue, ok := err.(*provider.UpstreamHTTPError); ok {
			*target = ue
			return true
		}
		type unwrapper interface{ Unwrap() error }
		u, ok := err.(unwrapper)
		if !ok {
			return false
		}
		err = u.Unwrap()
	}
	return false
}

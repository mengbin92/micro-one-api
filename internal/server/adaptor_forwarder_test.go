package server

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	relaycredential "micro-one-api/domain/upstream/credential"
	relayprovider "micro-one-api/domain/upstream/provider"
	relaybiz "micro-one-api/internal/biz"
)

func TestRelayAdaptorForwarderConvertsResponsesForAnthropicAPIKeyChannel(t *testing.T) {
	t.Setenv("PROVIDER_DISABLE_SSRF_CHECK", "true")
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/messages" {
			t.Errorf("upstream path = %q", r.URL.Path)
		}
		if got := r.Header.Get("x-api-key"); got != "step-key" {
			t.Errorf("x-api-key = %q", got)
		}
		body, err := io.ReadAll(r.Body)
		if err != nil {
			t.Errorf("read upstream body: %v", err)
		}
		requestBody := string(body)
		if !strings.Contains(requestBody, `"model":"step-explore"`) || !strings.Contains(requestBody, `"messages"`) {
			t.Errorf("converted upstream body = %s", requestBody)
		}
		if strings.Contains(requestBody, `"thinking"`) || strings.Contains(requestBody, `"output_config"`) {
			t.Errorf("third-party-incompatible extensions remain: %s", requestBody)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"id":"msg_step_1","type":"message","role":"assistant","model":"step-explore","content":[{"type":"text","text":"done"}],"stop_reason":"end_turn","usage":{"input_tokens":5,"output_tokens":2}}`))
	}))
	defer upstream.Close()

	factory := relayprovider.NewProviderFactory(time.Second)
	_ = NewHTTPServer(nil, nil, nil, factory, nil)
	forwarder := newRelayAdaptorForwarder(factory, nil, &http.Client{Timeout: time.Second}, nil, nil)
	response, err := forwarder.Forward(context.Background(), &relaybiz.RelayPlan{
		Channel:       &relaybiz.Channel{ID: 9, Type: relayprovider.ChannelTypeAnthropic, BaseURL: upstream.URL, Key: "step-key"},
		ResolvedModel: "step-explore",
	}, relaybiz.ExecutorRequest{
		Model:    "step-explore",
		Endpoint: "responses",
		Body:     []byte(`{"model":"step-explore","input":"inspect the repository","reasoning":{"effort":"high"}}`),
	})
	if err != nil {
		t.Fatalf("Forward() error = %v", err)
	}
	if response == nil || response.StatusCode != http.StatusOK {
		t.Fatalf("response = %#v", response)
	}
	if body := string(response.Body); !strings.Contains(body, `"object":"response"`) || !strings.Contains(body, `"text":"done"`) {
		t.Fatalf("responses body = %s", body)
	}
	if response.Usage == nil || response.Usage.TotalTokens != 7 {
		t.Fatalf("usage = %#v", response.Usage)
	}
}

func TestRelayAdaptorForwarderUsesRegistryForAPIKeyChannel(t *testing.T) {
	t.Setenv("PROVIDER_DISABLE_SSRF_CHECK", "true")
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if got := r.Header.Get("Authorization"); got != "Bearer upstream-key" {
			t.Errorf("upstream authorization = %q", got)
		}
		if got := r.Header.Get("X-Request-ID"); got != "request-adaptor" {
			t.Errorf("upstream request id = %q", got)
		}
		if got := r.Header.Get("Cookie"); got != "" {
			t.Errorf("upstream cookie = %q, want stripped", got)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"choices":[],"usage":{"prompt_tokens":2,"completion_tokens":3,"total_tokens":5}}`))
	}))
	defer upstream.Close()

	factory := relayprovider.NewProviderFactory(time.Second)
	// NewHTTPServer is the production bootstrap point that wires the registry.
	_ = NewHTTPServer(nil, nil, nil, factory, nil)
	forwarder := newRelayAdaptorForwarder(factory, nil, &http.Client{Timeout: time.Second}, nil, nil)
	response, err := forwarder.Forward(context.Background(), &relaybiz.RelayPlan{
		Auth:          &relaybiz.AuthSnapshot{UserID: 42},
		Channel:       &relaybiz.Channel{ID: 9, Type: relayprovider.ChannelTypeOpenAI, BaseURL: upstream.URL + "/v1", Key: "upstream-key"},
		ResolvedModel: "gpt-4o-mini",
	}, relaybiz.ExecutorRequest{
		Model:     "gpt-4o-mini",
		Endpoint:  "chat/completions",
		RequestID: "request-adaptor",
		Body:      []byte(`{"model":"gpt-4o-mini","messages":[{"role":"user","content":"hi"}]}`),
		Headers: map[string][]string{
			"Cookie":       {"session=secret"},
			"X-Request-ID": {"request-adaptor"},
		},
	})
	if err != nil {
		t.Fatalf("Forward() error = %v", err)
	}
	if response == nil || response.StatusCode != http.StatusOK || !strings.Contains(string(response.Body), `"total_tokens":5`) {
		t.Fatalf("response = %#v", response)
	}
	if response.Usage == nil || response.Usage.TotalTokens != 5 {
		t.Fatalf("usage = %#v", response.Usage)
	}
}

func TestRelayAdaptorForwarderCapsUpstreamErrorBody(t *testing.T) {
	t.Setenv("PROVIDER_DISABLE_SSRF_CHECK", "true")
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusBadGateway)
		_, _ = w.Write([]byte(strings.Repeat("x", relayprovider.MaxUpstreamErrorBody+1024)))
	}))
	defer upstream.Close()

	factory := relayprovider.NewProviderFactory(time.Second)
	_ = NewHTTPServer(nil, nil, nil, factory, nil)
	forwarder := newRelayAdaptorForwarder(factory, nil, &http.Client{Timeout: time.Second}, nil, nil)
	_, err := forwarder.Forward(context.Background(), &relaybiz.RelayPlan{
		Channel: &relaybiz.Channel{ID: 9, Type: relayprovider.ChannelTypeOpenAI, BaseURL: upstream.URL + "/v1", Key: "upstream-key"},
	}, relaybiz.ExecutorRequest{
		Model: "gpt-4o-mini",
		Body:  []byte(`{"model":"gpt-4o-mini","messages":[{"role":"user","content":"hi"}]}`),
	})
	var upstreamErr *relayprovider.UpstreamHTTPError
	if !errors.As(err, &upstreamErr) {
		t.Fatalf("Forward() error = %v, want UpstreamHTTPError", err)
	}
	if upstreamErr.StatusCode != http.StatusBadGateway || len(upstreamErr.Body) != relayprovider.MaxUpstreamErrorBody {
		t.Fatalf("upstream error = status:%d body:%d", upstreamErr.StatusCode, len(upstreamErr.Body))
	}
}

func TestClassifyExecutorCapabilityErrorScopesMethodNotAllowedToResponses(t *testing.T) {
	upstreamErr := &relayprovider.UpstreamHTTPError{StatusCode: http.StatusMethodNotAllowed, Body: []byte("method not allowed")}

	responsesErr := classifyExecutorCapabilityError(string(EndpointResponses), upstreamErr)
	if !relaybiz.IsProtocolCapabilityMismatch(responsesErr) {
		t.Fatalf("responses error = %v, want protocol capability mismatch", responsesErr)
	}
	chatErr := classifyExecutorCapabilityError(string(EndpointChatCompletions), upstreamErr)
	if relaybiz.IsProtocolCapabilityMismatch(chatErr) {
		t.Fatalf("chat error = %v, generic 405 must not be a capability mismatch", chatErr)
	}
}

func TestRelayAdaptorForwarderResolvesSubscriptionCredential(t *testing.T) {
	resolver := relaycredential.NewNoopAccountResolver()
	resolver.SeedByChannel(17, &relaycredential.SubscriptionAccountMetadata{
		ID:          71,
		Platform:    relaycredential.PlatformCodex,
		AccountType: "oauth",
		AccessToken: "resolved-token",
		AccountID:   "account-71",
	})
	forwarder := relayAdaptorForwarder{accountResolver: resolver}
	rc, client, err := forwarder.relayContext(context.Background(), &relaybiz.RelayPlan{
		Auth:    &relaybiz.AuthSnapshot{UserID: 42},
		Channel: &relaybiz.Channel{ID: 17, Type: relayprovider.ChannelTypeCodexOAuth, SubscriptionAccountID: 71},
	}, relaybiz.ExecutorRequest{Model: "gpt-5-codex", RequestID: "request-subscription"})
	if err != nil {
		t.Fatalf("relayContext() error = %v", err)
	}
	if client != nil {
		t.Fatalf("subscription client = %#v, want nil default client", client)
	}
	if rc.Account == nil || rc.Account.ID != 71 || rc.Account.AccessToken != "resolved-token" || rc.Account.AccountID != "account-71" {
		t.Fatalf("resolved account = %#v", rc.Account)
	}
}

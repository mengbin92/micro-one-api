package server

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	relaybiz "micro-one-api/internal/relay/biz"
	relayprovider "micro-one-api/internal/relay/provider"
)

type orchestratorIdentityClient struct{}

func (orchestratorIdentityClient) GetAuthSnapshot(_ context.Context, token string) (*relaybiz.AuthSnapshot, error) {
	return &relaybiz.AuthSnapshot{
		UserID:        1,
		TokenID:       2,
		TokenName:     "test-token",
		Group:         "default",
		AllowedModels: []string{"gpt-4o-mini"},
		UserEnabled:   true,
		TokenEnabled:  true,
	}, nil
}

type orchestratorChannelClient struct {
	baseURL string
}

func (c orchestratorChannelClient) SelectChannel(_ context.Context, _, _ string, _ bool) (*relaybiz.Channel, error) {
	return &relaybiz.Channel{
		ID:      11,
		Type:    relayprovider.ChannelTypeOpenAI,
		BaseURL: c.baseURL,
		Key:     "sk-upstream",
	}, nil
}

func (c orchestratorChannelClient) RecordChannelHealth(_ context.Context, _ int64, _ bool, _ string, _ int64) error {
	return nil
}

func TestRelayOrchestratorForwardsNonStreamResponse(t *testing.T) {
	t.Setenv("PROVIDER_DISABLE_SSRF_CHECK", "true")

	var upstreamBody string
	var upstreamAuth string
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/chat/completions" {
			t.Fatalf("upstream path = %q", r.URL.Path)
		}
		upstreamAuth = r.Header.Get("Authorization")
		body, err := io.ReadAll(r.Body)
		if err != nil {
			t.Fatalf("read upstream body: %v", err)
		}
		upstreamBody = string(body)
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusAccepted)
		_, _ = w.Write([]byte(`{"usage":{"prompt_tokens":3,"completion_tokens":4,"total_tokens":7},"choices":[]}`))
	}))
	defer upstream.Close()

	uc := relaybiz.NewRelayUsecase(
		orchestratorIdentityClient{},
		orchestratorChannelClient{baseURL: upstream.URL + "/v1"},
		nil,
		nil,
	)
	orchestrator := NewRelayOrchestratorWithProviderFactory(uc, relayprovider.NewProviderFactory(time.Second), nil)

	result, err := orchestrator.Execute(context.Background(), &RelayRequest{
		Token:    "client-token",
		Model:    "gpt-4o-mini",
		Endpoint: EndpointChatCompletions,
		Body:     strings.NewReader(`{"model":"gpt-4o-mini","messages":[{"role":"user","content":"hi"}]}`),
		Headers:  http.Header{"Authorization": []string{"Bearer client-token"}},
	})
	if err != nil {
		t.Fatalf("Execute() error = %v", err)
	}
	if result.StatusCode != http.StatusAccepted {
		t.Fatalf("status = %d, want %d", result.StatusCode, http.StatusAccepted)
	}
	if result.Response == nil {
		t.Fatal("response body is nil")
	}
	body, err := io.ReadAll(result.Response)
	if err != nil {
		t.Fatalf("read result body: %v", err)
	}
	if !strings.Contains(string(body), `"total_tokens":7`) {
		t.Fatalf("result body = %s", string(body))
	}
	if result.Usage == nil || result.Usage.TotalTokens != 7 {
		t.Fatalf("usage = %#v, want total 7", result.Usage)
	}
	if upstreamAuth != "Bearer sk-upstream" {
		t.Fatalf("upstream auth = %q", upstreamAuth)
	}
	if !strings.Contains(upstreamBody, `"model":"gpt-4o-mini"`) {
		t.Fatalf("upstream body = %s", upstreamBody)
	}
}

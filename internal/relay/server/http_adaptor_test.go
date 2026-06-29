package server

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	relaybiz "micro-one-api/internal/relay/biz"
	relaycredential "micro-one-api/internal/relay/credential"
	relayprovider "micro-one-api/internal/relay/provider"
)

type testSubscriptionResolver struct {
	meta *relaycredential.SubscriptionAccountMetadata
}

func (r testSubscriptionResolver) Resolve(context.Context, int64) (*relaycredential.SubscriptionAccountMetadata, error) {
	if r.meta == nil {
		return nil, relaycredential.ErrAccountNotFound
	}
	cp := *r.meta
	return &cp, nil
}

func TestHandleChatCompletionsViaAdaptor_UsesFallbackMetadata(t *testing.T) {
	httpServer := NewHTTPServer(nil, nil, nil, nil, nil)
	httpServer.SetHybridAdaptorEnabled(true)
	var seenAuth, seenAccountID string
	httpServer.SetOAuthHTTPClient(&http.Client{
		Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
			seenAuth = req.Header.Get("Authorization")
			seenAccountID = req.Header.Get("chatgpt-account-id")
			return newJSONResponse(`{"id":"resp_1","object":"response","model":"gpt-5","status":"completed","output":[{"type":"message","id":"msg_1","role":"assistant","content":[{"type":"output_text","text":"ok"}]}]}`), nil
		}),
	})
	httpServer.SetSubscriptionAccountResolver(testSubscriptionResolver{meta: nil})

	plan := &relaybiz.RelayPlan{
		Auth: &relaybiz.AuthSnapshot{UserID: 42, Group: "default"},
		Channel: &relaybiz.Channel{
			ID:      11,
			Type:    relayprovider.ChannelTypeCodexOAuth,
			BaseURL: "https://example.invalid",
			Key:     "acct-123",
			Group:   "default",
		},
		ResolvedModel: "gpt-5",
	}
	req := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", strings.NewReader(`{"model":"gpt-5","messages":[{"role":"user","content":"hi"}],"stream":false}`))
	req.Header.Set("Authorization", "Bearer test-token")
	rec := httptest.NewRecorder()

	httpServer.handleChatCompletionsViaAdaptor(rec, req, plan, "gpt-5", []byte(`{"model":"gpt-5","messages":[{"role":"user","content":"hi"}],"stream":false}`))

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, body=%s", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), `"ok"`) {
		t.Fatalf("unexpected body: %s", rec.Body.String())
	}
	if seenAuth != "Bearer acct-123" {
		t.Fatalf("Authorization = %q", seenAuth)
	}
	if seenAccountID != "11" {
		t.Fatalf("chatgpt-account-id = %q", seenAccountID)
	}
}

func TestHandleChatCompletionsViaAdaptor_PlanAccountWinsOverResolver(t *testing.T) {
	httpServer := NewHTTPServer(nil, nil, nil, nil, nil)
	httpServer.SetHybridAdaptorEnabled(true)
	var seenAuth, seenAccountID string
	httpServer.SetOAuthHTTPClient(&http.Client{
		Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
			seenAuth = req.Header.Get("Authorization")
			seenAccountID = req.Header.Get("chatgpt-account-id")
			return newJSONResponse(`{"id":"resp_1","object":"response","model":"gpt-5","status":"completed","output":[{"type":"message","id":"msg_1","role":"assistant","content":[{"type":"output_text","text":"ok"}]}]}`), nil
		}),
	})
	httpServer.SetSubscriptionAccountResolver(testSubscriptionResolver{meta: &relaycredential.SubscriptionAccountMetadata{
		ID:          99,
		Platform:    relaycredential.PlatformCodex,
		AccountType: "oauth",
		AccessToken: "resolver-token",
		AccountID:   "resolver-account",
	}})

	plan := &relaybiz.RelayPlan{
		Auth: &relaybiz.AuthSnapshot{UserID: 42, Group: "default"},
		Channel: &relaybiz.Channel{
			ID:      12,
			Type:    relayprovider.ChannelTypeCodexOAuth,
			BaseURL: "https://example.invalid",
			Group:   "default",
		},
		Account: &relaybiz.SubscriptionAccount{
			ID:          12,
			Platform:    "codex",
			AccountType: "oauth",
			AccessToken: "plan-token",
			AccountID:   "plan-account",
			Group:       "default",
		},
		ResolvedModel: "gpt-5",
	}
	req := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", strings.NewReader(`{"model":"gpt-5","messages":[{"role":"user","content":"hi"}],"stream":false}`))
	req.Header.Set("Authorization", "Bearer test-token")
	rec := httptest.NewRecorder()

	httpServer.handleChatCompletionsViaAdaptor(rec, req, plan, "gpt-5", []byte(`{"model":"gpt-5","messages":[{"role":"user","content":"hi"}],"stream":false}`))

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, body=%s", rec.Code, rec.Body.String())
	}
	if seenAuth != "Bearer plan-token" {
		t.Fatalf("Authorization = %q", seenAuth)
	}
	if seenAccountID != "plan-account" {
		t.Fatalf("chatgpt-account-id = %q", seenAccountID)
	}
}

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(req *http.Request) (*http.Response, error) { return f(req) }

func newJSONResponse(body string) *http.Response {
	return &http.Response{
		StatusCode: http.StatusOK,
		Header:     http.Header{"Content-Type": []string{"application/json"}},
		Body:       io.NopCloser(strings.NewReader(body)),
	}
}

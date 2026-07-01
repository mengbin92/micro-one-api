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

	"github.com/prometheus/client_golang/prometheus/testutil"

	"micro-one-api/internal/pkg/metrics"
	relaybiz "micro-one-api/internal/relay/biz"
	relaycredential "micro-one-api/internal/relay/credential"
	relayprovider "micro-one-api/internal/relay/provider"
	relayquota "micro-one-api/internal/relay/quota"
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

func TestHandleChatCompletionsViaAdaptor_FailoverOnRetryableUpstreamStatus(t *testing.T) {
	selector := &adaptorFailoverChannelClient{
		accounts: []*relaybiz.SubscriptionAccount{
			{
				ID:          13,
				Name:        "second",
				Platform:    "codex",
				AccountType: "oauth",
				Status:      1,
				BaseURL:     "https://example.invalid",
				Group:       "default",
				Models:      []string{"gpt-5"},
				Priority:    10,
				AccessToken: "second-token",
				AccountID:   "second-account",
			},
		},
	}
	relayUsecase := relaybiz.NewRelayUsecase(adaptorFailoverIdentity{}, selector, nil, nil)
	httpServer := NewHTTPServer(nil, nil, nil, nil, relayUsecase)
	httpServer.SetHybridAdaptorEnabled(true)
	httpServer.wsPoolCfg.failoverMaxSwitches = 1

	var authHeaders []string
	httpServer.SetOAuthHTTPClient(&http.Client{
		Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
			authHeaders = append(authHeaders, req.Header.Get("Authorization"))
			if len(authHeaders) == 1 {
				return newStatusResponse(http.StatusInternalServerError, `{"error":"temporary"}`), nil
			}
			return newJSONResponse(`{"id":"resp_2","object":"response","model":"gpt-5","status":"completed","output":[{"type":"message","id":"msg_1","role":"assistant","content":[{"type":"output_text","text":"ok"}]}]}`), nil
		}),
	})

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
			Status:      1,
			BaseURL:     "https://example.invalid",
			Group:       "default",
			Models:      []string{"gpt-5"},
			Priority:    20,
			AccessToken: "first-token",
			AccountID:   "first-account",
		},
		ResolvedModel: "gpt-5",
	}
	req := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", strings.NewReader(`{"model":"gpt-5","messages":[{"role":"user","content":"hi"}],"stream":false}`))
	req.Header.Set("Authorization", "Bearer test-token")
	rec := httptest.NewRecorder()
	failoverBefore := testutil.ToFloat64(metrics.RelaySubscriptionFailoverTotal.WithLabelValues("5xx", "switched"))
	blockBefore := testutil.ToFloat64(metrics.RelayRuntimeBlocksTotal.WithLabelValues("5xx"))

	httpServer.handleChatCompletionsViaAdaptor(rec, req, plan, "gpt-5", []byte(`{"model":"gpt-5","messages":[{"role":"user","content":"hi"}],"stream":false}`))

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, body=%s", rec.Code, rec.Body.String())
	}
	if len(authHeaders) != 2 {
		t.Fatalf("upstream calls = %d, want 2", len(authHeaders))
	}
	if authHeaders[0] != "Bearer first-token" || authHeaders[1] != "Bearer second-token" {
		t.Fatalf("Authorization headers = %v", authHeaders)
	}
	if metrics := httpServer.runtimeBlocker.Metrics(); metrics.Blocks != 1 {
		t.Fatalf("runtime blocks = %d, want 1", metrics.Blocks)
	}
	if selector.calls != 1 {
		t.Fatalf("selector calls = %d, want 1", selector.calls)
	}
	if delta := testutil.ToFloat64(metrics.RelaySubscriptionFailoverTotal.WithLabelValues("5xx", "switched")) - failoverBefore; delta != 1 {
		t.Fatalf("subscription failover metric delta = %v, want 1", delta)
	}
	if delta := testutil.ToFloat64(metrics.RelayRuntimeBlocksTotal.WithLabelValues("5xx")) - blockBefore; delta != 1 {
		t.Fatalf("runtime block metric delta = %v, want 1", delta)
	}
}

// TestHandleChatCompletionsViaAdaptor_Passthrough429 covers the exhausted case:
// with no relay usecase there is no sibling account to fail over to, so the
// original upstream 429 (with Retry-After) is passed through to the client.
func TestHandleChatCompletionsViaAdaptor_Passthrough429(t *testing.T) {
	httpServer := NewHTTPServer(nil, nil, nil, nil, nil)
	httpServer.SetHybridAdaptorEnabled(true)
	httpServer.SetOAuthHTTPClient(&http.Client{
		Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
			resp := newStatusResponse(http.StatusTooManyRequests, `{"error":{"message":"rate limited","type":"rate_limit"}}`)
			resp.Header.Set("Retry-After", "30")
			return resp, nil
		}),
	})

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
			Group:       "default",
			AccessToken: "first-token",
			AccountID:   "first-account",
		},
		ResolvedModel: "gpt-5",
	}
	req := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", strings.NewReader(`{"model":"gpt-5","messages":[{"role":"user","content":"hi"}],"stream":false}`))
	req.Header.Set("Authorization", "Bearer test-token")
	rec := httptest.NewRecorder()
	passthroughBefore := testutil.ToFloat64(metrics.RelayUpstreamPassthroughTotal.WithLabelValues("RetryablePassthrough", "429"))

	httpServer.handleChatCompletionsViaAdaptor(rec, req, plan, "gpt-5", []byte(`{"model":"gpt-5","messages":[{"role":"user","content":"hi"}],"stream":false}`))

	if rec.Code != http.StatusTooManyRequests {
		t.Fatalf("status = %d, body=%s", rec.Code, rec.Body.String())
	}
	if rec.Header().Get("Retry-After") != "30" {
		t.Fatalf("Retry-After = %q", rec.Header().Get("Retry-After"))
	}
	if !strings.Contains(rec.Body.String(), "rate limited") {
		t.Fatalf("body was not passed through: %s", rec.Body.String())
	}
	if delta := testutil.ToFloat64(metrics.RelayUpstreamPassthroughTotal.WithLabelValues("RetryablePassthrough", "429")) - passthroughBefore; delta != 1 {
		t.Fatalf("upstream passthrough metric delta = %v, want 1", delta)
	}
}

func TestRuntimeBlockDuration_DefaultsAndOverrides(t *testing.T) {
	// Defaults when nothing is configured.
	def := &HTTPServer{}
	if got := def.runtimeBlockDuration(http.StatusTooManyRequests); got != 5*time.Second {
		t.Fatalf("429 default = %v, want 5s", got)
	}
	if got := def.runtimeBlockDuration(http.StatusUnauthorized); got != 2*time.Minute {
		t.Fatalf("401 default = %v, want 2m", got)
	}
	if got := def.runtimeBlockDuration(http.StatusBadGateway); got != 2*time.Minute {
		t.Fatalf("5xx default = %v, want 2m", got)
	}
	if got := def.runtimeBlockDuration(http.StatusBadRequest); got != 0 {
		t.Fatalf("400 default = %v, want 0", got)
	}

	// Overrides via the setter take effect.
	s := &HTTPServer{}
	s.SetRuntimeBlockDurations(30*time.Second, 10*time.Minute, time.Minute)
	if got := s.runtimeBlockDuration(http.StatusTooManyRequests); got != 30*time.Second {
		t.Fatalf("429 override = %v, want 30s", got)
	}
	if got := s.runtimeBlockDuration(http.StatusUnauthorized); got != 10*time.Minute {
		t.Fatalf("401 override = %v, want 10m", got)
	}
	if got := s.runtimeBlockDuration(http.StatusServiceUnavailable); got != time.Minute {
		t.Fatalf("5xx override = %v, want 1m", got)
	}

	// A non-positive override falls back to the default for that class.
	partial := &HTTPServer{}
	partial.SetRuntimeBlockDurations(0, 0, 90*time.Second)
	if got := partial.runtimeBlockDuration(http.StatusTooManyRequests); got != 5*time.Second {
		t.Fatalf("429 partial = %v, want default 5s", got)
	}
	if got := partial.runtimeBlockDuration(http.StatusBadGateway); got != 90*time.Second {
		t.Fatalf("5xx partial = %v, want 90s", got)
	}
}

// TestHandleChatCompletionsViaAdaptor_FailoverOn429 verifies that a 429 from the
// first subscription account now triggers a cross-account failover (rather than
// being passed straight through): the second account serves the request and the
// client sees a 200.
func TestHandleChatCompletionsViaAdaptor_FailoverOn429(t *testing.T) {
	selector := &adaptorFailoverChannelClient{
		accounts: []*relaybiz.SubscriptionAccount{
			{
				ID:          13,
				Name:        "second",
				Platform:    "codex",
				AccountType: "oauth",
				Status:      1,
				BaseURL:     "https://example.invalid",
				Group:       "default",
				Models:      []string{"gpt-5"},
				Priority:    10,
				AccessToken: "second-token",
				AccountID:   "second-account",
			},
		},
	}
	relayUsecase := relaybiz.NewRelayUsecase(adaptorFailoverIdentity{}, selector, nil, nil)
	httpServer := NewHTTPServer(nil, nil, nil, nil, relayUsecase)
	httpServer.SetHybridAdaptorEnabled(true)
	httpServer.wsPoolCfg.failoverMaxSwitches = 1

	var authHeaders []string
	httpServer.SetOAuthHTTPClient(&http.Client{
		Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
			authHeaders = append(authHeaders, req.Header.Get("Authorization"))
			if len(authHeaders) == 1 {
				resp := newStatusResponse(http.StatusTooManyRequests, `{"error":{"message":"rate limited","type":"rate_limit"}}`)
				resp.Header.Set("Retry-After", "30")
				return resp, nil
			}
			return newJSONResponse(`{"id":"resp_2","object":"response","model":"gpt-5","status":"completed","output":[{"type":"message","id":"msg_1","role":"assistant","content":[{"type":"output_text","text":"ok"}]}]}`), nil
		}),
	})

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
			Status:      1,
			BaseURL:     "https://example.invalid",
			Group:       "default",
			Models:      []string{"gpt-5"},
			Priority:    20,
			AccessToken: "first-token",
			AccountID:   "first-account",
		},
		ResolvedModel: "gpt-5",
	}
	req := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", strings.NewReader(`{"model":"gpt-5","messages":[{"role":"user","content":"hi"}],"stream":false}`))
	req.Header.Set("Authorization", "Bearer test-token")
	rec := httptest.NewRecorder()
	failoverBefore := testutil.ToFloat64(metrics.RelaySubscriptionFailoverTotal.WithLabelValues("429", "switched"))
	blockBefore := testutil.ToFloat64(metrics.RelayRuntimeBlocksTotal.WithLabelValues("429"))

	httpServer.handleChatCompletionsViaAdaptor(rec, req, plan, "gpt-5", []byte(`{"model":"gpt-5","messages":[{"role":"user","content":"hi"}],"stream":false}`))

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, body=%s", rec.Code, rec.Body.String())
	}
	if len(authHeaders) != 2 {
		t.Fatalf("upstream calls = %d, want 2", len(authHeaders))
	}
	if authHeaders[0] != "Bearer first-token" || authHeaders[1] != "Bearer second-token" {
		t.Fatalf("Authorization headers = %v", authHeaders)
	}
	if delta := testutil.ToFloat64(metrics.RelaySubscriptionFailoverTotal.WithLabelValues("429", "switched")) - failoverBefore; delta != 1 {
		t.Fatalf("subscription failover metric delta = %v, want 1", delta)
	}
	if delta := testutil.ToFloat64(metrics.RelayRuntimeBlocksTotal.WithLabelValues("429")) - blockBefore; delta != 1 {
		t.Fatalf("runtime block metric delta = %v, want 1", delta)
	}
}

func TestHandleChatCompletionsViaAdaptor_RecordsCodexQuotaSnapshot(t *testing.T) {
	httpServer := NewHTTPServer(nil, nil, nil, nil, nil)
	httpServer.SetHybridAdaptorEnabled(true)
	recorder := &testQuotaRecorder{}
	httpServer.SetSubscriptionAccountQuotaRecorder(recorder)
	httpServer.SetOAuthHTTPClient(&http.Client{
		Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
			return newStatusResponse(http.StatusTooManyRequests, `{
				"error":{"message":"quota exhausted"},
				"quota":{"primary":{"used_percent":96,"reset_after_seconds":120,"window_minutes":300}}
			}`), nil
		}),
	})

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
			Group:       "default",
			AccessToken: "first-token",
			AccountID:   "first-account",
		},
		ResolvedModel: "gpt-5",
	}
	req := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", strings.NewReader(`{"model":"gpt-5","messages":[{"role":"user","content":"hi"}],"stream":false}`))
	req.Header.Set("Authorization", "Bearer test-token")
	rec := httptest.NewRecorder()
	recordedBefore := testutil.ToFloat64(metrics.RelayCodexQuotaSnapshotsTotal.WithLabelValues("recorded"))
	pausedBefore := testutil.ToFloat64(metrics.RelayCodexQuotaSnapshotsTotal.WithLabelValues("auto_paused"))

	httpServer.handleChatCompletionsViaAdaptor(rec, req, plan, "gpt-5", []byte(`{"model":"gpt-5","messages":[{"role":"user","content":"hi"}],"stream":false}`))

	if recorder.recordedAccountID != 12 {
		t.Fatalf("recorded account = %d, want 12", recorder.recordedAccountID)
	}
	if recorder.snapshot == nil || recorder.snapshot.PrimaryUsedPercent == nil || *recorder.snapshot.PrimaryUsedPercent != 96 {
		t.Fatalf("unexpected snapshot: %+v", recorder.snapshot)
	}
	if recorder.pausedAccountID != 12 {
		t.Fatalf("paused account = %d, want 12", recorder.pausedAccountID)
	}
	if delta := testutil.ToFloat64(metrics.RelayCodexQuotaSnapshotsTotal.WithLabelValues("recorded")) - recordedBefore; delta != 1 {
		t.Fatalf("codex quota recorded metric delta = %v, want 1", delta)
	}
	if delta := testutil.ToFloat64(metrics.RelayCodexQuotaSnapshotsTotal.WithLabelValues("auto_paused")) - pausedBefore; delta != 1 {
		t.Fatalf("codex quota auto paused metric delta = %v, want 1", delta)
	}
}

type adaptorFailoverIdentity struct{}

func (adaptorFailoverIdentity) GetAuthSnapshot(context.Context, string) (*relaybiz.AuthSnapshot, error) {
	return &relaybiz.AuthSnapshot{UserID: 42, Group: "default"}, nil
}

type adaptorFailoverChannelClient struct {
	accounts []*relaybiz.SubscriptionAccount
	calls    int
}

func (c *adaptorFailoverChannelClient) SelectChannel(context.Context, string, string, bool) (*relaybiz.Channel, error) {
	return nil, errors.New("no api-key channel")
}

func (c *adaptorFailoverChannelClient) RecordChannelHealth(context.Context, int64, bool, string, int64) error {
	return nil
}

func (c *adaptorFailoverChannelClient) SelectSubscriptionAccount(_ context.Context, _, _, _ string, _ bool) (*relaybiz.SubscriptionAccount, error) {
	if c.calls >= len(c.accounts) {
		return nil, errors.New("no subscription account")
	}
	account := c.accounts[c.calls]
	c.calls++
	return account, nil
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

func newStatusResponse(status int, body string) *http.Response {
	return &http.Response{
		StatusCode: status,
		Header:     http.Header{"Content-Type": []string{"application/json"}},
		Body:       io.NopCloser(strings.NewReader(body)),
	}
}

type testQuotaRecorder struct {
	recordedAccountID int64
	pausedAccountID   int64
	snapshot          *relayquota.CodexSnapshot
}

func (r *testQuotaRecorder) RecordAccountQuotaSnapshot(ctx context.Context, accountID int64, snapshot *relayquota.CodexSnapshot) error {
	r.recordedAccountID = accountID
	r.snapshot = snapshot
	return nil
}

func (r *testQuotaRecorder) AutoPauseAccount(ctx context.Context, accountID int64, reason string) error {
	r.pausedAccountID = accountID
	return nil
}

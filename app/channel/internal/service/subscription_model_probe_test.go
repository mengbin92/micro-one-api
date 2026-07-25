package service

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	"micro-one-api/app/channel/internal/biz"
	"micro-one-api/platform/events"
)

type probeLookupStub struct {
	mu      sync.Mutex
	account *biz.SubscriptionAccount
	updated *biz.SubscriptionAccount
}

func (p *probeLookupStub) FindSubscriptionAccountByID(ctx context.Context, accountID int64) (*biz.SubscriptionAccount, error) {
	if p.account == nil || p.account.ID != accountID {
		return nil, biz.ErrSubscriptionAccountNotFound
	}
	cloned := *p.account
	cloned.Models = append([]string(nil), p.account.Models...)
	return &cloned, nil
}

func (p *probeLookupStub) UpdateSubscriptionAccount(ctx context.Context, account *biz.SubscriptionAccount) error {
	cloned := *account
	cloned.Models = append([]string(nil), account.Models...)
	p.mu.Lock()
	p.updated = &cloned
	p.mu.Unlock()
	return nil
}

// Updated returns the last account written by UpdateSubscriptionAccount,
// safe to call from the test goroutine while the async event handler runs.
func (p *probeLookupStub) Updated() *biz.SubscriptionAccount {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.updated
}

func TestSubscriptionAccountIDFromEventPayload(t *testing.T) {
	if got := subscriptionAccountIDFromEventPayload(&biz.SubscriptionAccount{ID: 9}); got != 9 {
		t.Fatalf("pointer payload = %d, want 9", got)
	}
	if got := subscriptionAccountIDFromEventPayload(map[string]any{"id": float64(7)}); got != 7 {
		t.Fatalf("map payload = %d, want 7", got)
	}
	if got := subscriptionAccountIDFromEventPayload(`{"Payload":{"id":5}}`); got != 0 {
		t.Fatalf("string wrapper should not decode as account directly, got %d", got)
	}
}

func TestCodexModelProbeServiceSyncExistingCodexAccounts(t *testing.T) {
	var requests []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests = append(requests, r.Header.Get("Authorization"))
		var body map[string]any
		_ = json.NewDecoder(r.Body).Decode(&body)
		model, _ := body["model"].(string)
		switch model {
		case "gpt-5", "o4-mini":
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte(`{"output":[{"type":"function_call"}]}`))
		default:
			w.WriteHeader(http.StatusBadRequest)
			_, _ = w.Write([]byte(`{"error":"model not supported"}`))
		}
	}))
	defer srv.Close()

	lookup := &probeLookupStub{
		account: &biz.SubscriptionAccount{
			ID:          2,
			Platform:    "codex",
			AccessToken: "token",
			AccountID:   "acc-1",
			Models:      []string{"gpt-5", "gpt-5", "o4-mini"},
		},
	}
	probe := newCodexModelProbeService(lookup)
	probe.client = srv.Client()
	restore := setCodexUpstreamURLForTest(srv.URL)
	defer restore()

	if err := probe.syncCodexModels(context.Background(), 2); err != nil {
		t.Fatalf("syncCodexModels() error = %v", err)
	}
	if lookup.updated == nil {
		t.Fatal("expected account update")
	}
	if got := lookup.updated.ModelsCSV(); got != "gpt-5,o4-mini" {
		t.Fatalf("updated models = %q, want gpt-5,o4-mini", got)
	}
	if len(requests) != len(codexProbeCandidates(lookup.account.Models)) {
		t.Fatalf("requests = %d, want %d", len(requests), len(codexProbeCandidates(lookup.account.Models)))
	}
}

func TestCodexProbeIgnoresNonCodex(t *testing.T) {
	probe := newCodexModelProbeService(&probeLookupStub{})
	if _, err := probe.ProbeCodexModels(context.Background(), &biz.SubscriptionAccount{Platform: "openai"}); err == nil {
		t.Fatal("expected error for non-codex account")
	}
}

func TestHandleSubscriptionAccountEventParsesJSONStringPayload(t *testing.T) {
	lookup := &probeLookupStub{
		account: &biz.SubscriptionAccount{
			ID:          3,
			Platform:    "codex",
			AccessToken: "token",
			AccountID:   "acc-3",
			Models:      []string{"gpt-5"},
		},
	}
	probe := newCodexModelProbeService(lookup)
	probe.client = &http.Client{Transport: roundTripperFunc(func(*http.Request) (*http.Response, error) {
		return &http.Response{StatusCode: http.StatusBadRequest, Body: http.NoBody}, nil
	})}
	if err := probe.HandleSubscriptionAccountEvent(context.Background(), events.Event{
		Topic:   events.TopicChannelChanged,
		Payload: `{"id":3}`,
	}); err != nil {
		t.Fatalf("HandleSubscriptionAccountEvent() error = %v", err)
	}
	time.Sleep(50 * time.Millisecond)
}

func TestCodexProbeCandidates(t *testing.T) {
	got := codexProbeCandidates([]string{"gpt-5", "o4-mini", "gpt-5"})
	if len(got) < 2 {
		t.Fatalf("expected candidates, got %v", got)
	}
}

func TestProbeCodexModelsNoSupportedModels(t *testing.T) {
	lookup := &probeLookupStub{
		account: &biz.SubscriptionAccount{
			ID:          4,
			Platform:    "codex",
			AccessToken: "token",
			AccountID:   "acc-4",
			Models:      []string{"bad"},
		},
	}
	probe := newCodexModelProbeService(lookup)
	probe.client = &http.Client{Transport: roundTripperFunc(func(*http.Request) (*http.Response, error) {
		return &http.Response{
			StatusCode: http.StatusBadRequest,
			Body:       http.NoBody,
		}, nil
	})}
	restore := setCodexUpstreamURLForTest("http://example.invalid")
	defer restore()
	_, err := probe.ProbeCodexModels(context.Background(), lookup.account)
	if err == nil {
		t.Fatal("expected probe error")
	}
}

type roundTripperFunc func(*http.Request) (*http.Response, error)

func (f roundTripperFunc) RoundTrip(r *http.Request) (*http.Response, error) {
	return f(r)
}

// recoveryProbeAdapterTestCase groups the RecoveryProbeAdapter regression
// checks. Each case builds a real CodexModelProbeService + AnthropicModelProbeService
// so the adapter exercises the full platform dispatch path.
type recoveryProbeAdapterTestCase struct {
	name     string
	account  *biz.SubscriptionAccount
	setup    func(t *testing.T, account *biz.SubscriptionAccount) (cleanup func())
	wantOK   bool
	wantErr  bool
	noProber bool
}

func TestRecoveryProbeAdapter(t *testing.T) {
	cases := []recoveryProbeAdapterTestCase{
		{
			name: "codex healthy upstream returns ok",
			account: &biz.SubscriptionAccount{
				ID:          1,
				Platform:    "codex",
				AccessToken: "token",
				AccountID:   "acc-1",
				Models:      []string{"gpt-5"},
			},
			setup: func(t *testing.T, account *biz.SubscriptionAccount) func() {
				srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					// Accept every codex probe request so the probe reports success.
					w.WriteHeader(http.StatusOK)
				}))
				restore := setCodexUpstreamURLForTest(srv.URL)
				return func() { restore(); srv.Close() }
			},
			wantOK: true,
		},
		{
			name: "claude healthy upstream returns ok",
			account: &biz.SubscriptionAccount{
				ID:          2,
				Platform:    "claude",
				AccessToken: "sk-ant-oat-2",
				Models:      []string{"claude-sonnet-4-20250514"},
			},
			setup: func(t *testing.T, account *biz.SubscriptionAccount) func() {
				srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					if r.URL.Path == "/v1/messages" {
						w.WriteHeader(http.StatusOK)
						return
					}
					w.WriteHeader(http.StatusNotFound)
				}))
				restore := setClaudeDefaultBaseURLForTest(srv.URL)
				return func() { restore(); srv.Close() }
			},
			wantOK: true,
		},
		{
			name: "domestic zhipu unhealthy upstream returns not ok",
			account: &biz.SubscriptionAccount{
				ID:          3,
				Platform:    "zhipu",
				AccessToken: "plan-key-3",
				Models:      []string{"glm-4.6"},
			},
			setup: func(t *testing.T, account *biz.SubscriptionAccount) func() {
				srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					w.WriteHeader(http.StatusUnauthorized)
				}))
				account.BaseURL = srv.URL
				return func() { srv.Close() }
			},
			wantOK: false,
		},
		{
			name: "unsupported platform returns error for sweeper fallback",
			account: &biz.SubscriptionAccount{
				ID:       4,
				Platform: "openai",
			},
			setup: func(t *testing.T, account *biz.SubscriptionAccount) func() { return func() {} },
			wantErr: true,
		},
		{
			name: "claude without anthropic prober wired returns error",
			account: &biz.SubscriptionAccount{
				ID:          5,
				Platform:    "claude",
				AccessToken: "sk-ant-oat-5",
			},
			setup: func(t *testing.T, account *biz.SubscriptionAccount) func() { return func() {} },
			wantErr:  true,
			noProber: true,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			cleanup := tc.setup(t, tc.account)
			defer cleanup()

			probe := NewCodexModelProbeService(nil)
			if !tc.noProber {
				probe.SetAnthropicProber(NewAnthropicModelProbeService())
			}

			adapter := NewRecoveryProbeAdapter(probe)
			ok, err := adapter.ProbeRecovery(context.Background(), tc.account)
			if tc.wantErr {
				if err == nil {
					t.Fatalf("ProbeRecovery() err = nil, want non-nil")
				}
				return
			}
			if err != nil {
				t.Fatalf("ProbeRecovery() unexpected error = %v", err)
			}
			if ok != tc.wantOK {
				t.Fatalf("ProbeRecovery() ok = %v, want %v", ok, tc.wantOK)
			}
		})
	}
}

package service

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"micro-one-api/internal/channel/biz"
	"micro-one-api/internal/pkg/events"
)

type probeLookupStub struct {
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
	p.updated = &cloned
	return nil
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
	originalURL := codexResponsesUpstreamURL
	defer func() { codexResponsesUpstreamURL = originalURL }()
	codexResponsesUpstreamURL = srv.URL

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
	originalURL := codexResponsesUpstreamURL
	defer func() { codexResponsesUpstreamURL = originalURL }()
	codexResponsesUpstreamURL = "http://example.invalid"
	_, err := probe.ProbeCodexModels(context.Background(), lookup.account)
	if err == nil {
		t.Fatal("expected probe error")
	}
}

type roundTripperFunc func(*http.Request) (*http.Response, error)

func (f roundTripperFunc) RoundTrip(r *http.Request) (*http.Response, error) {
	return f(r)
}

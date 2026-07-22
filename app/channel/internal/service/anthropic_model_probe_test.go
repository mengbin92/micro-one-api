package service

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"micro-one-api/app/channel/internal/biz"
	"micro-one-api/platform/events"
)

func TestBuildCandidatesMergesDefaultsAndCurrent(t *testing.T) {
	probe := NewAnthropicModelProbeService()
	account := &biz.SubscriptionAccount{
		Platform: "zhipu",
		Models:   []string{"glm-4.6", "glm-custom"},
	}
	got := probe.buildCandidates(context.Background(), codingPlanProbePlatformZhipu, account)
	// defaults first, then account-supplied extras, deduped
	want := []string{"glm-4.6", "glm-4.5", "glm-4.5-air", "glm-4", "glm-custom"}
	if strings.Join(got, ",") != strings.Join(want, ",") {
		t.Fatalf("candidates = %v, want %v", got, want)
	}
}

func TestAnthropicPlatformDefaultModels(t *testing.T) {
	if got := anthropicPlatformDefaultModels(codingPlanProbePlatformMinimax); len(got) == 0 {
		t.Fatal("minimax defaults empty")
	}
	if got := anthropicPlatformDefaultModels(codingPlanProbePlatformKimi); len(got) == 0 {
		t.Fatal("kimi defaults empty")
	}
	if got := anthropicPlatformDefaultModels("codex"); got != nil {
		t.Fatalf("unknown platform defaults = %v, want nil", got)
	}
}

func TestProbeAnthropicModelsFiltersUnsupported(t *testing.T) {
	var gotAuth string
	var gotPath string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotAuth = r.Header.Get("Authorization")
		gotPath = r.URL.Path
		// Handle GET /v1/models - return the model list
		if r.Method == http.MethodGet && r.URL.Path == "/v1/models" {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte(`{"data":[{"id":"glm-4.6"},{"id":"glm-4.5"},{"id":"unknown-model"}]}`))
			return
		}
		// Handle POST /v1/messages - validate models
		var body anthropicMessagesProbeRequest
		_ = json.NewDecoder(r.Body).Decode(&body)
		if body.MaxTokens != anthropicModelProbeMaxTokens {
			t.Errorf("max_tokens = %d, want %d", body.MaxTokens, anthropicModelProbeMaxTokens)
		}
		switch body.Model {
		case "glm-4.6", "glm-4.5":
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte(`{"id":"msg_1"}`))
		default:
			w.WriteHeader(http.StatusBadRequest)
			_, _ = w.Write([]byte(`{"error":"model not supported"}`))
		}
	}))
	defer srv.Close()

	probe := NewAnthropicModelProbeService()
	models, err := probe.ProbeAnthropicModels(context.Background(), &biz.SubscriptionAccount{
		Platform:    "zhipu",
		AccessToken: "plan-key-123",
		BaseURL:     srv.URL,
	})
	if err != nil {
		t.Fatalf("ProbeAnthropicModels() error = %v", err)
	}
	if gotAuth != "Bearer plan-key-123" {
		t.Fatalf("Authorization = %q", gotAuth)
	}
	// Both /v1/models and /v1/messages will be hit, check for messages path
	if !strings.Contains(gotPath, "/v1/messages") {
		t.Fatalf("path should contain /v1/messages, got %q", gotPath)
	}
	// sorted by dedupeSortedStrings - only glm-4.5 and glm-4.6 should pass validation
	want := "glm-4.5,glm-4.6"
	if strings.Join(models, ",") != want {
		t.Fatalf("models = %v, want %v", models, want)
	}
}

func TestProbeAnthropicModelsRejectsUnsupportedPlatform(t *testing.T) {
	probe := NewAnthropicModelProbeService()
	if _, err := probe.ProbeAnthropicModels(context.Background(), &biz.SubscriptionAccount{Platform: "codex"}); err == nil {
		t.Fatal("expected error for codex platform")
	}
}

func TestProbeAnthropicModelsErrorsWhenNoneAccepted(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
	}))
	defer srv.Close()
	probe := NewAnthropicModelProbeService()
	if _, err := probe.ProbeAnthropicModels(context.Background(), &biz.SubscriptionAccount{
		Platform:    "minimax",
		AccessToken: "bad-key",
		BaseURL:     srv.URL,
	}); err == nil {
		t.Fatal("expected error when upstream rejects every candidate")
	}
}

// TestHandleSubscriptionAccountEventRoutesDomesticPlatform is the regression
// test for the reported bug: a newly created zhipu account must have its
// Models refreshed via the anthropic prober, not silently skipped.
func TestHandleSubscriptionAccountEventRoutesDomesticPlatform(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body anthropicMessagesProbeRequest
		_ = json.NewDecoder(r.Body).Decode(&body)
		if body.Model == "glm-4.6" {
			w.WriteHeader(http.StatusOK)
			return
		}
		w.WriteHeader(http.StatusBadRequest)
	}))
	defer srv.Close()

	lookup := &probeLookupStub{account: &biz.SubscriptionAccount{
		ID:          42,
		Platform:    "zhipu",
		AccessToken: "k",
		BaseURL:     srv.URL,
		Models:      []string{"glm-4.6"},
	}}
	probe := NewCodexModelProbeService(lookup)
	probe.SetAnthropicProber(NewAnthropicModelProbeService())

	if err := probe.HandleSubscriptionAccountEvent(context.Background(), events.Event{
		Topic:   events.TopicChannelChanged,
		Payload: &biz.SubscriptionAccount{ID: 42},
	}); err != nil {
		t.Fatalf("HandleSubscriptionAccountEvent() error = %v", err)
	}

	// wait for the async goroutine to write back
	updated := waitForUpdate(lookup, 2*time.Second)
	if updated == nil {
		t.Fatal("expected UpdateSubscriptionAccount to be called for zhipu account")
	}
	if strings.Join(updated.Models, ",") != "glm-4.6" {
		t.Fatalf("updated models = %v", updated.Models)
	}
	return
}

// waitForUpdate polls the stub until the async handler writes back or the
// timeout elapses. probeLookupStub has no synchronization of its own, so we
// read under a short sleep-poll loop; this mirrors how the production event
// bus delivers asynchronously.
func waitForUpdate(l *probeLookupStub, timeout time.Duration) *biz.SubscriptionAccount {
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if l.Updated() != nil {
			return l.Updated()
		}
		time.Sleep(5 * time.Millisecond)
	}
	return nil
}

// TestHandleSubscriptionAccountEventRoutesClaude covers the claude OAuth
// subscription path: newly created claude accounts (whose BaseURL is empty
// because the adaptor hardcodes api.anthropic.com) must also get their model
// list probed and refreshed.
func TestHandleSubscriptionAccountEventRoutesClaude(t *testing.T) {
	var gotAuth, gotPath string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotAuth = r.Header.Get("Authorization")
		gotPath = r.URL.Path
		var body anthropicMessagesProbeRequest
		_ = json.NewDecoder(r.Body).Decode(&body)
		switch body.Model {
		case "claude-sonnet-4-20250514", "claude-opus-4-20250514":
			w.WriteHeader(http.StatusOK)
		default:
			w.WriteHeader(http.StatusBadRequest)
		}
	}))
	defer srv.Close()

	lookup := &probeLookupStub{account: &biz.SubscriptionAccount{
		ID:          55,
		Platform:    "claude",
		AccountType: "oauth",
		AccessToken: "sk-ant-oat-1",
		BaseURL:     srv.URL, // operator override; empty BaseURL falls back to api.anthropic.com
		Models:      []string{"claude-sonnet-4-20250514"},
	}}
	probe := NewCodexModelProbeService(lookup)
	probe.SetAnthropicProber(NewAnthropicModelProbeService())

	if err := probe.HandleSubscriptionAccountEvent(context.Background(), events.Event{
		Topic:   events.TopicChannelChanged,
		Payload: &biz.SubscriptionAccount{ID: 55},
	}); err != nil {
		t.Fatalf("HandleSubscriptionAccountEvent() error = %v", err)
	}

	updated := waitForUpdate(lookup, 2*time.Second)
	if updated == nil {
		t.Fatal("expected UpdateSubscriptionAccount to be called for claude account")
	}
	if gotAuth != "Bearer sk-ant-oat-1" {
		t.Fatalf("Authorization = %q", gotAuth)
	}
	if gotPath != "/v1/messages" {
		t.Fatalf("path = %q", gotPath)
	}
	want := "claude-opus-4-20250514,claude-sonnet-4-20250514"
	if strings.Join(updated.Models, ",") != want {
		t.Fatalf("updated models = %v, want %v", updated.Models, want)
	}
}

// TestProbeAnthropicModelsClaudeDefaultBaseURL verifies that a claude account
// with an empty BaseURL (the common case — the adaptor hardcodes
// api.anthropic.com) falls back to the default upstream instead of erroring
// with "missing base url".
func TestProbeAnthropicModelsClaudeDefaultBaseURL(t *testing.T) {
	var gotPath string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		var body anthropicMessagesProbeRequest
		_ = json.NewDecoder(r.Body).Decode(&body)
		if body.Model == "claude-sonnet-4-20250514" {
			w.WriteHeader(http.StatusOK)
			return
		}
		w.WriteHeader(http.StatusBadRequest)
	}))
	defer srv.Close()
	restore := setClaudeDefaultBaseURLForTest(srv.URL)
	defer restore()

	probe := NewAnthropicModelProbeService()
	models, err := probe.ProbeAnthropicModels(context.Background(), &biz.SubscriptionAccount{
		Platform:    "claude",
		AccountType: "oauth",
		AccessToken: "sk-ant-oat-1",
		// BaseURL intentionally empty — exercises the fallback.
	})
	if err != nil {
		t.Fatalf("ProbeAnthropicModels() error = %v", err)
	}
	if gotPath != "/v1/messages" {
		t.Fatalf("path = %q, want /v1/messages (default base fallback)", gotPath)
	}
	if strings.Join(models, ",") != "claude-sonnet-4-20250514" {
		t.Fatalf("models = %v", models)
	}
}

// TestHandleSubscriptionAccountEventSkipsDomesticWhenNoProber preserves the old
// fail-safe: without an anthropic prober wired, domestic events must not error
// the handler (just no-op via the async path).
func TestHandleSubscriptionAccountEventSkipsDomesticWhenNoProber(t *testing.T) {
	lookup := &probeLookupStub{account: &biz.SubscriptionAccount{
		ID:       7,
		Platform: "kimi",
	}}
	probe := NewCodexModelProbeService(lookup) // no SetAnthropicProber
	if err := probe.HandleSubscriptionAccountEvent(context.Background(), events.Event{
		Topic:   events.TopicChannelChanged,
		Payload: &biz.SubscriptionAccount{ID: 7},
	}); err != nil {
		t.Fatalf("HandleSubscriptionAccountEvent() error = %v", err)
	}
}

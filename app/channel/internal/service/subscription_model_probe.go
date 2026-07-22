package service

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"sort"
	"strings"
	"sync"
	"time"

	"micro-one-api/app/channel/internal/biz"
	"micro-one-api/platform/events"
)

// codexResponsesUpstreamURL is a var (not const) so tests can point the probe
// at an httptest server. Reads/writes go through codexUpstreamURLMu because
// the async event handler reads it from a background goroutine while tests
// swap it on the main test goroutine.
var (
	codexResponsesUpstreamURL = "https://chatgpt.com/backend-api/codex/responses"
	codexUpstreamURLMu        sync.RWMutex
	// errProbeNotApplicable signals that the platform is not supported or the
	// required sub-prober is not wired. Callers such as RecoveryProbeAdapter
	// treat this as "probe unavailable" and fall back to local-state behavior.
	errProbeNotApplicable = errors.New("probe not applicable for this account")
)

// codexUpstreamURL returns the current upstream URL under the read lock.
func codexUpstreamURL() string {
	codexUpstreamURLMu.RLock()
	defer codexUpstreamURLMu.RUnlock()
	return codexResponsesUpstreamURL
}

// setCodexUpstreamURLForTest swaps the upstream URL and returns a restore
// func. Exported-for-test via the same package.
func setCodexUpstreamURLForTest(url string) func() {
	codexUpstreamURLMu.Lock()
	prev := codexResponsesUpstreamURL
	codexResponsesUpstreamURL = url
	codexUpstreamURLMu.Unlock()
	return func() {
		codexUpstreamURLMu.Lock()
		codexResponsesUpstreamURL = prev
		codexUpstreamURLMu.Unlock()
	}
}

type subscriptionAccountLookup interface {
	FindSubscriptionAccountByID(ctx context.Context, accountID int64) (*biz.SubscriptionAccount, error)
	UpdateSubscriptionAccount(ctx context.Context, account *biz.SubscriptionAccount) error
}

type subscriptionAccountModelProber interface {
	ProbeCodexModels(ctx context.Context, account *biz.SubscriptionAccount) ([]string, error)
	// ProbeAccountModels dispatches to the platform-appropriate low-level prober
	// without persisting anything. Used by the recovery probe adapter.
	ProbeAccountModels(ctx context.Context, account *biz.SubscriptionAccount) ([]string, error)
}

type subscriptionAccountLister interface {
	ListSubscriptionAccounts(ctx context.Context, page, pageSize int32, keyword, group string, status int32, platform string) ([]*biz.SubscriptionAccount, int64, error)
}

type codexModelProbeRequest struct {
	Model           string `json:"model"`
	Input           any    `json:"input"`
	MaxOutputTokens int    `json:"max_output_tokens"`
	Stream          bool   `json:"stream"`
	Store           bool   `json:"store"`
}

type codexModelProbeInputItem struct {
	Role    string                       `json:"role"`
	Content []codexModelProbeContentItem `json:"content"`
}

type codexModelProbeContentItem struct {
	Type string `json:"type"`
	Text string `json:"text"`
}

type CodexModelProbeService struct {
	lookup  subscriptionAccountLookup
	client  *http.Client
	mu      sync.Mutex
	pending map[int64]struct{}
	// anthropic is the optional prober for the domestic Anthropic-compatible
	// Coding Plan platforms (zhipu/minimax/kimi). When nil, events for those
	// platforms are skipped (previous behaviour).
	anthropic *AnthropicModelProbeService
}

func newCodexModelProbeService(lookup subscriptionAccountLookup) *CodexModelProbeService {
	return &CodexModelProbeService{
		lookup:  lookup,
		client:  &http.Client{Timeout: 20 * time.Second},
		pending: make(map[int64]struct{}),
	}
}

func NewCodexModelProbeService(lookup subscriptionAccountLookup) *CodexModelProbeService {
	return newCodexModelProbeService(lookup)
}

// SetAnthropicProber wires the domestic-platform prober used to route
// non-codex subscription-account events. Optional; nil disables routing.
func (s *CodexModelProbeService) SetAnthropicProber(p *AnthropicModelProbeService) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.anthropic = p
}

func (s *CodexModelProbeService) anthropicProber() *AnthropicModelProbeService {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.anthropic
}

// SyncExistingCodexAccounts backfills model probes for accounts that already
// existed before the service started (no create event fires for those). It
// sweeps every probeable platform — codex, claude, and the domestic three —
// routing each account through the same per-platform dispatch used by the
// event handler.
func (s *CodexModelProbeService) SyncExistingCodexAccounts(ctx context.Context, lister subscriptionAccountLister) {
	if s == nil || s.lookup == nil || lister == nil {
		return
	}
	platforms := []string{
		"codex",
		anthropicModelProbePlatformClaude,
		codingPlanProbePlatformZhipu,
		codingPlanProbePlatformMinimax,
		codingPlanProbePlatformKimi,
	}
	for _, platform := range platforms {
		accounts, _, err := lister.ListSubscriptionAccounts(ctx, 1, 1000, "", "", 0, platform)
		if err != nil {
			continue
		}
		for _, account := range accounts {
			if account == nil {
				continue
			}
			if strings.ToLower(strings.TrimSpace(account.Platform)) != platform {
				continue
			}
			if !s.markPending(account.ID) {
				continue
			}
			go func(accountID int64) {
				defer s.unmarkPending(accountID)
				probeCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
				defer cancel()
				_ = s.syncModelsForAccount(probeCtx, accountID)
			}(account.ID)
		}
	}
}

func (s *CodexModelProbeService) HandleSubscriptionAccountEvent(ctx context.Context, event events.Event) error {
	if s == nil || s.lookup == nil || event.Topic != events.TopicChannelChanged {
		return nil
	}
	accountID := subscriptionAccountIDFromEventPayload(event.Payload)
	if accountID <= 0 {
		return nil
	}
	if !s.markPending(accountID) {
		return nil
	}
	go func() {
		defer s.unmarkPending(accountID)
		probeCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
		defer cancel()
		_ = s.syncModelsForAccount(probeCtx, accountID)
	}()
	return nil
}

// syncModelsForAccount resolves the account, then routes to the platform-
// appropriate prober. Codex accounts keep the existing Responses-API probe;
// Claude and the domestic Anthropic-compatible platforms (zhipu/minimax/kimi)
// use the Messages-API probe. Previously this path was codex-only, so newly
// added claude/domestic accounts never had their supported-model list
// refreshed after creation.
func (s *CodexModelProbeService) syncModelsForAccount(ctx context.Context, accountID int64) error {
	account, err := s.lookup.FindSubscriptionAccountByID(ctx, accountID)
	if err != nil {
		return err
	}
	models, err := s.ProbeAccountModels(ctx, account)
	if err != nil {
		return err
	}
	account.Models = models
	return s.lookup.UpdateSubscriptionAccount(ctx, account)
}

// ProbeAccountModels dispatches to the platform-appropriate low-level prober
// and returns the supported models without persisting them. Codex accounts use
// the Responses-API probe; claude and the domestic Anthropic-compatible
// platforms (zhipu/minimax/kimi) use the Messages-API probe. Unsupported
// platforms and missing anthropic prober wiring return an error so callers can
// fall back to local-state behavior.
func (s *CodexModelProbeService) ProbeAccountModels(ctx context.Context, account *biz.SubscriptionAccount) ([]string, error) {
	if s == nil {
		return nil, errors.New("codex model prober is not configured")
	}
	if account == nil {
		return nil, errors.New("subscription account is required")
	}
	platform := strings.ToLower(strings.TrimSpace(account.Platform))
	switch platform {
	case "codex", "":
		return s.ProbeCodexModels(ctx, account)
	case anthropicModelProbePlatformClaude,
		codingPlanProbePlatformZhipu, codingPlanProbePlatformMinimax, codingPlanProbePlatformKimi:
		prober := s.anthropicProber()
		if prober == nil {
			return nil, fmt.Errorf("%w: no anthropic prober configured for platform %q", errProbeNotApplicable, platform)
		}
		return prober.ProbeAnthropicModels(ctx, account)
	default:
		return nil, fmt.Errorf("%w: platform %q is not probeable", errProbeNotApplicable, platform)
	}
}

func (s *CodexModelProbeService) ProbeCodexModels(ctx context.Context, account *biz.SubscriptionAccount) ([]string, error) {
	if s == nil {
		return nil, errors.New("codex model prober is not configured")
	}
	if account == nil {
		return nil, errors.New("subscription account is required")
	}
	if strings.ToLower(strings.TrimSpace(account.Platform)) != "codex" {
		return nil, fmt.Errorf("unsupported platform %q", account.Platform)
	}
	if strings.TrimSpace(account.AccessToken) == "" || strings.TrimSpace(account.AccountID) == "" {
		return nil, errors.New("missing codex access token or account_id")
	}

	candidates := codexProbeCandidates(account.Models)
	if len(candidates) == 0 {
		return nil, errors.New("no codex probe candidates available")
	}

	var supported []string
	for _, model := range candidates {
		ok, err := s.probeModel(ctx, account, model)
		if err != nil {
			continue
		}
		if ok {
			supported = append(supported, model)
		}
	}
	supported = dedupeSortedStrings(supported)
	if len(supported) == 0 {
		return nil, errors.New("no codex models were accepted by the upstream")
	}
	return supported, nil
}

func (s *CodexModelProbeService) syncCodexModels(ctx context.Context, accountID int64) error {
	return s.syncModelsForAccount(ctx, accountID)
}

func (s *CodexModelProbeService) probeModel(ctx context.Context, account *biz.SubscriptionAccount, model string) (bool, error) {
	payload := codexModelProbeRequest{
		Model:           model,
		Input:           []codexModelProbeInputItem{{Role: "user", Content: []codexModelProbeContentItem{{Type: "input_text", Text: "hi"}}}},
		MaxOutputTokens: 1,
		Stream:          false,
		Store:           false,
	}
	body, err := json.Marshal(payload)
	if err != nil {
		return false, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, codexUpstreamURL(), bytes.NewReader(body))
	if err != nil {
		return false, err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+account.AccessToken)
	req.Header.Set("chatgpt-account-id", account.AccountID)
	req.Header.Set("originator", "codex_cli_rs")
	req.Header.Set("OpenAI-Beta", "responses=experimental")
	req.Header.Set("User-Agent", "codex_cli_rs")

	resp, err := s.client.Do(req)
	if err != nil {
		return false, err
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode >= 200 && resp.StatusCode < 300 {
		return true, nil
	}
	return false, nil
}

func codexProbeCandidates(current []string) []string {
	base := []string{
		"gpt-5.5",
		"gpt-5.4",
		"gpt-5.4-mini",
		"gpt-5.3-codex",
		"gpt-5.2",
		"gpt-5",
		"gpt-5-codex",
		"codex-mini-latest",
		"o4-mini",
	}
	seen := make(map[string]struct{}, len(base)+len(current))
	out := make([]string, 0, len(base)+len(current))
	add := func(model string) {
		model = strings.TrimSpace(model)
		if model == "" {
			return
		}
		if _, ok := seen[model]; ok {
			return
		}
		seen[model] = struct{}{}
		out = append(out, model)
	}
	for _, model := range base {
		add(model)
	}
	for _, model := range current {
		add(model)
	}
	return out
}

func dedupeSortedStrings(values []string) []string {
	if len(values) == 0 {
		return nil
	}
	seen := make(map[string]struct{}, len(values))
	out := make([]string, 0, len(values))
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value == "" {
			continue
		}
		if _, ok := seen[value]; ok {
			continue
		}
		seen[value] = struct{}{}
		out = append(out, value)
	}
	sort.Strings(out)
	return out
}

func subscriptionAccountIDFromEventPayload(payload any) int64 {
	switch v := payload.(type) {
	case *biz.SubscriptionAccount:
		if v != nil {
			return v.ID
		}
	case biz.SubscriptionAccount:
		return v.ID
	case map[string]any:
		if id, ok := v["id"]; ok {
			switch n := id.(type) {
			case float64:
				return int64(n)
			case int64:
				return n
			case int:
				return int64(n)
			}
		}
	}
	if payload != nil {
		if raw, ok := payload.(string); ok && strings.TrimSpace(raw) != "" {
			var account biz.SubscriptionAccount
			if err := json.Unmarshal([]byte(raw), &account); err == nil {
				return account.ID
			}
			var event events.Event
			if err := json.Unmarshal([]byte(raw), &event); err == nil {
				return subscriptionAccountIDFromEventPayload(event.Payload)
			}
		}
		if raw, ok := payload.([]byte); ok && len(raw) > 0 {
			var account biz.SubscriptionAccount
			if err := json.Unmarshal(raw, &account); err == nil {
				return account.ID
			}
		}
	}
	return 0
}

func (s *CodexModelProbeService) markPending(accountID int64) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, ok := s.pending[accountID]; ok {
		return false
	}
	s.pending[accountID] = struct{}{}
	return true
}

func (s *CodexModelProbeService) unmarkPending(accountID int64) {
	s.mu.Lock()
	delete(s.pending, accountID)
	s.mu.Unlock()
}

// RecoveryProbeAdapter adapts the model probe into a biz.RecoveryProber.
// It performs the SAME lightweight 1-token upstream request the model probe
// uses, but interprets the result for account recovery: if the upstream
// accepts the request the account is healthy and can be re-enabled; if it
// rejects it the account is still failing and must not be re-enabled. Codex,
// claude, and the domestic Anthropic-compatible Coding Plan platforms
// (zhipu/minimax/kimi) are probed; other platforms return an error so the
// sweeper falls back to its local-state recovery path.
type RecoveryProbeAdapter struct {
	probe subscriptionAccountModelProber
}

// NewRecoveryProbeAdapter wraps a model prober as a recovery prober.
func NewRecoveryProbeAdapter(probe subscriptionAccountModelProber) *RecoveryProbeAdapter {
	return &RecoveryProbeAdapter{probe: probe}
}

// ProbeRecovery implements biz.RecoveryProber. ok=true means the upstream
// accepted a lightweight request (account is healthy); ok=false means the
// upstream still rejects the account; err!=nil means the probe does not apply
// to this platform or could not run, so the sweeper falls back to local state.
func (a *RecoveryProbeAdapter) ProbeRecovery(ctx context.Context, account *biz.SubscriptionAccount) (bool, error) {
	if a == nil || a.probe == nil || account == nil {
		return false, errors.New("recovery probe not configured")
	}
	// A successful model probe means the access token still works and the
	// upstream is accepting requests, i.e. the account has recovered.
	if _, err := a.probe.ProbeAccountModels(ctx, account); err != nil {
		if errors.Is(err, errProbeNotApplicable) {
			return false, err
		}
		return false, nil
	}
	return true, nil
}

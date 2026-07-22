package service

// This file implements AnthropicModelProbeService: a lightweight model prober
// for the domestic "Coding Plan" subscription platforms (Zhipu GLM, MiniMax,
// Kimi). All three expose an Anthropic-compatible Messages endpoint, so the
// probe reuses the request shape that ClaudeOAuthAdaptor.BuildUpstreamRequest
// emits: POST {base}/v1/messages with `max_tokens=1` and a one-token reply.
// A 2xx response means the upstream accepted the model for this account;
// anything else (401/403 auth, 4xx model rejection, 5xx) counts as "not
// supported" and the candidate is dropped.
//
// Unlike the Codex probe this service does NOT subscribe to events itself —
// the dispatcher in subscription_model_probe.go routes per-platform.

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"sync"
	"time"

	"micro-one-api/app/channel/internal/biz"
)

const (
	anthropicModelProbeHTTPTimeout = 20 * time.Second
	anthropicModelProbeMaxTokens   = 1
	// anthropicModelProbePlatformClaude is the Claude Code subscription
	// platform; unlike the domestic three its account BaseURL is usually empty
	// because ClaudeOAuthAdaptor hardcodes the default upstream.
	anthropicModelProbePlatformClaude = "claude"
)

// anthropicClaudeDefaultBaseURL mirrors ClaudeOAuthAdaptor.GetUpstreamURL's
// hardcoded default. It is a var so tests can point the probe at an httptest
// server; guarded the same way as codexResponsesUpstreamURL.
var (
	anthropicClaudeDefaultBaseURL   = "https://api.anthropic.com"
	anthropicClaudeDefaultBaseURLMu sync.RWMutex
)

func claudeDefaultBaseURL() string {
	anthropicClaudeDefaultBaseURLMu.RLock()
	defer anthropicClaudeDefaultBaseURLMu.RUnlock()
	return anthropicClaudeDefaultBaseURL
}

// setClaudeDefaultBaseURLForTest swaps the claude fallback base URL and
// returns a restore func.
func setClaudeDefaultBaseURLForTest(url string) func() {
	anthropicClaudeDefaultBaseURLMu.Lock()
	prev := anthropicClaudeDefaultBaseURL
	anthropicClaudeDefaultBaseURL = url
	anthropicClaudeDefaultBaseURLMu.Unlock()
	return func() {
		anthropicClaudeDefaultBaseURLMu.Lock()
		anthropicClaudeDefaultBaseURL = prev
		anthropicClaudeDefaultBaseURLMu.Unlock()
	}
}

// anthropicMessagesProbeRequest mirrors the minimal Anthropic Messages payload
// the relay sends upstream. max_tokens=1 keeps the probe cheap.
type anthropicMessagesProbeRequest struct {
	Model     string                        `json:"model"`
	MaxTokens int                           `json:"max_tokens"`
	Messages  []anthropicMessagesProbeInput `json:"messages"`
}

type anthropicMessagesProbeInput struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

// anthropicModelsResponse is the OpenAI-compatible /v1/models response format.
type anthropicModelsResponse struct {
	Data []anthropicModelEntry `json:"data"`
}

type anthropicModelEntry struct {
	ID      string `json:"id"`
	OwnedBy string `json:"owned_by,omitempty"`
}

// AnthropicModelProbeService probes model availability against an
// Anthropic-compatible upstream for domestic Coding Plan accounts.
type AnthropicModelProbeService struct {
	client *http.Client
}

// NewAnthropicModelProbeService builds the prober. It is always non-nil; the
// caller guards on account platform instead (same pattern as the codex probe).
func NewAnthropicModelProbeService() *AnthropicModelProbeService {
	return &AnthropicModelProbeService{
		client: &http.Client{Timeout: anthropicModelProbeHTTPTimeout},
	}
}

// ProbeAnthropicModels returns the subset of candidate models the upstream
// accepts for this account. Candidates are the account's currently configured
// Models plus the platform defaults, deduplicated. If the account has no
// candidates at all, or none are accepted, an error is returned so callers do
// not wipe the existing model list with an empty result.
//
// For domestic Coding Plan platforms (Zhipu, MiniMax, Kimi), this function first
// attempts to fetch the model list dynamically from the upstream's /v1/models
// endpoint. If the fetch fails (404/405/network error), it falls back to the
// hardcoded platform defaults.
func (s *AnthropicModelProbeService) ProbeAnthropicModels(ctx context.Context, account *biz.SubscriptionAccount) ([]string, error) {
	if s == nil {
		return nil, errors.New("anthropic model prober is not configured")
	}
	if account == nil {
		return nil, errors.New("subscription account is required")
	}
	platform := strings.ToLower(strings.TrimSpace(account.Platform))
	switch platform {
	case anthropicModelProbePlatformClaude,
		codingPlanProbePlatformZhipu, codingPlanProbePlatformMinimax, codingPlanProbePlatformKimi:
	default:
		return nil, fmt.Errorf("unsupported platform %q", account.Platform)
	}
	if strings.TrimSpace(account.AccessToken) == "" {
		return nil, errors.New("missing access token / plan key")
	}

	// Build candidates: try dynamic fetch for domestic platforms, fall back to defaults
	candidates := s.buildCandidates(ctx, platform, account)
	if len(candidates) == 0 {
		return nil, errors.New("no probe candidates available")
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
		return nil, errors.New("no models were accepted by the upstream")
	}
	return supported, nil
}

// buildCandidates returns the candidate models for probing. For domestic Coding
// Plan platforms (Zhipu, MiniMax, Kimi), it attempts to fetch the model list
// dynamically from /v1/models. Falls back to hardcoded defaults on any error.
func (s *AnthropicModelProbeService) buildCandidates(ctx context.Context, platform string, account *biz.SubscriptionAccount) []string {
	var dynamicModels []string
	var fetchErr error

	// Try dynamic fetch for domestic Coding Plan platforms
	switch platform {
	case codingPlanProbePlatformZhipu, codingPlanProbePlatformMinimax, codingPlanProbePlatformKimi:
		dynamicModels, fetchErr = fetchModels(ctx, s.client, account.BaseURL, account.AccessToken)
		// Log the error but don't fail - we'll fall back to defaults
		if fetchErr != nil {
			// TODO: add logging
			fmt.Printf("failed to fetch models for platform %s: %v (falling back to defaults)\n", platform, fetchErr)
		}
	}

	// Start with dynamic models or fallback to platform defaults
	baseCandidates := dynamicModels
	if len(baseCandidates) == 0 {
		baseCandidates = anthropicPlatformDefaultModels(platform)
	}

	// Merge with account's existing models (preserves custom models)
	seen := make(map[string]struct{}, len(baseCandidates)+len(account.Models))
	out := make([]string, 0, len(baseCandidates)+len(account.Models))
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

	// Add platform models first (dynamic or defaults)
	for _, model := range baseCandidates {
		add(model)
	}
	// Then add account's existing models (keeps custom models)
	for _, model := range account.Models {
		add(model)
	}

	return out
}

// probeModel issues the 1-token Messages request and reports whether the
// upstream accepted it. Mirrors ClaudeOAuthAdaptor.GetUpstreamURL +
// BuildUpstreamRequest: POST {base}/v1/messages, Bearer auth, anthropic-beta
// header.
func (s *AnthropicModelProbeService) probeModel(ctx context.Context, account *biz.SubscriptionAccount, model string) (bool, error) {
	base := strings.TrimRight(strings.TrimSpace(account.BaseURL), "/")
	if base == "" {
		if strings.ToLower(strings.TrimSpace(account.Platform)) == anthropicModelProbePlatformClaude {
			base = claudeDefaultBaseURL()
		} else {
			return false, errors.New("missing base url")
		}
	}
	url := base + "/v1/messages?beta=true"

	payload := anthropicMessagesProbeRequest{
		Model:     model,
		MaxTokens: anthropicModelProbeMaxTokens,
		Messages:  []anthropicMessagesProbeInput{{Role: "user", Content: "hi"}},
	}
	body, err := json.Marshal(payload)
	if err != nil {
		return false, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(body))
	if err != nil {
		return false, err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+strings.TrimSpace(account.AccessToken))
	req.Header.Set("anthropic-beta", "messages-2023-12-15")

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

// fetchModels attempts to fetch the model list dynamically from the upstream's
// /v1/models endpoint. Returns the fetched models or an error.
func fetchModels(ctx context.Context, client *http.Client, baseURL string, accessToken string) ([]string, error) {
	base := strings.TrimRight(strings.TrimSpace(baseURL), "/")
	if base == "" {
		return nil, errors.New("missing base url")
	}

	// Try /v1/models endpoint
	url := base + "/v1/models"
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, fmt.Errorf("create request: %w", err)
	}
	req.Header.Set("Authorization", "Bearer "+strings.TrimSpace(accessToken))
	req.Header.Set("Content-Type", "application/json")

	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("fetch models: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()

	// 404/405 means endpoint not supported, return empty to fall back to defaults
	if resp.StatusCode == http.StatusNotFound || resp.StatusCode == http.StatusMethodNotAllowed {
		return nil, nil
	}

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("unexpected status: %d", resp.StatusCode)
	}

	var modelsResp anthropicModelsResponse
	if err := json.NewDecoder(resp.Body).Decode(&modelsResp); err != nil {
		return nil, fmt.Errorf("decode response: %w", err)
	}

	models := make([]string, 0, len(modelsResp.Data))
	for _, entry := range modelsResp.Data {
		if entry.ID != "" {
			models = append(models, entry.ID)
		}
	}
	return models, nil
}

// anthropicPlatformDefaultModels is the fallback candidate list per platform.
// These mirror the "模型按厂商填写" guidance in
// docs/runbooks/cn-coding-subscription-accounts-runbook.md.
func anthropicPlatformDefaultModels(platform string) []string {
	switch platform {
	case anthropicModelProbePlatformClaude:
		// Mirror biz/oauth.defaultModels(PlatformClaude) plus the older 3.5
		// generation so upgrades/downgrades are both detected. The probe
		// drops whatever the upstream rejects, so listing a superset is safe.
		return []string{
			"claude-sonnet-4-20250514",
			"claude-opus-4-20250514",
			"claude-3-5-sonnet-20241022",
			"claude-3-5-haiku-20241022",
		}
	case codingPlanProbePlatformZhipu:
		return []string{"glm-4.6", "glm-4.5", "glm-4.5-air", "glm-4"}
	case codingPlanProbePlatformMinimax:
		return []string{
			"MiniMax-M2",
			"MiniMax-M2.1",
			"MiniMax-M2.1-highspeed",
			"MiniMax-M2.5",
			"MiniMax-M2.5-highspeed",
			"MiniMax-M2.7",
			"MiniMax-M2.7-highspeed",
		}
	case codingPlanProbePlatformKimi:
		return []string{"kimi-k2-0905-preview", "kimi-k2-turbo-preview", "kimi-k2"}
	default:
		return nil
	}
}

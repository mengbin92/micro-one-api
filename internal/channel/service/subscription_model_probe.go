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

	"micro-one-api/internal/channel/biz"
	"micro-one-api/internal/pkg/events"
)

var codexResponsesUpstreamURL = "https://chatgpt.com/backend-api/codex/responses"

type subscriptionAccountLookup interface {
	FindSubscriptionAccountByID(ctx context.Context, accountID int64) (*biz.SubscriptionAccount, error)
	UpdateSubscriptionAccount(ctx context.Context, account *biz.SubscriptionAccount) error
}

type subscriptionAccountModelProber interface {
	ProbeCodexModels(ctx context.Context, account *biz.SubscriptionAccount) ([]string, error)
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

type codexModelProbeService struct {
	lookup  subscriptionAccountLookup
	client  *http.Client
	mu      sync.Mutex
	pending map[int64]struct{}
}

func newCodexModelProbeService(lookup subscriptionAccountLookup) *codexModelProbeService {
	return &codexModelProbeService{
		lookup:  lookup,
		client:  &http.Client{Timeout: 20 * time.Second},
		pending: make(map[int64]struct{}),
	}
}

func NewCodexModelProbeService(lookup subscriptionAccountLookup) *codexModelProbeService {
	return newCodexModelProbeService(lookup)
}

func (s *codexModelProbeService) SyncExistingCodexAccounts(ctx context.Context, lister subscriptionAccountLister) {
	if s == nil || s.lookup == nil || lister == nil {
		return
	}
	accounts, _, err := lister.ListSubscriptionAccounts(ctx, 1, 1000, "", "", 0, "codex")
	if err != nil {
		return
	}
	for _, account := range accounts {
		if account == nil {
			continue
		}
		if strings.ToLower(strings.TrimSpace(account.Platform)) != "codex" {
			continue
		}
		if !s.markPending(account.ID) {
			continue
		}
		go func(accountID int64) {
			defer s.unmarkPending(accountID)
			probeCtx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
			defer cancel()
			_ = s.syncCodexModels(probeCtx, accountID)
		}(account.ID)
	}
}

func (s *codexModelProbeService) HandleSubscriptionAccountEvent(ctx context.Context, event events.Event) error {
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
		probeCtx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		_ = s.syncCodexModels(probeCtx, accountID)
	}()
	return nil
}

func (s *codexModelProbeService) ProbeCodexModels(ctx context.Context, account *biz.SubscriptionAccount) ([]string, error) {
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

func (s *codexModelProbeService) syncCodexModels(ctx context.Context, accountID int64) error {
	account, err := s.lookup.FindSubscriptionAccountByID(ctx, accountID)
	if err != nil {
		return err
	}
	models, err := s.ProbeCodexModels(ctx, account)
	if err != nil {
		return err
	}
	account.Models = models
	if err := s.lookup.UpdateSubscriptionAccount(ctx, account); err != nil {
		return err
	}
	return nil
}

func (s *codexModelProbeService) probeModel(ctx context.Context, account *biz.SubscriptionAccount, model string) (bool, error) {
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
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, codexResponsesUpstreamURL, bytes.NewReader(body))
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

func (s *codexModelProbeService) markPending(accountID int64) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, ok := s.pending[accountID]; ok {
		return false
	}
	s.pending[accountID] = struct{}{}
	return true
}

func (s *codexModelProbeService) unmarkPending(accountID int64) {
	s.mu.Lock()
	delete(s.pending, accountID)
	s.mu.Unlock()
}

// Package service provides the channel-service transport adapters.
//
// This file implements the CodingPlanQuotaProbeService: a background worker that
// periodically queries the upstream coding-plan quota APIs (Zhipu GLM, Kimi,
// MiniMax) for subscription accounts of those platforms, and records the
// resulting snapshot via ChannelRepo.RecordAccountQuotaSnapshot so the admin
// UI (SubscriptionAccountSummary) can render the real upstream usage.
//
// The implementation mirrors cc-switch's src-tauri/src/services/coding_plan.rs:
//
//   - Zhipu:  GET {base}/api/monitor/usage/quota/limit  (Authorization: {key}, no Bearer)
//   - Kimi:   GET https://api.kimi.com/coding/v1/usages (Authorization: Bearer {key})
//   - MiniMax: GET https://{api.minimaxi.com|api.minimax.io}/v1/api/openplatform/coding_plan/remains
//             (Authorization: Bearer {key})
//
// The snapshot is mapped to AccountQuotaSnapshot with:
//   Primary   = 5-hour window    (window_minutes=300)
//   Secondary = weekly window    (window_minutes=10080)
//
// Env toggles (consistent with the other startAccountOpsAutomation workers):
//   CODING_PLAN_QUOTA_PROBE_ENABLED   (default false)
//   CODING_PLAN_QUOTA_PROBE_INTERVAL  (default 5m)
//   CODING_PLAN_QUOTA_PROBE_TIMEOUT    (default 30s)
package service

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"

	"micro-one-api/app/channel/internal/biz"
	"micro-one-api/platform/metrics"

	"github.com/bytedance/sonic"
)

const (
	codingPlanProbeFiveHourMinutes   = 300
	codingPlanProbeWeeklyMinutes     = 10080
	codingPlanProbeDefaultInterval   = 5 * time.Minute
	codingPlanProbeDefaultTimeout    = 30 * time.Second
	codingPlanProbeHTTPTimeout        = 15 * time.Second // per-upstream-request cap, matches cc-switch
	codingPlanProbeDefaultPageSize    = 200
	codingPlanProbePlatformZhipu      = "zhipu"
	codingPlanProbePlatformMinimax    = "minimax"
	codingPlanProbePlatformKimi       = "kimi"
)

// codingPlanQuotaRepo is the subset of biz.ChannelRepo needed by the probe.
// Declared as an interface so the production *data.Repository and tests can
// both satisfy it; we keep it unexported to avoid leaking storage primitives
// into the service layer.
type codingPlanQuotaRepo interface {
	ListSubscriptionAccounts(ctx context.Context, page, pageSize int32, keyword, group string, status int32, platform string) ([]*biz.SubscriptionAccount, int64, error)
	RecordAccountQuotaSnapshot(ctx context.Context, snapshot *biz.AccountQuotaSnapshot) error
}

// CodingPlanQuotaProbeConfig configures the background quota probe worker.
type CodingPlanQuotaProbeConfig struct {
	Enabled  bool
	Interval time.Duration
	Timeout  time.Duration
	PageSize int32
}

// CodingPlanQuotaProbeService periodically queries upstream coding-plan quota
// APIs and writes the result into account_quota_snapshots. It only handles
// static_key / apikey style credentials (Zhipu/MiniMax) and the Kimi OAuth
// access token; it does NOT touch Codex/Claude OAuth accounts (those use the
// existing passive header-sampling path in the relay-gateway).
type CodingPlanQuotaProbeService struct {
	repo   codingPlanQuotaRepo
	client *http.Client
	cfg    CodingPlanQuotaProbeConfig
	now    func() time.Time
}

// NewCodingPlanQuotaProbeService builds the probe service. It is safe to
// return a nil receiver when cfg is disabled so wire-style callers can guard
// with a nil check (same pattern as NewCodexModelProbeService).
func NewCodingPlanQuotaProbeService(repo codingPlanQuotaRepo, cfg CodingPlanQuotaProbeConfig) *CodingPlanQuotaProbeService {
	if !cfg.Enabled {
		return nil
	}
	if cfg.Interval <= 0 {
		cfg.Interval = codingPlanProbeDefaultInterval
	}
	if cfg.PageSize <= 0 {
		cfg.PageSize = codingPlanProbeDefaultPageSize
	}
	if cfg.Timeout <= 0 {
		cfg.Timeout = codingPlanProbeDefaultTimeout
	}
	return &CodingPlanQuotaProbeService{
		repo:   repo,
		client: &http.Client{Timeout: codingPlanProbeHTTPTimeout},
		cfg:    cfg,
		now:    time.Now,
	}
}

// SetNow overrides the clock (for tests).
func (s *CodingPlanQuotaProbeService) SetNow(f func() time.Time) { s.now = f }

// Run starts the periodic loop; it blocks until ctx is cancelled.
func (s *CodingPlanQuotaProbeService) Run(ctx context.Context) {
	if s == nil {
		return
	}
	// Run once immediately so the admin UI has data before the first tick.
	s.sweep(ctx)
	ticker := time.NewTicker(s.cfg.Interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			s.sweep(ctx)
		}
	}
}

// sweepOnce performs a single scan + probe pass. Exported for tests.
func (s *CodingPlanQuotaProbeService) SweepOnce(ctx context.Context) error {
	if s == nil {
		return nil
	}
	return s.sweep(ctx)
}

func (s *CodingPlanQuotaProbeService) sweep(ctx context.Context) error {
	probeCtx, cancel := context.WithTimeout(ctx, s.cfg.Timeout)
	defer cancel()

	platforms := []string{codingPlanProbePlatformZhipu, codingPlanProbePlatformMinimax, codingPlanProbePlatformKimi}
	var lastErr error
	for _, platform := range platforms {
		if err := s.probePlatform(probeCtx, platform); err != nil {
			lastErr = err
		}
	}
	return lastErr
}

// probePlatform pages through all enabled subscription accounts of the given
// platform and probes each one.
func (s *CodingPlanQuotaProbeService) probePlatform(ctx context.Context, platform string) error {
	page := int32(1)
	for {
		accounts, total, err := s.repo.ListSubscriptionAccounts(ctx, page, s.cfg.PageSize, "", "", biz.ChannelStatusEnabled, platform)
		if err != nil {
			metrics.SubscriptionAccountRecoveriesTotal.WithLabelValues("coding_plan_probe_list_error", platform).Inc()
			return fmt.Errorf("list %s accounts page %d: %w", platform, page, err)
		}
		for _, account := range accounts {
			if account == nil {
				continue
			}
			if err := s.probeOne(ctx, account); err != nil {
				// per-account failures must not abort the whole scan; metrics
				// capture the failure for observability.
				metrics.SubscriptionAccountRecoveriesTotal.WithLabelValues("coding_plan_probe_error", platform).Inc()
				continue
			}
			metrics.SubscriptionAccountRecoveriesTotal.WithLabelValues("coding_plan_probe_ok", platform).Inc()
		}
		if int64(page)*int64(s.cfg.PageSize) >= total {
			return nil
		}
		page++
	}
}

// probeOne dispatches to the right upstream API based on platform, then
// records the resulting snapshot.
func (s *CodingPlanQuotaProbeService) probeOne(ctx context.Context, account *biz.SubscriptionAccount) error {
	apiKey := strings.TrimSpace(account.AccessToken)
	if apiKey == "" {
		return nil // nothing to query with; skip silently
	}

	var (
		snapshot *biz.AccountQuotaSnapshot
		err      error
	)
	switch account.Platform {
	case codingPlanProbePlatformZhipu:
		snapshot, err = s.queryZhipu(ctx, account, apiKey)
	case codingPlanProbePlatformMinimax:
		snapshot, err = s.queryMinimax(ctx, account, apiKey)
	case codingPlanProbePlatformKimi:
		snapshot, err = s.queryKimi(ctx, account, apiKey)
	default:
		return nil
	}
	if err != nil {
		return err
	}
	if snapshot == nil {
		return nil
	}
	snapshot.AccountID = account.ID
	snapshot.UpdatedAt = s.now()
	return s.repo.RecordAccountQuotaSnapshot(ctx, snapshot)
}

// ─── Zhipu GLM ─────────────────────────────────────────────────────────────

// queryZhipu calls GET {base}/api/monitor/usage/quota/limit with the bare
// API key (no Bearer prefix — a quirk of the Zhipu API). The base is derived
// from the account's BaseURL so it works for both bigmodel.cn and z.ai.
func (s *CodingPlanQuotaProbeService) queryZhipu(ctx context.Context, account *biz.SubscriptionAccount, apiKey string) (*biz.AccountQuotaSnapshot, error) {
	base := zhipuQuotaBase(account.BaseURL)
	url := base + "/api/monitor/usage/quota/limit"
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, fmt.Errorf("zhipu request: %w", err)
	}
	req.Header.Set("Authorization", apiKey) // no Bearer
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept-Language", "en-US,en")

	resp, err := s.client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("zhipu request: %w", err)
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("zhipu read body: %w", err)
	}
	if resp.StatusCode == http.StatusUnauthorized || resp.StatusCode == http.StatusForbidden {
		return nil, fmt.Errorf("zhipu auth failed (HTTP %d)", resp.StatusCode)
	}
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("zhipu HTTP %d: %s", resp.StatusCode, truncateBody(body))
	}

	var root map[string]any
	if err := sonic.Unmarshal(body, &root); err != nil {
		// fall back to encoding/json (sonic should be a drop-in, but be safe)
		if err := json.Unmarshal(body, &root); err != nil {
			return nil, fmt.Errorf("zhipu parse: %w", err)
		}
	}
	if success, _ := root["success"].(bool); !success {
		msg, _ := root["msg"].(string)
		return nil, fmt.Errorf("zhipu api error: %s", msg)
	}
	data, _ := root["data"].(map[string]any)
	if data == nil {
		return nil, fmt.Errorf("zhipu: missing data field")
	}

	return parseZhipuTiers(account.ID, data, s.now()), nil
}

func zhipuQuotaBase(accountBaseURL string) string {
	lower := strings.ToLower(accountBaseURL)
	if strings.Contains(lower, "bigmodel.cn") {
		return "https://open.bigmodel.cn"
	}
	if strings.Contains(lower, "api.z.ai") {
		return "https://api.z.ai"
	}
	// Fall back to the account's own base (strip the path) — the quota endpoint
	// lives on the same host as the coding endpoint.
	if idx := strings.Index(accountBaseURL, "://"); idx >= 0 {
		host := accountBaseURL[idx+3:]
		if slash := strings.Index(host, "/"); slash >= 0 {
			host = host[:slash]
		}
		return "https://" + host
	}
	return "https://open.bigmodel.cn"
}

// parseZhipuTiers mirrors cc-switch's parse_zhipu_token_tiers: classify by
// the `unit` field (unit=3 → 5h, unit=6 → weekly), with a reset-time
// heuristic fallback for entries missing the unit.
func parseZhipuTiers(accountID int64, data map[string]any, now time.Time) *biz.AccountQuotaSnapshot {
	var fiveHour, weekly *zhipuEntry
	var unclassified []zhipuEntry

	limits, _ := data["limits"].([]any)
	for _, raw := range limits {
		item, ok := raw.(map[string]any)
		if !ok {
			continue
		}
		limitType, _ := item["type"].(string)
		if !strings.EqualFold(limitType, "TOKENS_LIMIT") {
			continue
		}
		pct, _ := toFloat64(item["percentage"])
		var resetMs *int64
		if ms, ok := toInt64(item["nextResetTime"]); ok && ms != nil {
			resetMs = ms
		}
		var resetIso *string
		if resetMs != nil {
			iso := millisToISO8601(*resetMs)
			resetIso = &iso
		}
		e := zhipuEntry{resetMs: resetMs, percentage: pct, resetIso: resetIso}

		unit, hasUnit := item["unit"]
		unitInt, _ := toInt64Val(unit)
		switch {
		case hasUnit && unitInt == 3: // 5h
			if fiveHour == nil {
				fiveHour = &e
			}
		case hasUnit && unitInt == 6: // weekly
			if weekly == nil {
				weekly = &e
			}
		default:
			unclassified = append(unclassified, e)
		}
	}

	// Fallback heuristic for entries without an explicit unit field.
	// Sort by reset time ascending; entries without reset go first (they
	// are more likely to be the 5h window at 0% state).
	sortByResetAsc(unclassified)
	for _, e := range unclassified {
		if fiveHour == nil {
			fiveHour = &e
		} else if weekly == nil {
			weekly = &e
		}
	}

	snap := &biz.AccountQuotaSnapshot{AccountID: accountID, UpdatedAt: now}
	if fiveHour != nil {
		v := fiveHour.percentage
		snap.PrimaryUsedPercent = &v
		wm := int32(codingPlanProbeFiveHourMinutes)
		snap.PrimaryWindowMinutes = &wm
		snap.PrimaryResetAfterSeconds = resetAfterFromISO(fiveHour.resetIso, now)
	}
	if weekly != nil {
		v := weekly.percentage
		snap.SecondaryUsedPercent = &v
		wm := int32(codingPlanProbeWeeklyMinutes)
		snap.SecondaryWindowMinutes = &wm
		snap.SecondaryResetAfterSeconds = resetAfterFromISO(weekly.resetIso, now)
	}
	return snap
}

// ─── Kimi For Coding ───────────────────────────────────────────────────────

// queryKimi calls GET https://api.kimi.com/coding/v1/usages. The Kimi account
// stores an OAuth access token in AccessToken; we use it as a Bearer token.
func (s *CodingPlanQuotaProbeService) queryKimi(ctx context.Context, account *biz.SubscriptionAccount, apiKey string) (*biz.AccountQuotaSnapshot, error) {
	const url = "https://api.kimi.com/coding/v1/usages"
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, fmt.Errorf("kimi request: %w", err)
	}
	req.Header.Set("Authorization", "Bearer "+apiKey)
	req.Header.Set("Accept", "application/json")

	resp, err := s.client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("kimi request: %w", err)
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("kimi read body: %w", err)
	}
	if resp.StatusCode == http.StatusUnauthorized || resp.StatusCode == http.StatusForbidden {
		return nil, fmt.Errorf("kimi auth failed (HTTP %d)", resp.StatusCode)
	}
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("kimi HTTP %d: %s", resp.StatusCode, truncateBody(body))
	}

	var root map[string]any
	if err := sonic.Unmarshal(body, &root); err != nil {
		if err := json.Unmarshal(body, &root); err != nil {
			return nil, fmt.Errorf("kimi parse: %w", err)
		}
	}

	snap := &biz.AccountQuotaSnapshot{AccountID: account.ID, UpdatedAt: s.now()}

	// 5-hour window: limits[].detail{limit, remaining, resetTime}
	if limits, _ := root["limits"].([]any); len(limits) > 0 {
		for _, raw := range limits {
			item, ok := raw.(map[string]any)
			if !ok {
				continue
			}
			detail, _ := item["detail"].(map[string]any)
			if detail == nil {
				continue
			}
			limit, _ := toFloat64(detail["limit"])
			remaining, _ := toFloat64(detail["remaining"])
			used := limit - remaining
			if used < 0 {
				used = 0
			}
			utilization := 0.0
			if limit > 0 {
				utilization = (used / limit) * 100.0
			}
			v := utilization
			snap.PrimaryUsedPercent = &v
			wm := int32(codingPlanProbeFiveHourMinutes)
			snap.PrimaryWindowMinutes = &wm
			if resetISO, ok := toString(detail["resetTime"]); ok {
				snap.PrimaryResetAfterSeconds = resetAfterFromISO(&resetISO, s.now())
			}
			break
		}
	}

	// Weekly window: top-level usage{limit, remaining, resetTime}
	if usage, ok := root["usage"].(map[string]any); ok {
		limit, _ := toFloat64(usage["limit"])
		remaining, _ := toFloat64(usage["remaining"])
		used := limit - remaining
		if used < 0 {
			used = 0
		}
		utilization := 0.0
		if limit > 0 {
			utilization = (used / limit) * 100.0
		}
		v := utilization
		snap.SecondaryUsedPercent = &v
		wm := int32(codingPlanProbeWeeklyMinutes)
		snap.SecondaryWindowMinutes = &wm
		if resetISO, ok := toString(usage["resetTime"]); ok {
			snap.SecondaryResetAfterSeconds = resetAfterFromISO(&resetISO, s.now())
		}
	}

	return snap, nil
}

// ─── MiniMax ───────────────────────────────────────────────────────────────

// queryMinimax calls GET https://{api.minimaxi.com|api.minimax.io}/v1/api/
// openplatform/coding_plan/remains. The domain is derived from the account's
// BaseURL (api.minimaxi.com for CN, api.minimax.io for EN).
func (s *CodingPlanQuotaProbeService) queryMinimax(ctx context.Context, account *biz.SubscriptionAccount, apiKey string) (*biz.AccountQuotaSnapshot, error) {
	domain := minimaxDomain(account.BaseURL)
	url := fmt.Sprintf("https://%s/v1/api/openplatform/coding_plan/remains", domain)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, fmt.Errorf("minimax request: %w", err)
	}
	req.Header.Set("Authorization", "Bearer "+apiKey)
	req.Header.Set("Content-Type", "application/json")

	resp, err := s.client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("minimax request: %w", err)
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("minimax read body: %w", err)
	}
	if resp.StatusCode == http.StatusUnauthorized || resp.StatusCode == http.StatusForbidden {
		return nil, fmt.Errorf("minimax auth failed (HTTP %d)", resp.StatusCode)
	}
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("minimax HTTP %d: %s", resp.StatusCode, truncateBody(body))
	}

	var root map[string]any
	if err := sonic.Unmarshal(body, &root); err != nil {
		if err := json.Unmarshal(body, &root); err != nil {
			return nil, fmt.Errorf("minimax parse: %w", err)
		}
	}

	// Business-level error envelope (base_resp.status_code != 0)
	if baseResp, ok := root["base_resp"].(map[string]any); ok {
		statusCode, _ := toInt64Val(baseResp["status_code"])
		if statusCode != 0 {
			msg, _ := toString(baseResp["status_msg"])
			return nil, fmt.Errorf("minimax api error (code %d): %s", statusCode, msg)
		}
	}

	return parseMinimaxTiers(account.ID, root, s.now()), nil
}

func minimaxDomain(accountBaseURL string) string {
	lower := strings.ToLower(accountBaseURL)
	if strings.Contains(lower, "api.minimax.io") {
		return "api.minimax.io"
	}
	return "api.minimaxi.com" // default to CN
}

// parseMinimaxTiers mirrors cc-switch's parse_minimax_tiers: take the
// model_remains[] entry whose model_name == "general" (skip video etc),
// then use current_interval_remaining_percent (5h) and — only when
// current_weekly_status == 1 — current_weekly_remaining_percent (weekly).
// The API returns *remaining* percent; we invert to used percent.
func parseMinimaxTiers(accountID int64, root map[string]any, now time.Time) *biz.AccountQuotaSnapshot {
	snap := &biz.AccountQuotaSnapshot{AccountID: accountID, UpdatedAt: now}

	modelRemains, _ := root["model_remains"].([]any)
	for _, raw := range modelRemains {
		item, ok := raw.(map[string]any)
		if !ok {
			continue
		}
		name, _ := toString(item["model_name"])
		if name != "general" {
			continue
		}

		// 5h window
		if remainPct, ok := toFloat64(item["current_interval_remaining_percent"]); ok {
			used := 100.0 - remainPct
			v := used
			snap.PrimaryUsedPercent = &v
			wm := int32(codingPlanProbeFiveHourMinutes)
			snap.PrimaryWindowMinutes = &wm
			if endMs, ok := toInt64(item["end_time"]); ok && endMs != nil {
				iso := millisToISO8601(*endMs)
				snap.PrimaryResetAfterSeconds = resetAfterFromISO(&iso, now)
			}
		}

		// Weekly window — only when status == 1 (active).
		if status, _ := toInt64Val(item["current_weekly_status"]); status == 1 {
			if remainPct, ok := toFloat64(item["current_weekly_remaining_percent"]); ok {
				used := 100.0 - remainPct
				v := used
				snap.SecondaryUsedPercent = &v
				wm := int32(codingPlanProbeWeeklyMinutes)
				snap.SecondaryWindowMinutes = &wm
				if endMs, ok := toInt64(item["weekly_end_time"]); ok && endMs != nil {
					iso := millisToISO8601(*endMs)
					snap.SecondaryResetAfterSeconds = resetAfterFromISO(&iso, now)
				}
			}
		}
		break
	}

	return snap
}

// ─── helpers ───────────────────────────────────────────────────────────────

func millisToISO8601(ms int64) string {
	secs := ms / 1000
	nsecs := ((ms % 1000) * 1_000_000)
	t := time.Unix(secs, nsecs).UTC()
	return t.Format(time.RFC3339)
}

// resetAfterFromISO converts an ISO-8601 reset timestamp into the
// "reset_after_seconds" field expected by AccountQuotaSnapshot, relative to
// `now`. Returns nil for missing/invalid/already-passed resets.
func resetAfterFromISO(iso *string, now time.Time) *int32 {
	if iso == nil || *iso == "" {
		return nil
	}
	t, err := time.Parse(time.RFC3339, *iso)
	if err != nil {
		return nil
	}
	delta := int32(t.Unix() - now.Unix())
	if delta <= 0 {
		return nil
	}
	return &delta
}

func sortByResetAsc(entries []zhipuEntry) {
	// stable sort: entries without reset first, then ascending by resetMs.
	// (mirrors cc-switch's sort_by_key logic)
	for i := 1; i < len(entries); i++ {
		for j := i; j > 0; j-- {
			a, b := entries[j-1], entries[j]
			aHas := a.resetMs != nil
			bHas := b.resetMs != nil
			if aHas && !bHas {
				break // a has reset, b doesn't → a goes after; already in order
			}
			if !aHas && bHas {
				// a has no reset, b does → swap so a goes first
				entries[j-1], entries[j] = entries[j], entries[j-1]
				continue
			}
			if aHas && bHas && *a.resetMs <= *b.resetMs {
				break
			}
			entries[j-1], entries[j] = entries[j], entries[j-1]
		}
	}
}

// zhipuEntry is the entry type for the unclassified bucket in
// parseZhipuTiers. Kept package-private so it does not leak.
type zhipuEntry struct {
	resetMs    *int64
	percentage float64
	resetIso   *string
}

// toFloat64 accepts either a number or a numeric string and returns the value.
func toFloat64(v any) (float64, bool) {
	switch t := v.(type) {
	case float64:
		return t, true
	case float32:
		return float64(t), true
	case int:
		return float64(t), true
	case int64:
		return float64(t), true
	case json.Number:
		f, err := t.Float64()
		return f, err == nil
	case string:
		f, err := strconv.ParseFloat(t, 64)
		return f, err == nil
	}
	return 0, false
}

// toInt64 extracts an int64 from a JSON value, handling the number/string
// ambiguity some upstream APIs use.
func toInt64(v any) (*int64, bool) {
	switch t := v.(type) {
	case float64:
		i := int64(t)
		return &i, true
	case int:
		i := int64(t)
		return &i, true
	case int64:
		return &t, true
	case json.Number:
		i, err := t.Int64()
		if err != nil {
			f, err := t.Float64()
			if err != nil {
				return nil, false
			}
			i := int64(f)
			return &i, true
		}
		return &i, true
	case string:
		i, err := strconv.ParseInt(t, 10, 64)
		if err != nil {
			return nil, false
		}
		return &i, true
	}
	return nil, false
}

// toInt64Val is like toInt64 but returns by value (for switch expressions).
func toInt64Val(v any) (int64, bool) {
	p, ok := toInt64(v)
	if !ok || p == nil {
		return 0, false
	}
	return *p, true
}

func toString(v any) (string, bool) {
	if s, ok := v.(string); ok {
		return s, true
	}
	return "", false
}

func truncateBody(body []byte) string {
	const max = 200
	if len(body) <= max {
		return string(body)
	}
	return string(body[:max]) + "..."
}

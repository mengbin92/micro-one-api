package server

import (
	"fmt"
	"net/http"
	"os"
	"strconv"
	"strings"
	"time"

	channelv1 "micro-one-api/api/channel/v1"
	"micro-one-api/app/admin/internal/service"
)

// ── v0.11.0 Phase 3 §3.6: routing operations view ──────────────────────────
//
// GET /api/admin/routing-ops returns a single-call operations snapshot:
//   - traffic + cost split by source kind (channel vs subscription), from the
//     billing ledger AggregateUsage RPC
//   - fallback / error rates from relay-gateway metrics queried through
//     Prometheus for the same time window
//   - cache read/creation tokens
//   - gross profit (revenue − upstream cost)
//   - unpriced routed model count (Phase 2 §2.2)
//
// The time window defaults to the last 24h and is overridable via the
// start/end query params (unix seconds).

// routingOpsView is the /api/admin/routing-ops response body.
type routingOpsView struct {
	Success   bool               `json:"success"`
	Partial   bool               `json:"partial,omitempty"`
	Errors    []string           `json:"errors,omitempty"`
	Window    routingOpsWindow   `json:"window"`
	Sources   []routingOpsSource `json:"sources"`
	Truncated bool               `json:"truncated,omitempty"`
	Totals    routingOpsTotals   `json:"totals"`
	Rates     routingOpsRates    `json:"rates"`
	Unpriced  routingOpsUnpriced `json:"unpriced"`
	Alerts    []routingOpsAlert  `json:"alerts"`
}

// routingOpsRates carries routing outcomes for the same window as the billing
// aggregates. Values come from relay-gateway counters queried through
// Prometheus, not from admin-api's unrelated local registry.
type routingOpsRates struct {
	SelectionTotal   float64 `json:"selection_total"`
	SuccessTotal     float64 `json:"success_total"`
	ErrorTotal       float64 `json:"error_total"`
	ClientErrorTotal float64 `json:"client_error_total"`
	FallbackTotal    float64 `json:"fallback_total"`
	ErrorRate        float64 `json:"error_rate"`
	FallbackRate     float64 `json:"fallback_rate"`
	// Source identifies where the rates came from: "prometheus" (precise
	// window increment), "relay_scrape" (cumulative counters from
	// relay-gateway /metrics, degraded fallback), or "" when unavailable.
	Source string `json:"source,omitempty"`
	// Cumulative is true when the values are process-lifetime totals rather
	// than window-scoped increments (i.e. source="relay_scrape").
	Cumulative bool `json:"cumulative,omitempty"`
}

type routingOpsWindow struct {
	Start int64 `json:"start"` // unix seconds
	End   int64 `json:"end"`   // unix seconds
}

// routingOpsSource is one row of the source-kind breakdown. SourceKind is
// "channel" or "subscription"; channel_id > 0 marks a regular channel,
// subscription_account_id > 0 marks a subscription account.
type routingOpsSource struct {
	SourceKind            string `json:"source_kind"`
	SourceID              int64  `json:"source_id"`
	Quota                 int64  `json:"quota"`
	UpstreamCost          int64  `json:"upstream_cost"`
	GrossProfit           int64  `json:"gross_profit"`
	PromptTokens          int64  `json:"prompt_tokens"`
	CompletionTokens      int64  `json:"completion_tokens"`
	CacheReadTokens       int64  `json:"cache_read_tokens"`
	CacheCreation5mTokens int64  `json:"cache_creation_5m_tokens"`
	CacheCreation1hTokens int64  `json:"cache_creation_1h_tokens"`
	Count                 int64  `json:"count"`
}

type routingOpsTotals struct {
	Quota                 int64 `json:"quota"`
	UpstreamCost          int64 `json:"upstream_cost"`
	GrossProfit           int64 `json:"gross_profit"`
	PromptTokens          int64 `json:"prompt_tokens"`
	CompletionTokens      int64 `json:"completion_tokens"`
	CacheReadTokens       int64 `json:"cache_read_tokens"`
	CacheCreation5mTokens int64 `json:"cache_creation_5m_tokens"`
	CacheCreation1hTokens int64 `json:"cache_creation_1h_tokens"`
	Count                 int64 `json:"count"`
	// ChannelCount and SubscriptionCount are the GLOBAL request counts split by
	// source kind (not affected by the Top-N bucket truncation). They are
	// sourced from a separate billing aggregation grouped by source kind only,
	// so the channel/subscription traffic ratio is accurate even when there are
	// more than 200 individual sources (code review MEDIUM-2).
	ChannelCount      int64 `json:"channel_count"`
	SubscriptionCount int64 `json:"subscription_count"`
}

type routingOpsUnpriced struct {
	RoutedButUnpriced int32 `json:"routed_but_unpriced"`
}

// routingOpsAlert is one Phase 3 §3.7 alert condition detected in the current
// window. Severity is "warning" or "critical"; Detail carries model/source
// ids so ops can act without a second query.
type routingOpsAlert struct {
	Kind     string `json:"kind"`
	Severity string `json:"severity"`
	Message  string `json:"message"`
	Detail   string `json:"detail"`
}

// handleRoutingOps serves GET /api/admin/routing-ops.
func handleRoutingOps(w http.ResponseWriter, r *http.Request, svc *service.AdminService) {
	if r.Method != http.MethodGet {
		writeJSON(w, http.StatusMethodNotAllowed, map[string]string{"error": "method not allowed"})
		return
	}
	if svc == nil {
		writeJSON(w, http.StatusOK, routingOpsView{Success: true})
		return
	}

	now := time.Now().Unix()
	start, err := parseUnixQS(r, "start", now-24*60*60) // default: last 24h
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	}
	end, err := parseUnixQS(r, "end", now)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	}
	if end <= start {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "end must be greater than start"})
		return
	}
	const maxRoutingOpsWindow = 31 * 24 * 60 * 60
	if end-start > maxRoutingOpsWindow {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "routing operations window cannot exceed 31 days"})
		return
	}

	// Aggregate by channel first (regular channels have channel_id > 0 and
	// subscription_account_id = 0; subscription accounts have
	// subscription_account_id > 0). The billing RPC returns both the bucket
	// list (capped at Top-N) AND the global totals (unaffected by the cap).
	// We use the billing totals directly so the ops view never under-reports
	// when there are more than N sources (code review #7).
	view := routingOpsView{
		Success: true,
		Window:  routingOpsWindow{Start: start, End: end},
	}
	channelBuckets, billingTotals, err := svc.AggregateUsageGroupedByChannel(r.Context(), start, end)
	if err != nil {
		// Surface the billing dependency failure instead of silently returning
		// zero-data success (code review #7). The view is still returned so
		// the frontend can render the error banner, but success=false + the
		// error message makes the failure observable.
		view.Success = false
		view.Partial = true
		view.Errors = append(view.Errors, "billing aggregation failed: "+err.Error())
	} else {
		for _, b := range channelBuckets {
			kind := "channel"
			if b.SubscriptionAccountID > 0 {
				kind = "subscription"
			}
			view.Sources = append(view.Sources, routingOpsSource{
				SourceKind:            kind,
				SourceID:              sourceIDOf(b),
				Quota:                 b.Quota,
				UpstreamCost:          b.UpstreamCost,
				GrossProfit:           b.GrossProfit,
				PromptTokens:          b.PromptTokens,
				CompletionTokens:      b.CompletionTokens,
				CacheReadTokens:       b.CacheReadTokens,
				CacheCreation5mTokens: b.CacheCreation5mTokens,
				CacheCreation1hTokens: b.CacheCreation1hTokens,
				Count:                 b.Count,
			})
		}
		// Mark truncation when the bucket count hits the limit.
		if len(channelBuckets) >= 200 {
			view.Truncated = true
		}
		// Use the billing-level global totals (not the re-summed bucket list).
		view.Totals.Quota = billingTotals.Quota
		view.Totals.UpstreamCost = billingTotals.UpstreamCost
		view.Totals.GrossProfit = billingTotals.GrossProfit
		view.Totals.PromptTokens = billingTotals.PromptTokens
		view.Totals.CompletionTokens = billingTotals.CompletionTokens
		view.Totals.CacheReadTokens = billingTotals.CacheReadTokens
		view.Totals.CacheCreation5mTokens = billingTotals.CacheCreation5mTokens
		view.Totals.CacheCreation1hTokens = billingTotals.CacheCreation1hTokens
		view.Totals.Count = billingTotals.Count
		// Channel-vs-subscription split: derive from the bucket list as a
		// best-effort approximate when not truncated. When truncated, the
		// frontend displays the ratio with a note that it is approximate.
		// The global totals (Quota/Count/etc.) are always accurate because
		// they come from billingTotals.
		if !view.Truncated {
			for _, s := range view.Sources {
				if s.SourceKind == "channel" {
					view.Totals.ChannelCount += s.Count
				} else {
					view.Totals.SubscriptionCount += s.Count
				}
			}
		} else {
			// When truncated, mark counts as approximate by using -1 sentinel
			// so the frontend can display "N/A (truncated)".
			view.Totals.ChannelCount = -1
			view.Totals.SubscriptionCount = -1
		}
	}
	// Unpriced routed model count (Phase 2 §2.2) so the ops view surfaces the
	// pricing gap alongside traffic.
	var unpricedResp *channelv1.ListUnpricedRoutedModelsResponse
	if resp, err := svc.ListUnpricedRoutedModelsWithPricing(r.Context()); err == nil && resp != nil {
		view.Unpriced.RoutedButUnpriced = resp.GetTotal()
		unpricedResp = resp
	} else if err != nil {
		// Surface the channel-service dependency failure (code review #7).
		view.Partial = true
		view.Errors = append(view.Errors, "unpriced model query failed: "+err.Error())
	}
	// v0.11.0 review M5: keep the gauge fresh when the ops page is loaded.
	service.RecordUnpricedRoutedMetric(unpricedResp)

	prometheusURL := strings.TrimSpace(os.Getenv("PROMETHEUS_URL"))
	relayMetricsURL := strings.TrimSpace(os.Getenv("RELAY_METRICS_ENDPOINT"))

	rates, rateErrors := loadRoutingRates(r.Context(), prometheusURL, relayMetricsURL, start, end)
	view.Rates = rates
	if rates.Source == "" {
		// Both data sources failed (or were unconfigured).
		view.Partial = true
		view.Errors = append(view.Errors, rateErrors...)
	} else if len(rateErrors) > 0 {
		// The preferred source failed but the fallback succeeded. Keep the
		// non-fatal failure message so ops knows Prometheus is down.
		view.Errors = append(view.Errors, rateErrors...)
	}

	// Compute alerts only after every data source has populated the view. In
	// particular, error/fallback alerts depend on the Prometheus rates above.
	view.Alerts = computeRoutingOpsAlerts(view, unpricedResp)

	writeJSON(w, http.StatusOK, view)
}

func sourceIDOf(b service.UsageAggregateView) int64 {
	if b.SubscriptionAccountID > 0 {
		return b.SubscriptionAccountID
	}
	return b.ChannelID
}

// parseUnixQS parses a unix-seconds query parameter with a default.
func parseUnixQS(r *http.Request, key string, def int64) (int64, error) {
	v := strings.TrimSpace(r.URL.Query().Get(key))
	if v == "" {
		return def, nil
	}
	var n int64
	for _, c := range v {
		if c < '0' || c > '9' {
			return 0, fmt.Errorf("%s must be a positive unix timestamp", key)
		}
		if n > (1<<63-1-int64(c-'0'))/10 {
			return 0, fmt.Errorf("%s is out of range", key)
		}
		n = n*10 + int64(c-'0')
	}
	if n <= 0 {
		return 0, fmt.Errorf("%s must be a positive unix timestamp", key)
	}
	return n, nil
}

// computeRoutingOpsAlerts evaluates the Phase 3 §3.7 alert conditions against
// the routing-ops snapshot. It is pure: it reads the already-loaded view and
// the unpriced-model response, and returns the list of active alerts. Each
// condition maps to one roadmap requirement:
//   - unpriced_traffic: routed models with traffic but no user-facing price
//   - upstream_cost_missing: sources with revenue but no upstream cost
//   - source_skew: a single source dominates traffic far beyond its weight
//   - negative_margin: gross profit is negative (cost > revenue)
func computeRoutingOpsAlerts(view routingOpsView, unpriced *channelv1.ListUnpricedRoutedModelsResponse) []routingOpsAlert {
	var alerts []routingOpsAlert

	// 1. Unpriced models with active traffic.
	if unpriced != nil && unpriced.GetTotal() > 0 && view.Totals.Count > 0 {
		ids := make([]string, 0, len(unpriced.GetModels()))
		for _, m := range unpriced.GetModels() {
			ids = append(ids, m.GetModelId())
		}
		alerts = append(alerts, routingOpsAlert{
			Kind:     "unpriced_traffic",
			Severity: "warning",
			Message:  "有流量的模型缺少用户售价配置",
			Detail:   strings.Join(ids, ", "),
		})
	}

	// 2. Upstream cost missing: when total revenue > 0 but total upstream
	// cost == 0, the cost key is missing. This uses the GLOBAL totals (not
	// the truncated bucket list) so the alert fires even when the missing
	// sources are beyond the Top-N window (code review MEDIUM-2).
	if view.Totals.Quota > 0 && view.Totals.UpstreamCost == 0 {
		alerts = append(alerts, routingOpsAlert{
			Kind:     "upstream_cost_missing",
			Severity: "warning",
			Message:  "有收入但上游成本为零",
			Detail:   "total quota = " + strconv.FormatInt(view.Totals.Quota, 10) + ", upstream_cost = 0",
		})
	}

	// 3. Source skew: a single source handles >80% of traffic when there are
	//    2+ active sources. Only checked when NOT truncated so the percentage
	//    is accurate (code review MEDIUM-2).
	if !view.Truncated && len(view.Sources) >= 2 && view.Totals.Count > 0 {
		for _, src := range view.Sources {
			share := float64(src.Count) / float64(view.Totals.Count)
			if share > 0.80 {
				alerts = append(alerts, routingOpsAlert{
					Kind:     "source_skew",
					Severity: "warning",
					Message:  "单一来源流量占比过高，可能偏离配置权重",
					Detail:   src.SourceKind + " #" + strconv.FormatInt(src.SourceID, 10) + " = " + strconv.FormatFloat(share*100, 'f', 1, 64) + "%",
				})
				break
			}
		}
	}

	// 4. Negative margin: gross profit is negative (cost exceeds revenue).
	if view.Totals.GrossProfit < 0 {
		alerts = append(alerts, routingOpsAlert{
			Kind:     "negative_margin",
			Severity: "critical",
			Message:  "毛利为负：上游成本超过用户收入",
			Detail:   "gross_profit = " + strconv.FormatInt(view.Totals.GrossProfit, 10),
		})
	}

	return alerts
}

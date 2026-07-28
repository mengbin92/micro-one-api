package server

import (
	"net/http"
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
//   - fallback / error rates (from Prometheus metrics; the frontend reads
//     /metrics directly for live dashboards, but this endpoint surfaces the
//     ledger-backed absolute numbers so the view is useful without a scraper)
//   - cache read/creation tokens
//   - gross profit (revenue − upstream cost)
//   - unpriced routed model count (Phase 2 §2.2)
//
// The time window defaults to the last 24h and is overridable via the
// start/end query params (unix seconds).

// routingOpsView is the /api/admin/routing-ops response body.
type routingOpsView struct {
	Success bool              `json:"success"`
	Window  routingOpsWindow  `json:"window"`
	Sources []routingOpsSource `json:"sources"`
	Totals  routingOpsTotals  `json:"totals"`
	Unpriced routingOpsUnpriced `json:"unpriced"`
	Alerts  []routingOpsAlert `json:"alerts"`
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
	ChannelCount          int64 `json:"channel_count"`
	SubscriptionCount     int64 `json:"subscription_count"`
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
	start := parseUnixQS(r, "start", now-24*60*60) // default: last 24h
	end := parseUnixQS(r, "end", now)

	// Aggregate by channel first (regular channels have channel_id > 0 and
	// subscription_account_id = 0; subscription accounts have
	// subscription_account_id > 0). We issue two AggregateUsage calls and
	// split the buckets by which id column is set.
	view := routingOpsView{
		Success: true,
		Window:  routingOpsWindow{Start: start, End: end},
	}
	channelBuckets, err := svc.AggregateUsageGroupedByChannel(r.Context(), start, end)
	if err == nil {
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
	}
	// Totals across all sources.
	for _, s := range view.Sources {
		view.Totals.Quota += s.Quota
		view.Totals.UpstreamCost += s.UpstreamCost
		view.Totals.GrossProfit += s.GrossProfit
		view.Totals.PromptTokens += s.PromptTokens
		view.Totals.CompletionTokens += s.CompletionTokens
		view.Totals.CacheReadTokens += s.CacheReadTokens
		view.Totals.CacheCreation5mTokens += s.CacheCreation5mTokens
		view.Totals.CacheCreation1hTokens += s.CacheCreation1hTokens
		view.Totals.Count += s.Count
		if s.SourceKind == "channel" {
			view.Totals.ChannelCount += s.Count
		} else {
			view.Totals.SubscriptionCount += s.Count
		}
	}
	// Unpriced routed model count (Phase 2 §2.2) so the ops view surfaces the
	// pricing gap alongside traffic.
	priced := loadPricedModelSet(r.Context(), svc)
	var unpricedResp *channelv1.ListUnpricedRoutedModelsResponse
	if resp, err := svc.ListUnpricedRoutedModels(r.Context(), &channelv1.ListUnpricedRoutedModelsRequest{
		PricedModelIds: priced,
	}); err == nil && resp != nil {
		view.Unpriced.RoutedButUnpriced = resp.GetTotal()
		unpricedResp = resp
	}

	// v0.11.0 Phase 3 §3.7: compute routing alert conditions from the data
	// already loaded for this view. These are threshold-based observation
	// alerts surfaced in-line (no separate delivery path); the frontend
	// renders them as a warning banner.
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
func parseUnixQS(r *http.Request, key string, def int64) int64 {
	v := strings.TrimSpace(r.URL.Query().Get(key))
	if v == "" {
		return def
	}
	var n int64
	for _, c := range v {
		if c < '0' || c > '9' {
			return def
		}
		n = n*10 + int64(c-'0')
	}
	if n <= 0 {
		return def
	}
	return n
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

	// 2. Sources with revenue but zero upstream cost (cost key missing).
	totalSources := len(view.Sources)
	costMissingSources := 0
	for _, src := range view.Sources {
		if src.Quota > 0 && src.UpstreamCost == 0 {
			costMissingSources++
		}
	}
	if costMissingSources > 0 {
		alerts = append(alerts, routingOpsAlert{
			Kind:     "upstream_cost_missing",
			Severity: "warning",
			Message:  "有收入的来源缺少上游成本配置",
			Detail:   strconv.Itoa(costMissingSources) + "/" + strconv.Itoa(totalSources) + " sources",
		})
	}

	// 3. Source skew: a single source handles >80% of traffic when there are
	//    2+ active sources. This flags a weight/priority misconfiguration
	//    rather than a hard error.
	if totalSources >= 2 && view.Totals.Count > 0 {
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

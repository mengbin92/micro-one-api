package server

import (
	"net/http"
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
	if resp, err := svc.ListUnpricedRoutedModels(r.Context(), &channelv1.ListUnpricedRoutedModelsRequest{
		PricedModelIds: priced,
	}); err == nil && resp != nil {
		view.Unpriced.RoutedButUnpriced = resp.GetTotal()
	}
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

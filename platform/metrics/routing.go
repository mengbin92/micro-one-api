package metrics

import "github.com/prometheus/client_golang/prometheus"

// ── v0.11.0 Phase 3 §3.5: routing observability metrics ────────────────────
//
// All labels here are deliberately low-cardinality (roadmap §3.5):
// source_kind ∈ {channel, subscription}, result ∈ {success, error,
// client_error}, reason ∈ {empty, no_channel, no_subscription,
// all_circuit_open, upstream_5xx, timeout, circuit_open, quota, ...},
// provider_family ∈ {openai, anthropic, google, zhipu, deepseek, alibaba,
// other}. Channel/account/model identifiers stay in the structured
// SelectionEvent / log, never in Prometheus labels.

// RoutingSelectionTotal counts source-selection outcomes at the Plan()
// boundary. Use source_kind + result to compute traffic share by source and
// the channel-vs-subscription ratio.
var RoutingSelectionTotal = prometheus.NewCounterVec(
	prometheus.CounterOpts{
		Namespace: "micro_one_api",
		Subsystem: "routing",
		Name:      "selection_total",
		Help:      "Source-selection outcomes at the Plan boundary (source_kind × result × provider_family)",
	},
	[]string{"source_kind", "result", "provider_family"},
)

// RoutingFallbackTotal counts requests that retried onto a different source
// after the first choice failed. reason carries the low-cardinality fallback
// cause so ops can distinguish upstream 5xx from timeout / circuit-open.
var RoutingFallbackTotal = prometheus.NewCounterVec(
	prometheus.CounterOpts{
		Namespace: "micro_one_api",
		Subsystem: "routing",
		Name:      "fallback_total",
		Help:      "Requests that fell back to a different source after the first choice failed",
	},
	[]string{"reason", "provider_family"},
)

// RoutingSelectionDuration records the wall-clock selection latency (the
// Plan() call, not the upstream execution). Used to confirm the Phase 3
// observability additions do not regress the hot path (roadmap §3.8).
var RoutingSelectionDuration = prometheus.NewHistogramVec(
	prometheus.HistogramOpts{
		Namespace: "micro_one_api",
		Subsystem: "routing",
		Name:      "selection_duration_seconds",
		Help:      "Source-selection latency at the Plan boundary",
		Buckets:   []float64{0.001, 0.005, 0.01, 0.025, 0.05, 0.1, 0.25, 0.5, 1},
	},
	[]string{"source_kind"},
)

// RoutingStickyHitTotal counts subscription-account sticky-binding hits so ops
// can see how often a conversation reuses the same upstream account.
var RoutingStickyHitTotal = prometheus.NewCounterVec(
	prometheus.CounterOpts{
		Namespace: "micro_one_api",
		Subsystem: "routing",
		Name:      "sticky_hit_total",
		Help:      "Subscription-account sticky-binding hits at the Plan boundary",
	},
	[]string{"provider_family"},
)

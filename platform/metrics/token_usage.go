package metrics

import "github.com/prometheus/client_golang/prometheus"

// TokenUsageParseAnomaly counts token-usage parsing anomalies observed by the
// relay/provider layers, as defined by docs/design/token-usage-semantics.md
// §4 (v0.11.0 ADR).
//
// The single label "reason" is deliberately low-cardinality; it never carries
// channel/account/model ids (ADR §1.1 / v0.11.0 roadmap require that high-
// cardinality identifiers stay in structured logs, not Prometheus labels).
//
// Reasons used by the Phase 1 parsing layer:
//   - "negative"            : a token bucket was negative and clamped to 0
//     (ADR §4.1).
//   - "ttl_detail_exceeds_total": cache_creation TTL detail sum exceeded the
//     flat cache_creation_input_tokens total; detail
//     wins and billing is unchanged (ADR §4.2).
//   - "stream_usage_missing": a streaming response carried no usage object the
//     parser could recognize (ADR §4.3).
//   - "overflow"            : a bucket exceeded int64 range (ADR §4.1).
var TokenUsageParseAnomaly = prometheus.NewCounterVec(
	prometheus.CounterOpts{
		Namespace: "micro_one_api",
		Subsystem: "relay",
		Name:      "token_usage_parse_anomaly_total",
		Help:      "Token usage parsing anomalies by reason (low-cardinality)",
	},
	[]string{"reason"},
)

// TokenUsageShadowCost records the cache-creation shadow cost (the delta
// charge mode would add on top of the v0.10.2 cost) per request, so ops can
// compare observe-mode traffic against vendor invoices before flipping
// BILLING_CACHE_CREATION_MODE to charge (v0.11.0 ADR §5 / roadmap §1.3).
//
// Labels are deliberately low-cardinality: the billing mode (observe|charge)
// and whether the request had unpriced cache-creation tokens. Model id and
// request id stay in structured logs.
var TokenUsageShadowCost = prometheus.NewHistogramVec(
	prometheus.HistogramOpts{
		Namespace: "micro_one_api",
		Subsystem: "relay",
		Name:      "token_usage_shadow_cost",
		Help:      "Cache-creation shadow cost (quota units) that charge mode would add",
		Buckets:   []float64{0, 1, 10, 100, 1000, 10000, 100000, 1000000},
	},
	[]string{"mode", "unpriced"},
)

// UnpricedRoutedModels is the v0.11.0 Phase 2 §2.2 gauge for "routed but
// unpriced" models: public, enabled models that have at least one active
// channel or subscription mapping but carry no entry in the user-facing
// ModelPrice config. Unpriced does NOT block routing, but the roadmap
// requires a visible status + metric so operators do not silently serve a
// model at zero cost.
//
// The single label "source" distinguishes the two route kinds (channel vs
// subscription) so ops can tell where the gap is. Model ids stay in the audit
// event / structured log, not in Prometheus labels (cardinality).
var UnpricedRoutedModels = prometheus.NewGaugeVec(
	prometheus.GaugeOpts{
		Namespace: "micro_one_api",
		Subsystem: "model",
		Name:      "unpriced_routed",
		Help:      "Public enabled routed models with no ModelPrice entry, by route source kind",
	},
	[]string{"source"},
)

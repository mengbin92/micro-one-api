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

// ---------------------------------------------------------------------------
// token-usage-billing-semantics-remediation (2026-08-31) §9 metrics.
// Labels stay low-cardinality: protocol / semantics / parse status / reason
// and source_kind only; model id appears only where the doc explicitly
// requires it (billing-side delta/ambiguous) and never request/user ids.
// ---------------------------------------------------------------------------

// TokenUsageSemanticsTotal counts parsed usage envelopes by proven protocol
// and semantics verdict (§9 token_usage_semantics_total). Emitted at the
// relay boundary where the execution source is known.
var TokenUsageSemanticsTotal = prometheus.NewCounterVec(
	prometheus.CounterOpts{
		Namespace: "micro_one_api",
		Subsystem: "relay",
		Name:      "token_usage_semantics_total",
		Help:      "Usage envelopes by parser-proven protocol and semantics",
	},
	[]string{"protocol", "semantics", "source_kind"},
)

// TokenUsageInvariantMismatchTotal counts usage invariant violations that did
// NOT change the bill on their own (§9 token_usage_invariant_mismatch_total).
// Reasons: cached_exceeds_reported_prompt, reported_total_mismatch,
// protocol_field_conflict, final_attempt_semantics_missing, negative_bucket,
// overflow, stream_usage_missing.
var TokenUsageInvariantMismatchTotal = prometheus.NewCounterVec(
	prometheus.CounterOpts{
		Namespace: "micro_one_api",
		Subsystem: "relay",
		Name:      "token_usage_invariant_mismatch_total",
		Help:      "Usage invariant violations by reason/protocol/source",
	},
	[]string{"reason", "protocol", "source_kind"},
)

// BillingUsageSemanticsCostDelta records canonical-final minus legacy-final
// user cost per request (§9 billing_usage_semantics_cost_delta), so the
// observe window can prove delta=0 before charge mode flips.
var BillingUsageSemanticsCostDelta = prometheus.NewHistogramVec(
	prometheus.HistogramOpts{
		Namespace: "micro_one_api",
		Subsystem: "billing",
		Name:      "usage_semantics_cost_delta",
		Help:      "Canonical vs legacy user cost delta in quota units",
		Buckets:   []float64{-100000, -10000, -1000, -100, -10, -1, 0, 1, 10, 100, 1000, 10000, 100000},
	},
	[]string{"mode", "model", "source_kind"},
)

// BillingUsageAmbiguousTotal counts requests settled via the §5.2 ambiguous
// conservative path (§9 billing_usage_ambiguous_total). Any sample is a
// high-priority alert: verified production traffic must keep this at zero.
var BillingUsageAmbiguousTotal = prometheus.NewCounterVec(
	prometheus.CounterOpts{
		Namespace: "micro_one_api",
		Subsystem: "billing",
		Name:      "usage_ambiguous_total",
		Help:      "Requests settled via the ambiguous conservative policy",
	},
	[]string{"model", "source_kind"},
)

// UsageSemanticSourceIsolationTotal counts source+model quarantine events
// (§9 usage_semantic_source_isolation_total): block applied, block observed
// while routing, and block resolved.
var UsageSemanticSourceIsolationTotal = prometheus.NewCounterVec(
	prometheus.CounterOpts{
		Namespace: "micro_one_api",
		Subsystem: "channel",
		Name:      "usage_semantic_source_isolation_total",
		Help:      "Usage-semantics source isolation events",
	},
	[]string{"source_kind", "reason"},
)

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
//                              (ADR §4.1).
//   - "ttl_detail_exceeds_total": cache_creation TTL detail sum exceeded the
//                              flat cache_creation_input_tokens total; detail
//                              wins and billing is unchanged (ADR §4.2).
//   - "stream_usage_missing": a streaming response carried no usage object the
//                              parser could recognize (ADR §4.3).
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

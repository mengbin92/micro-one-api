package metrics

import "github.com/prometheus/client_golang/prometheus"

// SubscriptionQuotaChecksTotal counts business subscription quota checks.
var SubscriptionQuotaChecksTotal = prometheus.NewCounterVec(
	prometheus.CounterOpts{
		Namespace: "micro_one_api",
		Subsystem: "subscription",
		Name:      "quota_checks_total",
		Help:      "Total number of business subscription quota checks",
	},
	[]string{"result"},
)

// SubscriptionUsageRecordsTotal counts business subscription usage writebacks.
var SubscriptionUsageRecordsTotal = prometheus.NewCounterVec(
	prometheus.CounterOpts{
		Namespace: "micro_one_api",
		Subsystem: "subscription",
		Name:      "usage_records_total",
		Help:      "Total number of business subscription usage writebacks",
	},
	[]string{"status"},
)

// RelaySubscriptionAdaptorRequestsTotal counts subscription-account adaptor calls.
var RelaySubscriptionAdaptorRequestsTotal = prometheus.NewCounterVec(
	prometheus.CounterOpts{
		Namespace: "micro_one_api",
		Subsystem: "relay",
		Name:      "subscription_adaptor_requests_total",
		Help:      "Total number of subscription-account adaptor calls",
	},
	[]string{"platform", "format", "result"},
)

// RelaySubscriptionFailoverTotal counts subscription-account failover attempts.
var RelaySubscriptionFailoverTotal = prometheus.NewCounterVec(
	prometheus.CounterOpts{
		Namespace: "micro_one_api",
		Subsystem: "relay",
		Name:      "subscription_failover_total",
		Help:      "Total number of subscription-account failover attempts",
	},
	[]string{"reason", "result"},
)

// RelaySubscriptionStickyTotal counts cross-session subscription-account
// stickiness outcomes. result: "hit" (served account == bound account),
// "rebind" (served a different account than bound / previously unbound),
// "miss" (no binding for the session), "reused_unschedulable" (a binding
// existed but the account failed validation and normal selection was used).
// The prompt-cache reuse rate is hit / (hit + rebind + miss) per platform.
var RelaySubscriptionStickyTotal = prometheus.NewCounterVec(
	prometheus.CounterOpts{
		Namespace: "micro_one_api",
		Subsystem: "relay",
		Name:      "subscription_sticky_total",
		Help:      "Cross-session subscription account stickiness outcomes",
	},
	[]string{"result", "platform"},
)

// RelayRuntimeBlocksTotal counts runtime account block operations.
var RelayRuntimeBlocksTotal = prometheus.NewCounterVec(
	prometheus.CounterOpts{
		Namespace: "micro_one_api",
		Subsystem: "relay",
		Name:      "runtime_blocks_total",
		Help:      "Total number of runtime subscription-account blocks",
	},
	[]string{"reason"},
)

// RelayRuntimeBlockActive tracks currently active runtime account blocks.
var RelayRuntimeBlockActive = prometheus.NewGauge(
	prometheus.GaugeOpts{
		Namespace: "micro_one_api",
		Subsystem: "relay",
		Name:      "runtime_block_active",
		Help:      "Current number of active runtime subscription-account blocks",
	},
)

// RelayAccountPoolChecksTotal counts local subscription-account schedulability checks.
var RelayAccountPoolChecksTotal = prometheus.NewCounterVec(
	prometheus.CounterOpts{
		Namespace: "micro_one_api",
		Subsystem: "relay",
		Name:      "account_pool_checks_total",
		Help:      "Total number of subscription-account pool schedulability checks",
	},
	[]string{"result"},
)

// RelayUpstreamPassthroughTotal counts upstream errors passed through to clients.
var RelayUpstreamPassthroughTotal = prometheus.NewCounterVec(
	prometheus.CounterOpts{
		Namespace: "micro_one_api",
		Subsystem: "relay",
		Name:      "upstream_passthrough_total",
		Help:      "Total number of upstream errors passed through to clients",
	},
	[]string{"kind", "status"},
)

// RelayCodexQuotaSnapshotsTotal counts Codex quota snapshot parse/record outcomes.
var RelayCodexQuotaSnapshotsTotal = prometheus.NewCounterVec(
	prometheus.CounterOpts{
		Namespace: "micro_one_api",
		Subsystem: "relay",
		Name:      "codex_quota_snapshots_total",
		Help:      "Total number of Codex quota snapshot parse and record outcomes",
	},
	[]string{"result"},
)

// RelayCodexQuotaUsedPercent records latest observed Codex quota usage by window.
var RelayCodexQuotaUsedPercent = prometheus.NewGaugeVec(
	prometheus.GaugeOpts{
		Namespace: "micro_one_api",
		Subsystem: "relay",
		Name:      "codex_quota_used_percent",
		Help:      "Latest observed Codex quota used percent by quota window",
	},
	[]string{"window"},
)

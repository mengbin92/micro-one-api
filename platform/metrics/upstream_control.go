package metrics

import "github.com/prometheus/client_golang/prometheus"

var UpstreamObservationDecisions = prometheus.NewCounterVec(prometheus.CounterOpts{
	Namespace: "micro_one_api", Subsystem: "channel", Name: "upstream_observation_decisions_total",
	Help: "Upstream control evidence accepted, ignored by fencing, or rejected by persistence.",
}, []string{"source_kind", "kind", "result"})

var RelayRetryBudgetRejected = prometheus.NewCounter(prometheus.CounterOpts{
	Namespace: "micro_one_api", Subsystem: "relay", Name: "retry_budget_rejected_total",
	Help: "Additional upstream attempts suppressed by the shared relay retry budget.",
})

var RelayUpstreamObservationFailures = prometheus.NewCounter(prometheus.CounterOpts{
	Namespace: "micro_one_api", Subsystem: "relay", Name: "upstream_observation_failures_total",
	Help: "Upstream control observations that could not be delivered to the channel owner.",
})

func init() {
	prometheus.MustRegister(UpstreamObservationDecisions, RelayRetryBudgetRejected, RelayUpstreamObservationFailures)
}

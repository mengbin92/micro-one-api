package metrics

import "github.com/prometheus/client_golang/prometheus"

// Labels are bounded: owner is identity/channel/subscription; operation and
// reason are constants at call sites. Never attach user, token or request IDs.
var (
	RoutingOutboxPending = prometheus.NewGaugeVec(
		prometheus.GaugeOpts{
			Namespace: "micro_one_api",
			Name:      "routing_outbox_pending",
			Help:      "Undelivered routing events in the owner database (not summed across replicas).",
		},
		[]string{"owner"},
	)
	RoutingOutboxOldestAge = prometheus.NewGaugeVec(
		prometheus.GaugeOpts{
			Namespace: "micro_one_api",
			Name:      "routing_outbox_oldest_pending_age_seconds",
			Help:      "Age of the oldest undelivered routing event; zero when empty.",
		},
		[]string{"owner"},
	)
	RoutingOutboxLastScan = prometheus.NewGaugeVec(
		prometheus.GaugeOpts{
			Namespace: "micro_one_api",
			Name:      "routing_outbox_last_scan_timestamp_seconds",
			Help:      "Unix time of the last successful pending scan; zero before first scan.",
		},
		[]string{"owner"},
	)
	RoutingOutboxLastSuccess = prometheus.NewGaugeVec(
		prometheus.GaugeOpts{
			Namespace: "micro_one_api",
			Name:      "routing_outbox_last_success_timestamp_seconds",
			Help:      "Unix time of the last acknowledged publish by this worker; zero until delivery.",
		},
		[]string{"owner"},
	)
	RoutingOutboxFailures = prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Namespace: "micro_one_api",
			Name:      "routing_outbox_failures_total",
			Help:      "Failed routing outbox operations (scan, load, publish, acknowledge, gc).",
		},
		[]string{"owner", "operation"},
	)
	RoutingAdmissionRejected = prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Namespace: "micro_one_api",
			Name:      "routing_admission_rejected_total",
			Help:      "Routing admission failures by operation and bounded failure reason.",
		},
		[]string{"operation", "reason"},
	)
	RoutingSnapshotFailures = prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Namespace: "micro_one_api",
			Name:      "routing_snapshot_validation_failures_total",
			Help:      "Request snapshot validation failures by operation and reason.",
		},
		[]string{"operation", "reason"},
	)
)

func init() {
	// Seed bounded failure series so increase() catches the first rejection.
	for operation, reasons := range map[string][]string{
		"resolve": {"relay_capability", "identity_capability", "channel_capability", "group_lookup", "entitlements", "policy_denied", "projection_mismatch"},
		"ordered": {"relay_capability", "channel_capability", "entitlements", "candidate_list", "bound_group", "group_lookup", "access_denied", "settlement", "candidate_probe", "policy_denied", "no_candidates"},
		"reserve": {"billing_capability"}, "recheck": {"admission_changed"},
	} {
		for _, reason := range reasons {
			RoutingAdmissionRejected.WithLabelValues(operation, reason)
		}
	}
	RoutingSnapshotFailures.WithLabelValues("validate", "content")
	for _, reason := range []string{"decode", "digest", "subject", "subscription", "subscription_window", "missing_snapshot"} {
		RoutingSnapshotFailures.WithLabelValues("read", reason)
	}

	prometheus.MustRegister(RoutingOutboxPending, RoutingOutboxOldestAge, RoutingOutboxLastScan, RoutingOutboxLastSuccess, RoutingOutboxFailures, RoutingAdmissionRejected, RoutingSnapshotFailures)
}

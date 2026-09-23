package metrics

import "github.com/prometheus/client_golang/prometheus"

var BillingDedupeConflicts = prometheus.NewCounterVec(prometheus.CounterOpts{
	Namespace: "micro_one_api", Subsystem: "billing", Name: "dedupe_conflicts_total",
	Help: "Rejected duplicate ledger claims by bounded operation and result.",
}, []string{"operation", "result"})

func init() { prometheus.MustRegister(BillingDedupeConflicts) }

func RecordBillingDedupeConflict(operation string) {
	switch operation {
	case "consume", "refund", "recharge", "subscription", "redeem":
	default:
		operation = "other"
	}
	BillingDedupeConflicts.WithLabelValues(operation, "duplicate").Inc()
}

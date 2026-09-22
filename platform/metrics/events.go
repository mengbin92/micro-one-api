package metrics

import "github.com/prometheus/client_golang/prometheus"

// EventStreamFailures counts Redis Streams event-processing failures that
// leave the message pending for reclaim: malformed payloads and handler
// errors. Without this counter the only trace of a poisoned event was a
// stdout printf, so stuck pending entries were invisible to operators
// (fault-matrix F11).
var EventStreamFailures = prometheus.NewCounterVec(
	prometheus.CounterOpts{
		Namespace: "micro_one_api",
		Subsystem: "events",
		Name:      "stream_failures_total",
		Help:      "Stream event processing failures left pending for reclaim",
	},
	[]string{"topic", "reason"}, // reason: malformed_payload, handler_error
)

// ModelUsageDropped counts usage samples dropped before persistence. The
// recorder deliberately never fails the request path, so without this
// counter drops were indistinguishable from recorded samples (fault-matrix
// F15).
var ModelUsageDropped = prometheus.NewCounterVec(
	prometheus.CounterOpts{
		Namespace: "micro_one_api",
		Subsystem: "channel",
		Name:      "model_usage_dropped_total",
		Help:      "Model usage samples dropped instead of persisted",
	},
	[]string{"reason"}, // reason: unregistered_model
)

// CredentialPersistFailures counts OAuth credential refreshes whose durable
// store write failed after all retries. The refreshed (possibly rotated)
// credential survives only in process memory; a process restart then risks
// bricking the account on the stale stored refresh token, so this must be
// alertable, not just a log line (fault-matrix F18).
var CredentialPersistFailures = prometheus.NewCounterVec(
	prometheus.CounterOpts{
		Namespace: "micro_one_api",
		Subsystem: "credential",
		Name:      "persist_failures_total",
		Help:      "OAuth token refreshes whose persistence failed after retries",
	},
	[]string{"platform"},
)

func init() {
	prometheus.MustRegister(EventStreamFailures)
	prometheus.MustRegister(ModelUsageDropped)
	prometheus.MustRegister(CredentialPersistFailures)
}

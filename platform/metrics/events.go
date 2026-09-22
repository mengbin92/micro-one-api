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

func init() {
	prometheus.MustRegister(EventStreamFailures)
}

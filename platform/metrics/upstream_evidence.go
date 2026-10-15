package metrics

import "github.com/prometheus/client_golang/prometheus"

var ExecutionOutboxPending = prometheus.NewGauge(prometheus.GaugeOpts{Namespace: "micro_one_api", Subsystem: "relay", Name: "execution_outbox_pending", Help: "Durable execution events waiting for billing receipt"})
var ExecutionOutboxOldestAge = prometheus.NewGauge(prometheus.GaugeOpts{Namespace: "micro_one_api", Subsystem: "relay", Name: "execution_outbox_oldest_age_seconds", Help: "Age of oldest unacknowledged execution event"})
var UpstreamProbeRuns = prometheus.NewCounterVec(prometheus.CounterOpts{Namespace: "micro_one_api", Subsystem: "monitor", Name: "upstream_probe_runs_total", Help: "Controlled upstream probes by bounded outcome"}, []string{"protocol", "outcome"})
var UpstreamProbeCoverage = prometheus.NewCounterVec(prometheus.CounterOpts{Namespace: "micro_one_api", Subsystem: "monitor", Name: "upstream_probe_coverage_total", Help: "Controlled probe coverage including budget and backoff skips"}, []string{"status"})

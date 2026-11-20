package metrics

import (
	"slices"
	"strconv"
	"sync"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	dto "github.com/prometheus/client_model/go"
)

// Observation-only upstream usage evidence. These metrics never describe
// billing trust or settlement. Label values are bounded enums; source/model/
// request identifiers stay in the controlled evidence store.

var UsageObservationStarted = prometheus.NewCounterVec(prometheus.CounterOpts{
	Namespace: "micro_one_api", Subsystem: "relay", Name: "usage_observation_started_total",
	Help: "Upstream sends observed at the shared send seam before the supplier write",
}, []string{"protocol", "source_kind", "purpose"})

var UsageObservationTerminal = prometheus.NewCounterVec(prometheus.CounterOpts{
	Namespace: "micro_one_api", Subsystem: "relay", Name: "usage_observation_terminal_total",
	Help: "Single terminal observation per send by transport capture and usage status",
}, []string{"protocol", "source_kind", "purpose", "capture", "status"})

var UsageObservationDelivery = prometheus.NewCounterVec(prometheus.CounterOpts{
	Namespace: "micro_one_api", Subsystem: "relay", Name: "usage_observation_delivery_total",
	Help: "Observation outbox outcomes: local persistence and remote acknowledgement separately",
}, []string{"result"})

// UsageObservationTransportSend is counted inside the shared transport seam,
// independent of the observation sink. A missing sink therefore shows up as
// expected="true",observed="false" instead of disappearing with the counters.
var UsageObservationTransportSend = prometheus.NewCounterVec(prometheus.CounterOpts{
	Namespace: "micro_one_api", Subsystem: "relay", Name: "usage_observation_transport_sends_total",
	Help: "Actual supplier sends at the transport seam by protocol and observation expectation",
}, []string{"protocol", "expected", "observed"})

var UsageObservationTransportLastMissing = prometheus.NewGaugeVec(prometheus.GaugeOpts{
	Namespace: "micro_one_api", Subsystem: "relay", Name: "usage_observation_transport_last_missing_timestamp_seconds",
	Help: "Unix time of the last supplier send that was expected to have an observation sink but did not",
}, []string{"protocol"})

// RecordUsageObservationTransportSend keeps the first detached send visible even
// if its counter is first scraped after the send already happened.
func RecordUsageObservationTransportSend(protocol string, expected, observed bool) {
	known := false
	for _, supported := range observationProtocols {
		if protocol == supported {
			known = true
			break
		}
	}
	if !known {
		protocol = "other"
	}
	UsageObservationTransportSend.WithLabelValues(protocol, strconv.FormatBool(expected), strconv.FormatBool(observed)).Inc()
	if expected && !observed {
		UsageObservationTransportLastMissing.WithLabelValues(protocol).Set(float64(time.Now().Unix()))
	}
}

var UsageObservationOutboxPending = prometheus.NewGauge(prometheus.GaugeOpts{
	Namespace: "micro_one_api", Subsystem: "relay", Name: "usage_observation_outbox_pending",
	Help: "Observation events waiting for remote acknowledgement",
})

var UsageObservationOutboxOldestAge = prometheus.NewGauge(prometheus.GaugeOpts{
	Namespace: "micro_one_api", Subsystem: "relay", Name: "usage_observation_outbox_oldest_age_seconds",
	Help: "Age of the oldest pending observation event",
})

var UsageObservationOutboxBytes = prometheus.NewGauge(prometheus.GaugeOpts{
	Namespace: "micro_one_api", Subsystem: "relay", Name: "usage_observation_outbox_bytes",
	Help: "Allocated size of the observation outbox database, WAL and shared memory in bytes",
})

var UsageObservationLastStats = prometheus.NewGauge(prometheus.GaugeOpts{
	Namespace: "micro_one_api", Subsystem: "relay", Name: "usage_observation_last_stats_timestamp_seconds",
	Help: "Unix time of the last successful observation outbox statistics refresh",
})

// UsageObservationExpected is set from configuration only (it is not the
// producer switch). Alert rules use it to distinguish "legitimately disabled"
// from "expected target failed to observe".
var UsageObservationExpected = prometheus.NewGauge(prometheus.GaugeOpts{
	Namespace: "micro_one_api", Subsystem: "relay", Name: "usage_observation_expected",
	Help: "1 when this target is configured to run the observation producer",
})

var UsageObservationEnabled = prometheus.NewGauge(prometheus.GaugeOpts{
	Namespace: "micro_one_api", Subsystem: "relay", Name: "usage_observation_enabled",
	Help: "1 when the observation-only usage evidence producer is configured on",
})

var UsageObservationReady = prometheus.NewGauge(prometheus.GaugeOpts{
	Namespace: "micro_one_api", Subsystem: "relay", Name: "usage_observation_ready",
	Help: "1 when the observation outbox is open, migrated and accepting local writes",
})

// UsageObservationLastAnomaly and UsageObservationLastGap solve the low-traffic
// first-sample blind spot: a brand-new counter series starts at 1, so
// increase() sees no event. The timestamp is set by the producer at the event
// itself.
var UsageObservationLastAnomaly = prometheus.NewGaugeVec(prometheus.GaugeOpts{
	Namespace: "micro_one_api", Subsystem: "relay", Name: "usage_observation_last_anomaly_timestamp_seconds",
	Help: "Unix time of the last complete user response with anomalous usage",
}, []string{"protocol", "source_kind", "status"})

var UsageObservationLastGap = prometheus.NewGaugeVec(prometheus.GaugeOpts{
	Namespace: "micro_one_api", Subsystem: "relay", Name: "usage_observation_last_gap_timestamp_seconds",
	Help: "Unix time of the last known evidence capture gap",
}, []string{"kind"})

// ExecutionOutboxWorkerExpected distinguishes a legitimately absent financial
// worker from a stuck one.
var ExecutionOutboxWorkerExpected = prometheus.NewGauge(prometheus.GaugeOpts{
	Namespace: "micro_one_api", Subsystem: "relay", Name: "execution_outbox_worker_expected",
	Help: "1 when a financial execution outbox worker is running on this target",
})

// ExecutionOutboxLastStats exposes the financial outbox statistics freshness.
// The gauge keeps its previous value when a refresh fails; this timestamp is
// what turns a stuck reader into an alert instead of a silent zero backlog.
var ExecutionOutboxLastStats = prometheus.NewGauge(prometheus.GaugeOpts{
	Namespace: "micro_one_api", Subsystem: "relay", Name: "execution_outbox_last_stats_timestamp_seconds",
	Help: "Unix time of the last successful execution outbox statistics refresh",
})

var observationProtocols = []string{"openai_chat", "responses", "anthropic_messages", "gemini", "embeddings", "stepaudio_realtime", "other"}
var observationSourceKinds = []string{"channel", "subscription", "unknown"}
var observationPurposes = []string{"user", "health_probe", "canary", "internal_test"}
var observationStatuses = []string{"verified", "missing", "invalid", "ambiguous", "unsupported", "unobserved"}

// observationAnomalyStatuses is the subset that can be reported as a complete
// response with anomalous usage; "verified" is excluded on purpose.
var observationAnomalyStatuses = []string{"missing", "invalid", "ambiguous", "unsupported", "unobserved"}
var observationDeliveryResults = []string{"accepted", "acked", "retried", "failed", "lost", "conflict", "rejected"}
var observationGapKinds = []string{"lost", "conflict", "rejected", "unmatched", "limited", "unobserved", "unknown"}

var observationTimestampMu sync.Mutex

func RecordUsageObservationAnomaly(protocol, source, status string, stamp float64) {
	if !slices.Contains(observationProtocols, protocol) {
		protocol = "other"
	}
	if !slices.Contains(observationSourceKinds, source) {
		source = "unknown"
	}
	if !slices.Contains(observationAnomalyStatuses, status) {
		status = "unobserved"
	}
	setObservationTimestamp(UsageObservationLastAnomaly.WithLabelValues(protocol, source, status), stamp)
}

func RecordUsageObservationGap(kind string, stamp float64) {
	if !slices.Contains(observationGapKinds, kind) {
		kind = "unknown"
	}
	setObservationTimestamp(UsageObservationLastGap.WithLabelValues(kind), stamp)
}

// A recovered older incident must not overwrite a newer live event.
func setObservationTimestamp(gauge prometheus.Gauge, stamp float64) {
	if stamp <= 0 {
		return
	}
	observationTimestampMu.Lock()
	defer observationTimestampMu.Unlock()
	var sample dto.Metric
	if err := gauge.Write(&sample); err == nil && stamp > sample.GetGauge().GetValue() {
		gauge.Set(stamp)
	}
}

// InitUsageObservationBaselines creates the zero series for the finite
// combinations the relay can actually produce, so the first real event is a
// step from 0 rather than the birth of a series. It deliberately does not
// pre-create the full cartesian product of every label.
func InitUsageObservationBaselines() {
	for _, protocol := range observationProtocols {
		UsageObservationTransportLastMissing.WithLabelValues(protocol)
		for _, expected := range []string{"false", "true"} {
			for _, observed := range []string{"false", "true"} {
				UsageObservationTransportSend.WithLabelValues(protocol, expected, observed)
			}
		}
		for _, source := range observationSourceKinds {
			for _, purpose := range observationPurposes {
				UsageObservationStarted.WithLabelValues(protocol, source, purpose)
				for _, status := range observationStatuses {
					UsageObservationTerminal.WithLabelValues(protocol, source, purpose, "complete", status)
				}
			}
		}
	}
	for _, result := range observationDeliveryResults {
		UsageObservationDelivery.WithLabelValues(result)
	}
	for _, kind := range observationGapKinds {
		UsageObservationLastGap.WithLabelValues(kind)
	}
	for _, protocol := range observationProtocols {
		for _, source := range observationSourceKinds {
			for _, status := range observationAnomalyStatuses {
				UsageObservationLastAnomaly.WithLabelValues(protocol, source, status)
			}
		}
	}
}

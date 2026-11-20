package metrics

import (
	"strconv"
	"testing"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/testutil"
	dto "github.com/prometheus/client_model/go"
)

// R12: the observation counters are pre-created at zero for the finite
// combinations the relay can produce, so the first real anomaly is a step from
// zero instead of a series birth that increase() cannot see.
func TestObservationBaselinesExistAtZero(t *testing.T) {
	families, err := prometheus.DefaultGatherer.Gather()
	if err != nil {
		t.Fatalf("gather: %v", err)
	}
	byName := map[string]*dto.MetricFamily{}
	for _, family := range families {
		byName[family.GetName()] = family
	}

	for name, want := range map[string]int{
		"micro_one_api_relay_usage_observation_started_total":                            7 * 3 * 4,
		"micro_one_api_relay_usage_observation_terminal_total":                           7 * 3 * 4 * 6,
		"micro_one_api_relay_usage_observation_last_anomaly_timestamp_seconds":           7 * 3 * 5,
		"micro_one_api_relay_usage_observation_last_gap_timestamp_seconds":               7,
		"micro_one_api_relay_usage_observation_delivery_total":                           7,
		"micro_one_api_relay_usage_observation_transport_sends_total":                    7 * 2 * 2,
		"micro_one_api_relay_usage_observation_transport_last_missing_timestamp_seconds": 7,
	} {
		family := byName[name]
		if family == nil {
			t.Fatalf("baseline family %s is not registered", name)
		}
		if got := len(family.GetMetric()); got != want {
			t.Fatalf("%s has %d baseline series, want %d", name, got, want)
		}
		for _, sample := range family.GetMetric() {
			value := sample.GetCounter().GetValue()
			if sample.GetGauge() != nil {
				value = sample.GetGauge().GetValue()
			}
			if value != 0 {
				t.Fatalf("%s baseline starts at %v, want 0: %v", name, value, sample.GetLabel())
			}
		}
	}
}

func TestRecordUsageObservationTransportSendKeepsFirstMissingSend(t *testing.T) {
	for _, tc := range []struct {
		protocol           string
		expected, observed bool
	}{
		{"responses", true, false},
		{"responses", true, true},
		{"responses", false, false},
		{"unbounded/model/name", true, false},
	} {
		protocol := tc.protocol
		if protocol == "unbounded/model/name" {
			protocol = "other"
		}
		labels := []string{protocol, strconv.FormatBool(tc.expected), strconv.FormatBool(tc.observed)}
		missing := UsageObservationTransportLastMissing.WithLabelValues(protocol)
		missing.Set(0)
		counter := UsageObservationTransportSend.WithLabelValues(labels...)
		before := testutil.ToFloat64(counter)
		started := float64(time.Now().Unix())
		RecordUsageObservationTransportSend(tc.protocol, tc.expected, tc.observed)
		if got := testutil.ToFloat64(counter); got != before+1 {
			t.Fatalf("%v: counter = %v, want %v", tc, got, before+1)
		}
		stamp := testutil.ToFloat64(missing)
		if tc.expected && !tc.observed {
			if stamp < started {
				t.Fatalf("%v: first detached send has no event timestamp: %v", tc, stamp)
			}
		} else if stamp != 0 {
			t.Fatalf("%v: an observed or excluded send was marked detached", tc)
		}
		missing.Set(0)
		UsageObservationTransportSend.DeleteLabelValues(labels...)
	}
	InitUsageObservationBaselines()
}

func TestRecoveredObservationSignalsNeverOverwriteNewerLiveEvents(t *testing.T) {
	anomaly := UsageObservationLastAnomaly.WithLabelValues("responses", "channel", "invalid")
	gap := UsageObservationLastGap.WithLabelValues("limited")
	anomaly.Set(0)
	gap.Set(0)
	t.Cleanup(func() { anomaly.Set(0); gap.Set(0) })
	newer := float64(time.Now().Unix())
	RecordUsageObservationAnomaly("responses", "channel", "invalid", newer)
	RecordUsageObservationGap("limited", newer)
	RecordUsageObservationAnomaly("responses", "channel", "invalid", newer-30)
	RecordUsageObservationGap("limited", newer-30)
	if testutil.ToFloat64(anomaly) != newer || testutil.ToFloat64(gap) != newer {
		t.Fatal("restoring older metadata moved a live incident timestamp backwards")
	}
}

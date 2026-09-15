package routingtest

import (
	"net/url"
	"os"
	"strconv"
	"time"

	"github.com/stretchr/testify/require"
	"micro-one-api/pkg/jsonx"
)

func (s *suite) prom(query string) ([]float64, bool) {
	code, raw, err := send("GET", os.Getenv("PROMETHEUS_HTTP_BASE")+"/api/v1/query?query="+url.QueryEscape(query), "", nil, "")
	if err != nil || code != 200 {
		return nil, false
	}
	var response struct {
		Status string `json:"status"`
		Data   struct {
			Result []struct {
				Value []jsonx.RawMessage `json:"value"`
			} `json:"result"`
		} `json:"data"`
	}
	if jsonx.Unmarshal(raw, &response) != nil || response.Status != "success" {
		return nil, false
	}
	values := make([]float64, 0, len(response.Data.Result))
	for _, row := range response.Data.Result {
		if len(row.Value) != 2 {
			return nil, false
		}
		var value string
		if jsonx.Unmarshal(row.Value[1], &value) != nil {
			return nil, false
		}
		n, err := strconv.ParseFloat(value, 64)
		if err != nil {
			return nil, false
		}
		values = append(values, n)
	}
	return values, true
}
func (s *suite) waitMetric(query string, match func([]float64) bool, timeout time.Duration) {
	s.t.Helper()
	require.Eventually(s.t, func() bool {
		values, ok := s.prom(query)
		return ok && match(values)
	}, timeout, time.Second, query)
}
func positiveSample(values []float64) bool { return len(values) > 0 && values[0] > 0 }
func (s *suite) observeRedisOutage() {
	s.waitMetric(`micro_one_api_routing_outbox_pending{owner="identity"} > 0`, positiveSample, 30*time.Second)
	s.waitMetric(`micro_one_api_routing_outbox_failures_total{owner="identity",operation="publish"} > 0`, positiveSample, 30*time.Second)
	for _, alert := range []string{"RoutingOutboxDeliveryFailing", "RoutingOutboxBacklog"} {
		s.waitMetric(`ALERTS{alertname="`+alert+`",owner="identity",alertstate="firing"}`, positiveSample, 180*time.Second)
		s.t.Logf("observability: Redis stopped; %s firing for identity", alert)
	}
}
func (s *suite) observeRedisRecovery() {
	s.waitMetric(`micro_one_api_routing_outbox_pending{owner="identity"}`, func(v []float64) bool { return len(v) == 1 && v[0] == 0 }, 45*time.Second)
	s.waitMetric(`micro_one_api_routing_outbox_oldest_pending_age_seconds{owner="identity"}`, func(v []float64) bool { return len(v) == 1 && v[0] == 0 }, 45*time.Second)
	s.waitMetric(`micro_one_api_routing_outbox_last_success_timestamp_seconds{owner="identity"} > 0`, positiveSample, 30*time.Second)
	s.waitMetric(`ALERTS{alertname=~"RoutingOutboxDeliveryFailing|RoutingOutboxBacklog",owner="identity",alertstate="firing"}`, func(v []float64) bool { return len(v) == 0 }, 45*time.Second)
	s.t.Log("observability: Redis recovered; pending=0 oldest_age=0 last_success>0; delivery/backlog alerts cleared; revoked access denied and regrant settled")
}
func (s *suite) observeCapabilityRejection() {
	s.waitMetric(`micro_one_api_routing_admission_rejected_total{operation="reserve",reason="billing_capability"} > 0`, positiveSample, 30*time.Second)
	s.waitMetric(`ALERTS{alertname="RoutingCapabilityRejected",reason="billing_capability",alertstate="firing"}`, positiveSample, 45*time.Second)
	s.t.Log("observability: missing billing capability rejected; bounded reason metric and RoutingCapabilityRejected firing")
}

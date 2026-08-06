package metrics

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"strings"

	dto "github.com/prometheus/client_model/go"
	"github.com/prometheus/common/expfmt"
)

// routingMetricNames are the relay-gateway exposition-format metric families
// that ScrapeRoutingRates aggregates. They match the counters registered in
// platform/metrics/routing.go.
const (
	routingSelectionMetricName = "micro_one_api_routing_selection_total"
	routingFallbackMetricName  = "micro_one_api_routing_fallback_total"
)

// ScrapeRoutingRates reads cumulative routing counters directly from
// relay-gateway's /metrics endpoint (Prometheus exposition format).
//
// Unlike QueryRoutingRates (which runs increase() over a time window via a
// Prometheus server), this returns the current cumulative counter values since
// the relay-gateway process started. It is a degraded fallback for when
// Prometheus is unavailable: it cannot show per-window increments, but it
// gives ops a baseline view of routing health (total errors, total fallbacks)
// so the routing-ops page is not entirely blind during a Prometheus outage.
//
// The returned RoutingRates has Source set to "relay_scrape".
func ScrapeRoutingRates(ctx context.Context, client *http.Client, baseURL string) (RoutingRates, error) {
	if client == nil {
		client = http.DefaultClient
	}
	endpoint := strings.TrimRight(strings.TrimSpace(baseURL), "/") + "/metrics"

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return RoutingRates{}, fmt.Errorf("build relay metrics request: %w", err)
	}
	req.Header.Set("Accept", string(expfmt.NewFormat(expfmt.TypeTextPlain)))

	resp, err := client.Do(req)
	if err != nil {
		return RoutingRates{}, fmt.Errorf("scrape relay metrics: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return RoutingRates{}, fmt.Errorf("relay /metrics returned HTTP %d", resp.StatusCode)
	}

	format := expfmt.ResponseFormat(resp.Header)
	if format == "" {
		format = expfmt.NewFormat(expfmt.TypeTextPlain)
	}
	dec := expfmt.NewDecoder(io.LimitReader(resp.Body, 4<<20), format)

	var rates RoutingRates
	for {
		var family dto.MetricFamily
		if err := dec.Decode(&family); err != nil {
			if err == io.EOF {
				break
			}
			return RoutingRates{}, fmt.Errorf("decode relay metrics: %w", err)
		}
		switch family.GetName() {
		case routingSelectionMetricName:
			for _, m := range family.GetMetric() {
				val := counterValue(m)
				rates.SelectionTotal += val
				if label := findLabel(m, "result"); label != "" {
					switch label {
					case "success":
						rates.SuccessTotal += val
					case "error":
						rates.ErrorTotal += val
					case "client_error":
						rates.ClientErrorTotal += val
					}
				}
			}
		case routingFallbackMetricName:
			for _, m := range family.GetMetric() {
				rates.FallbackTotal += counterValue(m)
			}
		}
	}
	rates.Source = "relay_scrape"
	return rates, nil
}

// counterValue extracts the value from a metric, treating COUNTER, UNTYPED,
// and GAUGE families uniformly (relay counters are always COUNTER, but being
// defensive against exposition quirks costs nothing).
func counterValue(m *dto.Metric) float64 {
	if m == nil {
		return 0
	}
	if c := m.GetCounter(); c != nil {
		return c.GetValue()
	}
	if g := m.GetGauge(); g != nil {
		return g.GetValue()
	}
	if u := m.GetUntyped(); u != nil {
		return u.GetValue()
	}
	return 0
}

// findLabel returns the value of the named label, or "" if absent.
func findLabel(m *dto.Metric, name string) string {
	for _, lp := range m.GetLabel() {
		if lp.GetName() == name {
			return lp.GetValue()
		}
	}
	return ""
}

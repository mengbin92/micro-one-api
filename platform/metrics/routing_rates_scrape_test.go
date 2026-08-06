package metrics

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// Sample Prometheus exposition text matching the relay-gateway /metrics output.
// Includes the two routing families plus an unrelated metric to verify filtering.
const sampleRelayMetricsExposition = `# HELP micro_one_api_routing_selection_total Source-selection outcomes
# TYPE micro_one_api_routing_selection_total counter
micro_one_api_routing_selection_total{source_kind="subscription",result="success",provider_family="anthropic"} 432
micro_one_api_routing_selection_total{source_kind="channel",result="success",provider_family="openai"} 12
micro_one_api_routing_selection_total{source_kind="subscription",result="error",provider_family="anthropic"} 3
micro_one_api_routing_selection_total{source_kind="subscription",result="client_error",provider_family="anthropic"} 5
# HELP micro_one_api_http_requests_total Total number of HTTP requests
# TYPE micro_one_api_http_requests_total counter
micro_one_api_http_requests_total{service="relay-gateway",method="POST",path="/v1/chat/completions",status="200"} 450
# HELP micro_one_api_routing_fallback_total Requests that fell back
# TYPE micro_one_api_routing_fallback_total counter
micro_one_api_routing_fallback_total{reason="upstream_5xx",provider_family="anthropic"} 7
micro_one_api_routing_fallback_total{reason="timeout",provider_family="openai"} 2
`

func TestScrapeRoutingRates_AggregatesCounters(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/metrics" {
			t.Fatalf("unexpected path: %s", r.URL.Path)
		}
		w.Header().Set("Content-Type", "text/plain; version=0.0.4; charset=utf-8")
		_, _ = io.WriteString(w, sampleRelayMetricsExposition)
	}))
	defer server.Close()

	rates, err := ScrapeRoutingRates(context.Background(), server.Client(), server.URL)
	if err != nil {
		t.Fatalf("ScrapeRoutingRates failed: %v", err)
	}
	// 432 + 12 + 3 + 5 = 452
	if rates.SelectionTotal != 452 {
		t.Errorf("SelectionTotal = %v, want 452", rates.SelectionTotal)
	}
	if rates.SuccessTotal != 444 { // 432 + 12
		t.Errorf("SuccessTotal = %v, want 444", rates.SuccessTotal)
	}
	if rates.ErrorTotal != 3 {
		t.Errorf("ErrorTotal = %v, want 3", rates.ErrorTotal)
	}
	if rates.ClientErrorTotal != 5 {
		t.Errorf("ClientErrorTotal = %v, want 5", rates.ClientErrorTotal)
	}
	if rates.FallbackTotal != 9 { // 7 + 2
		t.Errorf("FallbackTotal = %v, want 9", rates.FallbackTotal)
	}
	if rates.Source != "relay_scrape" {
		t.Errorf("Source = %q, want \"relay_scrape\"", rates.Source)
	}
}

func TestScrapeRoutingRates_EmptyMetrics(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/plain; version=0.0.4; charset=utf-8")
		_, _ = io.WriteString(w, "")
	}))
	defer server.Close()

	rates, err := ScrapeRoutingRates(context.Background(), server.Client(), server.URL)
	if err != nil {
		t.Fatalf("ScrapeRoutingRates failed: %v", err)
	}
	if rates.SelectionTotal != 0 || rates.FallbackTotal != 0 {
		t.Errorf("expected zero rates, got %+v", rates)
	}
	if rates.Source != "relay_scrape" {
		t.Errorf("Source = %q, want \"relay_scrape\"", rates.Source)
	}
}

func TestScrapeRoutingRates_ServerError(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusServiceUnavailable)
	}))
	defer server.Close()

	_, err := ScrapeRoutingRates(context.Background(), server.Client(), server.URL)
	if err == nil || !strings.Contains(err.Error(), "503") {
		t.Fatalf("error = %v, want HTTP 503 failure", err)
	}
}

func TestScrapeRoutingRates_IgnoresUnrelatedMetrics(t *testing.T) {
	exposition := `# HELP micro_one_api_http_requests_total Total number of HTTP requests
# TYPE micro_one_api_http_requests_total counter
micro_one_api_http_requests_total{service="relay-gateway",method="POST"} 1000
# HELP some_other_metric Unrelated
# TYPE some_other_metric counter
some_other_metric{foo="bar"} 999
`
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/plain; version=0.0.4; charset=utf-8")
		_, _ = io.WriteString(w, exposition)
	}))
	defer server.Close()

	rates, err := ScrapeRoutingRates(context.Background(), server.Client(), server.URL)
	if err != nil {
		t.Fatalf("ScrapeRoutingRates failed: %v", err)
	}
	// No routing metrics present → all zeros.
	if rates.SelectionTotal != 0 || rates.FallbackTotal != 0 || rates.SuccessTotal != 0 {
		t.Errorf("expected zero routing rates, got %+v", rates)
	}
}

func TestQueryRoutingRates_SetsPrometheusSource(t *testing.T) {
	client := &http.Client{Transport: routingRatesRoundTripFunc(func(*http.Request) (*http.Response, error) {
		body := `{"status":"success","data":{"resultType":"vector","result":[]}}`
		return &http.Response{
			StatusCode: http.StatusOK,
			Body:       io.NopCloser(strings.NewReader(body)),
			Header:     make(http.Header),
		}, nil
	})}

	rates, err := QueryRoutingRates(context.Background(), client, "http://prometheus:9090", 100, 200)
	if err != nil {
		t.Fatalf("QueryRoutingRates failed: %v", err)
	}
	if rates.Source != "prometheus" {
		t.Errorf("Source = %q, want \"prometheus\"", rates.Source)
	}
}

package server

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

const prometheusSuccessBody = `{"status":"success","data":{"resultType":"vector","result":[` +
	`{"metric":{"result":"success"},"value":[200,"80"]},` +
	`{"metric":{"result":"error"},"value":[200,"10"]},` +
	`{"metric":{"result":"client_error"},"value":[200,"5"]}` +
	`]}}`

const relayScrapeBody = `# HELP micro_one_api_routing_selection_total Source-selection outcomes
# TYPE micro_one_api_routing_selection_total counter
micro_one_api_routing_selection_total{source_kind="subscription",result="success",provider_family="anthropic"} 432
micro_one_api_routing_selection_total{source_kind="subscription",result="error",provider_family="anthropic"} 3
micro_one_api_routing_selection_total{source_kind="subscription",result="client_error",provider_family="anthropic"} 5
# HELP micro_one_api_routing_fallback_total Requests that fell back
# TYPE micro_one_api_routing_fallback_total counter
micro_one_api_routing_fallback_total{reason="upstream_5xx",provider_family="anthropic"} 7
`

// prometheusHandler returns an http.HandlerFunc that responds as a Prometheus
// server with the given body and status code.
func prometheusHandler(body string, status int) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if status != http.StatusOK {
			w.WriteHeader(status)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, body)
	}
}

// relayMetricsHandler returns an http.HandlerFunc that responds as
// relay-gateway's /metrics endpoint.
func relayMetricsHandler(body string, status int) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if status != http.StatusOK {
			w.WriteHeader(status)
			return
		}
		w.Header().Set("Content-Type", "text/plain; version=0.0.4; charset=utf-8")
		_, _ = io.WriteString(w, body)
	}
}

func TestLoadRoutingRates_PrometheusSucceeds(t *testing.T) {
	// Both sources configured; Prometheus should win (precise window increment).
	promSrv := httptest.NewServer(prometheusHandler(prometheusSuccessBody, http.StatusOK))
	defer promSrv.Close()
	relaySrv := httptest.NewServer(relayMetricsHandler(relayScrapeBody, http.StatusOK))
	defer relaySrv.Close()

	rates, errs := loadRoutingRates(context.Background(), promSrv.URL, relaySrv.URL, 100, 200)
	if len(errs) != 0 {
		t.Fatalf("expected no errors, got %v", errs)
	}
	if rates.Source != "prometheus" {
		t.Errorf("Source = %q, want \"prometheus\"", rates.Source)
	}
	if rates.Cumulative {
		t.Errorf("Cumulative = true, want false for Prometheus source")
	}
	// 80 success + 10 error + 5 client_error = 95 total
	if rates.SelectionTotal != 95 {
		t.Errorf("SelectionTotal = %v, want 95", rates.SelectionTotal)
	}
	if rates.SuccessTotal != 80 {
		t.Errorf("SuccessTotal = %v, want 80", rates.SuccessTotal)
	}
	if rates.ErrorRate != 15.0/95.0 {
		t.Errorf("ErrorRate = %v, want %v", rates.ErrorRate, 15.0/95.0)
	}
}

func TestLoadRoutingRates_FallbackToRelayScrape(t *testing.T) {
	// Prometheus down (500), relay-gateway up → should fall back to scrape.
	promSrv := httptest.NewServer(prometheusHandler("", http.StatusInternalServerError))
	defer promSrv.Close()
	relaySrv := httptest.NewServer(relayMetricsHandler(relayScrapeBody, http.StatusOK))
	defer relaySrv.Close()

	rates, errs := loadRoutingRates(context.Background(), promSrv.URL, relaySrv.URL, 100, 200)
	if rates.Source != "relay_scrape" {
		t.Errorf("Source = %q, want \"relay_scrape\"", rates.Source)
	}
	if !rates.Cumulative {
		t.Errorf("Cumulative = false, want true for scrape source")
	}
	// 432 + 3 + 5 = 440 total
	if rates.SelectionTotal != 440 {
		t.Errorf("SelectionTotal = %v, want 440", rates.SelectionTotal)
	}
	if rates.FallbackTotal != 7 {
		t.Errorf("FallbackTotal = %v, want 7", rates.FallbackTotal)
	}
	// Should carry the non-fatal Prometheus failure message.
	if len(errs) != 1 || !strings.Contains(errs[0], "routing metrics query failed") {
		t.Fatalf("expected 1 Prometheus failure error, got %v", errs)
	}
}

func TestLoadRoutingRates_PrometheusNotConfigured(t *testing.T) {
	// PROMETHEUS_URL empty, RELAY_METRICS_ENDPOINT configured → scrape.
	relaySrv := httptest.NewServer(relayMetricsHandler(relayScrapeBody, http.StatusOK))
	defer relaySrv.Close()

	rates, errs := loadRoutingRates(context.Background(), "", relaySrv.URL, 100, 200)
	if rates.Source != "relay_scrape" {
		t.Errorf("Source = %q, want \"relay_scrape\"", rates.Source)
	}
	if len(errs) != 0 {
		t.Fatalf("expected no errors when Prometheus is simply unconfigured and scrape succeeds, got %v", errs)
	}
}

func TestLoadRoutingRates_BothFail(t *testing.T) {
	// Prometheus down, relay-gateway also down.
	promSrv := httptest.NewServer(prometheusHandler("", http.StatusServiceUnavailable))
	defer promSrv.Close()
	relaySrv := httptest.NewServer(relayMetricsHandler("", http.StatusServiceUnavailable))
	defer relaySrv.Close()

	rates, errs := loadRoutingRates(context.Background(), promSrv.URL, relaySrv.URL, 100, 200)
	if rates.Source != "" {
		t.Errorf("Source = %q, want empty (both failed)", rates.Source)
	}
	if len(errs) != 2 {
		t.Fatalf("expected 2 errors, got %d: %v", len(errs), errs)
	}
}

func TestLoadRoutingRates_NeitherConfigured(t *testing.T) {
	rates, errs := loadRoutingRates(context.Background(), "", "", 100, 200)
	if rates.Source != "" {
		t.Errorf("Source = %q, want empty", rates.Source)
	}
	if len(errs) != 1 {
		t.Fatalf("expected 1 error (not configured), got %d: %v", len(errs), errs)
	}
	if !strings.Contains(errs[0], "PROMETHEUS_URL is not configured") {
		t.Fatalf("error message = %q, want 'not configured'", errs[0])
	}
}

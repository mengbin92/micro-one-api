package server

import (
	"context"
	"net/http"
	"time"

	"micro-one-api/platform/metrics"
)

// loadRoutingRates fetches routing rates using a two-tier fallback strategy:
//
//  1. Prometheus (preferred): a PromQL increase() query gives a precise
//     per-window increment that aligns with the billing aggregates shown
//     in the same view. Requires PROMETHEUS_URL.
//
//  2. relay-gateway /metrics scrape (degraded fallback): when Prometheus is
//     unavailable or fails, admin-api scrapes relay-gateway's standard
//     exposition endpoint directly. This yields cumulative process counters
//     (not window-scoped), but keeps the ops page functional during a
//     Prometheus outage. Requires RELAY_METRICS_ENDPOINT.
//
// Returns the populated routingOpsRates and the list of non-fatal error
// messages collected along the way (from the sources that failed). When
// both sources fail (or neither is configured), the returned rates have an
// empty Source, signalling the caller to set partial=true.
func loadRoutingRates(ctx context.Context, prometheusURL, relayMetricsURL string, start, end int64) (routingOpsRates, []string) {
	var errors []string

	// --- Tier 1: Prometheus ---
	if prometheusURL != "" {
		metricsCtx, cancel := context.WithTimeout(ctx, 3*time.Second)
		rates, err := metrics.QueryRoutingRates(metricsCtx, &http.Client{Timeout: 3 * time.Second}, prometheusURL, start, end)
		cancel()
		if err == nil {
			return routingRatesView(rates, false), nil
		}
		errors = append(errors, "routing metrics query failed: "+err.Error())
	} else if relayMetricsURL == "" {
		errors = append(errors, "routing metrics unavailable: PROMETHEUS_URL is not configured")
	}

	// --- Tier 2: relay-gateway /metrics scrape ---
	if relayMetricsURL != "" {
		scrapeCtx, cancel := context.WithTimeout(ctx, 3*time.Second)
		rates, err := metrics.ScrapeRoutingRates(scrapeCtx, &http.Client{Timeout: 3 * time.Second}, relayMetricsURL)
		cancel()
		if err == nil {
			return routingRatesView(rates, true), errors
		}
		errors = append(errors, "relay metrics scrape failed: "+err.Error())
	}

	// --- Both failed (or unconfigured) ---
	return routingOpsRates{}, errors
}

// routingRatesView converts a metrics.RoutingRates into the JSON-facing
// routingOpsRates, computing derived rates. cumulative marks whether the
// values are process-lifetime totals (scrape) rather than window increments
// (Prometheus).
func routingRatesView(rates metrics.RoutingRates, cumulative bool) routingOpsRates {
	out := routingOpsRates{
		SelectionTotal:   rates.SelectionTotal,
		SuccessTotal:     rates.SuccessTotal,
		ErrorTotal:       rates.ErrorTotal,
		ClientErrorTotal: rates.ClientErrorTotal,
		FallbackTotal:    rates.FallbackTotal,
		Source:           rates.Source,
		Cumulative:       cumulative,
	}
	if rates.SelectionTotal > 0 {
		out.ErrorRate = (rates.ErrorTotal + rates.ClientErrorTotal) / rates.SelectionTotal
		out.FallbackRate = rates.FallbackTotal / rates.SelectionTotal
	}
	if out.Source == "" {
		// Derive a source label from the cumulative flag so the JSON always
		// carries provenance when rates are present.
		if cumulative {
			out.Source = "relay_scrape"
		} else {
			out.Source = "prometheus"
		}
	}
	return out
}

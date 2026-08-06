package metrics

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"

	"micro-one-api/pkg/jsonx"
)

// RoutingRates holds routing selection outcomes for a requested time window.
//
// Source distinguishes the data provenance so callers can tell whether the
// numbers are a precise window increase (from Prometheus) or cumulative
// process counters scraped directly from relay-gateway (a degraded fallback).
type RoutingRates struct {
	SelectionTotal   float64
	ErrorTotal       float64
	ClientErrorTotal float64
	SuccessTotal     float64
	FallbackTotal    float64
	// Source identifies where the rates came from: "prometheus" for a PromQL
	// increase() window query, "relay_scrape" for cumulative counters scraped
	// directly from relay-gateway's /metrics endpoint, or "" when unset.
	Source string
}

type prometheusQueryResponse struct {
	Status    string `json:"status"`
	ErrorType string `json:"errorType"`
	Error     string `json:"error"`
	Data      struct {
		ResultType string                     `json:"resultType"`
		Result     []prometheusQueryResultRow `json:"result"`
	} `json:"data"`
}

type prometheusQueryResultRow struct {
	Metric map[string]string  `json:"metric"`
	Value  []jsonx.RawMessage `json:"value"`
}

// QueryRoutingRates reads relay-gateway counters from Prometheus for exactly
// [start,end]. Using increase() handles counter resets and keeps the rates in
// the same time window as the billing aggregates shown by the admin view.
func QueryRoutingRates(ctx context.Context, client *http.Client, baseURL string, start, end int64) (RoutingRates, error) {
	if client == nil {
		client = http.DefaultClient
	}
	if end <= start {
		return RoutingRates{}, fmt.Errorf("invalid routing metrics window: start=%d end=%d", start, end)
	}
	rangeSelector := strconv.FormatInt(end-start, 10) + "s"
	selectionQuery := fmt.Sprintf("sum by (result) (increase(micro_one_api_routing_selection_total[%s]))", rangeSelector)
	fallbackQuery := fmt.Sprintf("sum(increase(micro_one_api_routing_fallback_total[%s]))", rangeSelector)

	selectionRows, err := queryPrometheusVector(ctx, client, baseURL, selectionQuery, end)
	if err != nil {
		return RoutingRates{}, fmt.Errorf("query routing selections: %w", err)
	}
	fallbackRows, err := queryPrometheusVector(ctx, client, baseURL, fallbackQuery, end)
	if err != nil {
		return RoutingRates{}, fmt.Errorf("query routing fallbacks: %w", err)
	}

	var rates RoutingRates
	for _, row := range selectionRows {
		value, err := prometheusSampleValue(row)
		if err != nil {
			return RoutingRates{}, err
		}
		rates.SelectionTotal += value
		switch row.Metric["result"] {
		case "success":
			rates.SuccessTotal += value
		case "error":
			rates.ErrorTotal += value
		case "client_error":
			rates.ClientErrorTotal += value
		}
	}
	for _, row := range fallbackRows {
		value, err := prometheusSampleValue(row)
		if err != nil {
			return RoutingRates{}, err
		}
		rates.FallbackTotal += value
	}
	rates.Source = "prometheus"
	return rates, nil
}

func queryPrometheusVector(ctx context.Context, client *http.Client, baseURL, query string, at int64) ([]prometheusQueryResultRow, error) {
	endpoint, err := url.Parse(strings.TrimSpace(baseURL))
	if err != nil {
		return nil, fmt.Errorf("parse prometheus URL: %w", err)
	}
	if endpoint.Scheme != "http" && endpoint.Scheme != "https" {
		return nil, fmt.Errorf("prometheus URL must use http or https")
	}
	endpoint.Path = strings.TrimRight(endpoint.Path, "/") + "/api/v1/query"
	params := endpoint.Query()
	params.Set("query", query)
	params.Set("time", strconv.FormatInt(at, 10))
	endpoint.RawQuery = params.Encode()

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint.String(), nil)
	if err != nil {
		return nil, err
	}
	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("prometheus returned HTTP %d", resp.StatusCode)
	}
	var payload prometheusQueryResponse
	if err := jsonx.NewDecoder(io.LimitReader(resp.Body, 1<<20)).Decode(&payload); err != nil {
		return nil, fmt.Errorf("decode prometheus response: %w", err)
	}
	if payload.Status != "success" {
		return nil, fmt.Errorf("prometheus query failed (%s): %s", payload.ErrorType, payload.Error)
	}
	if payload.Data.ResultType != "vector" {
		return nil, fmt.Errorf("unexpected prometheus result type %q", payload.Data.ResultType)
	}
	return payload.Data.Result, nil
}

func prometheusSampleValue(row prometheusQueryResultRow) (float64, error) {
	if len(row.Value) != 2 {
		return 0, fmt.Errorf("invalid prometheus sample")
	}
	var raw string
	if err := jsonx.Unmarshal(row.Value[1], &raw); err != nil {
		return 0, fmt.Errorf("decode prometheus sample value: %w", err)
	}
	value, err := strconv.ParseFloat(raw, 64)
	if err != nil {
		return 0, fmt.Errorf("parse prometheus sample value %q: %w", raw, err)
	}
	return value, nil
}

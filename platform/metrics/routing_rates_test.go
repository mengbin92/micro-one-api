package metrics

import (
	"context"
	"io"
	"net/http"
	"net/url"
	"strings"
	"testing"
)

type routingRatesRoundTripFunc func(*http.Request) (*http.Response, error)

func (fn routingRatesRoundTripFunc) RoundTrip(req *http.Request) (*http.Response, error) {
	return fn(req)
}

func TestQueryRoutingRates_UsesRequestedWindow(t *testing.T) {
	var queries []url.Values
	client := &http.Client{Transport: routingRatesRoundTripFunc(func(req *http.Request) (*http.Response, error) {
		queries = append(queries, req.URL.Query())
		body := `{"status":"success","data":{"resultType":"vector","result":[]}}`
		query := req.URL.Query().Get("query")
		if strings.Contains(query, "selection_total") {
			body = `{"status":"success","data":{"resultType":"vector","result":[` +
				`{"metric":{"result":"success"},"value":[200,"80"]},` +
				`{"metric":{"result":"error"},"value":[200,"10"]},` +
				`{"metric":{"result":"client_error"},"value":[200,"5"]}` +
				`]}}`
		} else if strings.Contains(query, "fallback_total") {
			body = `{"status":"success","data":{"resultType":"vector","result":[` +
				`{"metric":{},"value":[200,"7"]}` +
				`]}}`
		}
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
	if rates.SelectionTotal != 95 || rates.SuccessTotal != 80 || rates.ErrorTotal != 10 || rates.ClientErrorTotal != 5 || rates.FallbackTotal != 7 {
		t.Fatalf("unexpected rates: %+v", rates)
	}
	if len(queries) != 2 {
		t.Fatalf("query count = %d, want 2", len(queries))
	}
	for _, values := range queries {
		if !strings.Contains(values.Get("query"), "[100s]") {
			t.Fatalf("query %q does not use requested 100s window", values.Get("query"))
		}
		if values.Get("time") != "200" {
			t.Fatalf("query time = %q, want 200", values.Get("time"))
		}
	}
}

func TestQueryRoutingRates_RejectsPrometheusFailure(t *testing.T) {
	client := &http.Client{Transport: routingRatesRoundTripFunc(func(*http.Request) (*http.Response, error) {
		return &http.Response{
			StatusCode: http.StatusOK,
			Body: io.NopCloser(strings.NewReader(
				`{"status":"error","errorType":"bad_data","error":"invalid expression"}`,
			)),
			Header: make(http.Header),
		}, nil
	})}

	_, err := QueryRoutingRates(context.Background(), client, "http://prometheus:9090", 100, 200)
	if err == nil || !strings.Contains(err.Error(), "invalid expression") {
		t.Fatalf("error = %v, want Prometheus query failure", err)
	}
}

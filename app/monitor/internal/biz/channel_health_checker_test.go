package biz

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"micro-one-api/platform/metrics"

	"github.com/prometheus/client_golang/prometheus/testutil"
)

const monitorMetricEpsilon = 0.000001

func assertMonitorMetricDelta(t *testing.T, before, after, want float64) {
	t.Helper()
	if diff := after - before; diff < want-monitorMetricEpsilon || diff > want+monitorMetricEpsilon {
		t.Fatalf("metric delta = %f, want %f", diff, want)
	}
}

type checkerChannelClient struct {
	channels   []ChannelProbeSummary
	details    map[int64]*ChannelProbeDetail
	healthReqs []struct {
		ChannelID    int64
		Success      bool
		ErrMsg       string
		ResponseTime int64
	}
}

func (c *checkerChannelClient) ListEnabledChannels(ctx context.Context, page, pageSize int32) ([]ChannelProbeSummary, error) {
	return c.channels, nil
}

func (c *checkerChannelClient) GetChannelDetail(ctx context.Context, channelID int64) (*ChannelProbeDetail, error) {
	return c.details[channelID], nil
}

func (c *checkerChannelClient) RecordChannelHealth(ctx context.Context, channelID int64, success bool, errMsg string, responseTimeMs int64) error {
	c.healthReqs = append(c.healthReqs, struct {
		ChannelID    int64
		Success      bool
		ErrMsg       string
		ResponseTime int64
	}{
		ChannelID:    channelID,
		Success:      success,
		ErrMsg:       errMsg,
		ResponseTime: responseTimeMs,
	})
	return nil
}

func TestChannelHealthChecker_CheckOnceRecordsSuccess(t *testing.T) {
	t.Setenv("PROVIDER_DISABLE_SSRF_CHECK", "true")
	runBefore := testutil.ToFloat64(metrics.ChannelHealthCheckRunsTotal.WithLabelValues("success"))
	probeBefore := testutil.ToFloat64(metrics.ChannelHealthProbeTotal.WithLabelValues("success", "none"))
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/models" {
			t.Fatalf("path = %q, want /v1/models", r.URL.Path)
		}
		_, _ = w.Write([]byte(`{"object":"list","data":[]}`))
	}))
	defer upstream.Close()

	client := &checkerChannelClient{
		channels: []ChannelProbeSummary{{ID: 1, Type: 1, Status: 1}},
		details: map[int64]*ChannelProbeDetail{
			1: {ID: 1, Type: 1, BaseURL: upstream.URL + "/v1", Key: "sk-test"},
		},
	}
	checker := NewChannelHealthChecker(client, ChannelHealthCheckerConfig{Enabled: true, Timeout: time.Second})
	checker.CheckOnce(context.Background())

	if len(client.healthReqs) != 1 || !client.healthReqs[0].Success || client.healthReqs[0].ChannelID != 1 {
		t.Fatalf("health requests = %+v", client.healthReqs)
	}
	runAfter := testutil.ToFloat64(metrics.ChannelHealthCheckRunsTotal.WithLabelValues("success"))
	probeAfter := testutil.ToFloat64(metrics.ChannelHealthProbeTotal.WithLabelValues("success", "none"))
	assertMonitorMetricDelta(t, runBefore, runAfter, 1)
	assertMonitorMetricDelta(t, probeBefore, probeAfter, 1)
}

func TestChannelHealthChecker_CheckOnceRecordsFailure(t *testing.T) {
	t.Setenv("PROVIDER_DISABLE_SSRF_CHECK", "true")
	probeBefore := testutil.ToFloat64(metrics.ChannelHealthProbeTotal.WithLabelValues("error", "upstream_status"))
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "bad gateway", http.StatusBadGateway)
	}))
	defer upstream.Close()

	client := &checkerChannelClient{
		channels: []ChannelProbeSummary{{ID: 1, Type: 1, Status: 1}},
		details: map[int64]*ChannelProbeDetail{
			1: {ID: 1, Type: 1, BaseURL: upstream.URL + "/v1", Key: "sk-test"},
		},
	}
	checker := NewChannelHealthChecker(client, ChannelHealthCheckerConfig{Enabled: true, Timeout: time.Second})
	checker.CheckOnce(context.Background())

	if len(client.healthReqs) != 1 || client.healthReqs[0].Success || client.healthReqs[0].ChannelID != 1 {
		t.Fatalf("health requests = %+v", client.healthReqs)
	}
	probeAfter := testutil.ToFloat64(metrics.ChannelHealthProbeTotal.WithLabelValues("error", "upstream_status"))
	assertMonitorMetricDelta(t, probeBefore, probeAfter, 1)
}

func TestChannelHealthChecker_CheckOnceSkipsUnsupportedProvider(t *testing.T) {
	client := &checkerChannelClient{
		channels: []ChannelProbeSummary{{ID: 1, Type: 2, Status: 1}},
		details:  map[int64]*ChannelProbeDetail{},
	}
	checker := NewChannelHealthChecker(client, ChannelHealthCheckerConfig{Enabled: true, Timeout: time.Second})
	checker.CheckOnce(context.Background())

	if len(client.healthReqs) != 0 {
		t.Fatalf("health requests = %+v, want none", client.healthReqs)
	}
}

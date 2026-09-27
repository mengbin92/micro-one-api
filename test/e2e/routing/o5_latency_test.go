package routingtest

import (
	"fmt"
	"math"
	"net/url"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/stretchr/testify/require"
	"micro-one-api/pkg/jsonx"
)

// O5a：Redis 故障传播与降级延迟测量。复用 observability_test.go 的
// Prometheus instant query，但按标签展开（prom 只返回值），把
// micro_one_api_dependency_grpc_latency_seconds 在故障窗口前后的
// per-method 样本量与 P95 记入 state.o5，供隔离验收证据引用。

const o5Methods = `.*/(GetAuthSnapshot|GetRoutingGroup|GetRoutingCapabilities|GetRoutingEntitlements|CheckRoutingSettlement|HasRoutingCandidates)`

// o5sample captures dependency-gRPC counts and P95 for one measurement window.
type o5sample struct {
	StartScrape float64 `json:"start_scrape_unix"`
	EndScrape   float64 `json:"end_scrape_unix"`
	// Counts maps "method|status" to increase() over the window.
	Counts map[string]float64 `json:"counts"`
	// P95MS maps method to histogram_quantile(0.95) over OK samples (ms).
	P95MS map[string]float64 `json:"p95_ms"`
}

// promVec runs an instant query and returns values keyed by the given labels.
func (s *suite) promVec(query string, labels ...string) (map[string]float64, bool) {
	code, raw, err := send("GET", os.Getenv("PROMETHEUS_HTTP_BASE")+"/api/v1/query?query="+url.QueryEscape(query), "", nil, "")
	if err != nil || code != 200 {
		return nil, false
	}
	var response struct {
		Status string `json:"status"`
		Data   struct {
			Result []struct {
				Metric map[string]string  `json:"metric"`
				Value  []jsonx.RawMessage `json:"value"`
			} `json:"result"`
		} `json:"data"`
	}
	if jsonx.Unmarshal(raw, &response) != nil || response.Status != "success" {
		return nil, false
	}
	out := map[string]float64{}
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
		parts := make([]string, 0, len(labels))
		for _, label := range labels {
			parts = append(parts, row.Metric[label])
		}
		out[strings.Join(parts, "|")] = n
	}
	return out, true
}

// nextO5Scrape returns the timestamp of a scrape performed after this call.
// Each measured burst is bracketed by two such scrapes, so its range cannot
// reuse samples from the preceding phase.
func (s *suite) nextO5Scrape() float64 {
	s.t.Helper()
	scrapeAfter := float64(time.Now().UnixNano()) / float64(time.Second)
	scrapeQuery := fmt.Sprintf(`max(timestamp(micro_one_api_dependency_grpc_latency_seconds_count{instance="relay-gateway:8080",method=~"%s"}))`, o5Methods)
	var scraped float64
	require.Eventually(s.t, func() bool {
		values, ok := s.prom(scrapeQuery)
		if ok && len(values) == 1 && values[0] > scrapeAfter {
			scraped = values[0]
			return true
		}
		return false
	}, 60*time.Second, time.Second, scrapeQuery)
	return scraped
}

// recordO5 samples only between the scrape preceding a burst and the first
// scrape after it. The @ modifier fixes the end of each range to that scrape.
func (s *suite) recordO5(label string, before float64) {
	s.t.Helper()
	after := s.nextO5Scrape()
	windowSeconds := int(math.Ceil(after-before)) + 1
	require.Greater(s.t, windowSeconds, 1)
	end := strconv.FormatFloat(after, 'f', -1, 64)
	if s.state.O5 == nil {
		s.state.O5 = map[string]o5sample{}
	}
	countQuery := fmt.Sprintf(
		`sum by (method, status) (increase(micro_one_api_dependency_grpc_latency_seconds_count{instance="relay-gateway:8080",method=~"%s"}[%ds] @ %s))`,
		o5Methods, windowSeconds, end)
	p95Query := fmt.Sprintf(
		`histogram_quantile(0.95, sum by (method, le) (increase(micro_one_api_dependency_grpc_latency_seconds_bucket{instance="relay-gateway:8080",status="OK",method=~"%s"}[%ds] @ %s))) * 1000`,
		o5Methods, windowSeconds, end)
	sample := o5sample{StartScrape: before, EndScrape: after}
	counts, ok := s.promVec(countQuery, "method", "status")
	require.True(s.t, ok, "dependency grpc counts query failed: %s", countQuery)
	sample.Counts = counts
	p95, ok := s.promVec(p95Query, "method")
	require.True(s.t, ok, "dependency grpc p95 query failed: %s", p95Query)
	// histogram_quantile returns NaN when the window has no OK samples (e.g.
	// the legacy L1 absorbed every lookup during a Redis outage). NaN cannot
	// round-trip through the fixture JSON, so drop non-finite values.
	sample.P95MS = map[string]float64{}
	for method, value := range p95 {
		if !math.IsNaN(value) && !math.IsInf(value, 0) {
			sample.P95MS[method] = value
		}
	}
	s.state.O5[label] = sample
	s.t.Logf("o5a[%s]: counts=%v p95_ms=%v", label, sample.Counts, sample.P95MS)
}

// driveBurst sends n sequential chats with unique ids. Every request must
// succeed; settlement is awaited only for the final request to keep the burst
// short while still proving the path settles during the fault window.
func (s *suite) driveBurst(token, prefix string, n int) {
	s.t.Helper()
	for i := range n {
		id := fmt.Sprintf("%s-%d", prefix, i)
		s.chat(token, id, false)
		if i == n-1 {
			s.settled(id)
		}
	}
}

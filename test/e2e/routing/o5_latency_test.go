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

var o5Instances = []string{"relay-gateway:8080", "identity-service:8001", "channel-service:8002", "billing-service:8004"}

type o5scrapes struct {
	Start float64 `json:"start_scrape_unix"`
	End   float64 `json:"end_scrape_unix"`
}

// o5sample captures dependency-gRPC counts and P95 for one measurement window.
type o5sample struct {
	StartScrape float64 `json:"start_scrape_unix"`
	EndScrape   float64 `json:"end_scrape_unix"`
	// Scrapes preserves each target's own boundaries; targets scrape asynchronously.
	Scrapes map[string]o5scrapes `json:"scrapes"`
	// Counts maps "method|status" to increase() over the window.
	Counts map[string]float64 `json:"counts"`
	// P95MS maps method to histogram_quantile(0.95) over OK samples (ms).
	P95MS map[string]float64 `json:"p95_ms"`
	// ServerP95MS maps "service|method" to the SERVER-side handling P95 (ms)
	// from micro_one_api_grpc_request_duration_seconds. Read together with
	// the client-side P95MS it discriminates downstream slowdown from
	// relay-side/host contention (the open O5 attribution question: identity
	// itself has no Redis dependency on the GetAuthSnapshot path).
	ServerP95MS map[string]float64 `json:"server_p95_ms,omitempty"`
	// CPURate maps instance to rate(process_cpu_seconds_total) over the window.
	CPURate map[string]float64 `json:"cpu_rate,omitempty"`
	// Goroutines maps instance to max go_goroutines over the window.
	Goroutines map[string]float64 `json:"goroutines_max,omitempty"`
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

// nextO5Scrape waits for every measured target to finish a successful scrape
// after this call. Waiting only for relay can miss a dependency's whole burst.
func (s *suite) nextO5Scrape() map[string]float64 {
	s.t.Helper()
	scrapeAfter := float64(time.Now().UnixNano()) / float64(time.Second)
	targets := fmt.Sprintf(`up{job="micro-one-api",instance=~"%s"}`, strings.Join(o5Instances, "|"))
	// Filter after timestamp(): timestamp(up == 1) reports evaluation time,
	// not scrape time, and would accept stale samples as fresh.
	scrapeQuery := fmt.Sprintf(`timestamp(%s) and (%s == 1)`, targets, targets)
	var scraped map[string]float64
	require.Eventually(s.t, func() bool {
		values, ok := s.promVec(scrapeQuery, "instance")
		if !ok {
			return false
		}
		for _, instance := range o5Instances {
			if !(values[instance] > scrapeAfter) {
				return false
			}
		}
		scraped = values
		return true
	}, 60*time.Second, time.Second, scrapeQuery)
	return scraped
}

func o5Range(before, after float64) string {
	// Prometheus ranges exclude their left boundary. One millisecond includes
	// the baseline scrape without admitting a preceding scrape's traffic.
	return fmt.Sprintf(`[%dms] @ %s`, int64(math.Ceil((after-before)*1000))+1, strconv.FormatFloat(after, 'f', -1, 64))
}

// recordO5 uses each target's own pre/post-burst scrapes, with no look-back
// slack that could include setup traffic or a preceding fault phase.
func (s *suite) recordO5(label string, before map[string]float64) {
	s.t.Helper()
	after := s.nextO5Scrape()
	window := o5Range(before[o5Instances[0]], after[o5Instances[0]])
	if s.state.O5 == nil {
		s.state.O5 = map[string]o5sample{}
	}
	countQuery := fmt.Sprintf(
		`sum by (method, status) (increase(micro_one_api_dependency_grpc_latency_seconds_count{instance="relay-gateway:8080",method=~"%s"}%s))`,
		o5Methods, window)
	p95Query := fmt.Sprintf(
		`histogram_quantile(0.95, sum by (method, le) (increase(micro_one_api_dependency_grpc_latency_seconds_bucket{instance="relay-gateway:8080",status="OK",method=~"%s"}%s))) * 1000`,
		o5Methods, window)
	sample := o5sample{StartScrape: before[o5Instances[0]], EndScrape: after[o5Instances[0]],
		Scrapes: map[string]o5scrapes{}, ServerP95MS: map[string]float64{}, CPURate: map[string]float64{}, Goroutines: map[string]float64{}}
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
	serverMethods := map[string]bool{}
	for _, instance := range o5Instances {
		require.Greater(s.t, after[instance], before[instance], instance)
		sample.Scrapes[instance] = o5scrapes{Start: before[instance], End: after[instance]}
		window := o5Range(before[instance], after[instance])
		if instance != o5Instances[0] {
			query := fmt.Sprintf(
				`histogram_quantile(0.95, sum by (service, method, le) (increase(micro_one_api_grpc_request_duration_seconds_bucket{instance="%s",method=~"%s"}%s))) * 1000`,
				instance, o5Methods, window)
			serverP95, ok := s.promVec(query, "service", "method")
			require.True(s.t, ok, "server grpc p95 query failed: %s", query)
			for key, value := range serverP95 {
				if !math.IsNaN(value) && !math.IsInf(value, 0) {
					sample.ServerP95MS[key] = value
					_, method, _ := strings.Cut(key, "|")
					serverMethods[method] = true
				}
			}
		}
		cpuQuery := fmt.Sprintf(`rate(process_cpu_seconds_total{instance="%s"}%s)`, instance, window)
		cpu, ok := s.promVec(cpuQuery, "instance")
		require.True(s.t, ok, "cpu query failed: %s", cpuQuery)
		require.Contains(s.t, cpu, instance, "missing CPU samples")
		sample.CPURate[instance] = cpu[instance]
		goroutineQuery := fmt.Sprintf(`max_over_time(go_goroutines{instance="%s"}%s)`, instance, window)
		goroutines, ok := s.promVec(goroutineQuery, "instance")
		require.True(s.t, ok, "goroutine query failed: %s", goroutineQuery)
		require.Contains(s.t, goroutines, instance, "missing goroutine samples")
		sample.Goroutines[instance] = goroutines[instance]
	}
	for method := range sample.P95MS {
		require.True(s.t, serverMethods[method], "client measured %s but server samples are missing", method)
	}
	s.state.O5[label] = sample
	s.t.Logf("o5a[%s]: counts=%v p95_ms=%v server_p95_ms=%v cpu_rate=%v goroutines=%v",
		label, sample.Counts, sample.P95MS, sample.ServerP95MS, sample.CPURate, sample.Goroutines)
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

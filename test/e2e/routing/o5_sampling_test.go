package routingtest

import (
	"fmt"
	"io"
	"net/http"
	"regexp"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"micro-one-api/pkg/jsonx"
)

type o5Transport func(*http.Request) (*http.Response, error)

func (f o5Transport) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

func TestO5ScrapesBracketAllTargets(t *testing.T) {
	t.Setenv("PROMETHEUS_HTTP_BASE", "http://prometheus.test")
	previous := http.DefaultClient.Transport
	t.Cleanup(func() { http.DefaultClient.Transport = previous })
	// Scrapes are staggered, and each phase initially has one stale target.
	base := float64(time.Now().Unix() + 60)
	starts := map[string]float64{"relay-gateway:8080": base, "identity-service:8001": base + 2, "channel-service:8002": base + 4, "billing-service:8004": base + 6}
	ends := map[string]float64{}
	for instance, start := range starts {
		ends[instance] = start + 15
	}
	phase, polls := "before", map[string]int{}
	const method = "/api.identity.v1.IdentityService/GetAuthSnapshot"
	var ranges []string
	http.DefaultClient.Transport = o5Transport(func(r *http.Request) (*http.Response, error) {
		q := r.URL.Query().Get("query")
		rows := []object{}
		add := func(labels map[string]string, value float64) {
			rows = append(rows, object{"metric": labels, "value": []any{base, strconv.FormatFloat(value, 'f', -1, 64)}})
		}
		if strings.Contains(q, "timestamp(") {
			polls[phase]++
			timestamps := starts
			if phase == "after" {
				timestamps = ends
			}
			if strings.HasPrefix(q, "max(timestamp(") {
				add(nil, timestamps["relay-gateway:8080"])
			} else {
				for instance, timestamp := range timestamps {
					if polls[phase] == 1 && instance == "billing-service:8004" {
						timestamp = 0
					}
					add(map[string]string{"instance": instance}, timestamp)
				}
			}
		} else {
			ranges = append(ranges, q)
			switch {
			case strings.Contains(q, "dependency_grpc_latency_seconds_count"):
				add(map[string]string{"method": method, "status": "OK"}, 24)
			case strings.Contains(q, "dependency_grpc_latency_seconds_bucket"):
				add(map[string]string{"method": method}, 4.5)
			case strings.Contains(q, "grpc_request_duration_seconds_bucket") && strings.Contains(q, `instance="identity-service:8001"`):
				add(map[string]string{"service": "identity-service", "method": method}, 2.5)
			case strings.Contains(q, "process_cpu_seconds_total") || strings.Contains(q, "go_goroutines"):
				for instance := range starts {
					if strings.Contains(q, fmt.Sprintf(`instance="%s"`, instance)) {
						add(map[string]string{"instance": instance}, 0)
					}
				}
			}
		}
		raw, err := jsonx.Marshal(object{"status": "success", "data": object{"result": rows}})
		return &http.Response{StatusCode: http.StatusOK, Body: io.NopCloser(strings.NewReader(string(raw))), Header: make(http.Header)}, err
	})
	s := &suite{t: t, state: &fixture{}}
	before := s.nextO5Scrape()
	require.GreaterOrEqual(t, polls["before"], 2, "a fresh relay scrape must not hide a stale dependency")
	phase = "after"
	s.recordO5("test", before)
	require.GreaterOrEqual(t, polls["after"], 2, "wait for every dependency to capture the burst")
	require.Equal(t, 2.5, s.state.O5["test"].ServerP95MS["identity-service|"+method])
	require.Len(t, s.state.O5["test"].Scrapes, 4)
	pattern := regexp.MustCompile(`\[(\d+)(ms|s)\] @ ([\d.]+)`)
	for _, q := range ranges {
		bounds := pattern.FindStringSubmatch(q)
		require.Len(t, bounds, 4, q)
		duration, err := time.ParseDuration(bounds[1] + bounds[2])
		require.NoError(t, err)
		end, err := strconv.ParseFloat(bounds[3], 64)
		require.NoError(t, err)
		matched := false
		for instance, start := range starts {
			if strings.Contains(q, fmt.Sprintf(`instance="%s"`, instance)) {
				matched = true
				require.Equal(t, ends[instance], end, q)
				// Include this target's baseline scrape, without widening back
				// to a preceding phase or omitting the post-burst scrape.
				require.InDelta(t, start, end-duration.Seconds(), 0.002, q)
			}
		}
		require.True(t, matched, "query must use one target's own boundaries: %s", q)
	}
}

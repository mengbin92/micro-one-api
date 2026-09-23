package service

import (
	"strings"
	"testing"

	"github.com/prometheus/client_golang/prometheus/testutil"
	"micro-one-api/platform/metrics"
)

func TestProbeUsageSeparateTokenBuckets(t *testing.T) {
	for _, tt := range []struct {
		platform, body string
		input, cached  float64
	}{
		{"claude", `{"usage":{"input_tokens":3,"output_tokens":1,"cache_read_input_tokens":2}}`, 3, 2},
		{"codex", `{"usage":{"input_tokens":5,"output_tokens":1,"input_tokens_details":{"cached_tokens":2}}}`, 3, 2},
	} {
		t.Run(tt.platform, func(t *testing.T) {
			input := metrics.AccountProbeTokens.WithLabelValues(tt.platform, "input")
			cached := metrics.AccountProbeTokens.WithLabelValues(tt.platform, "cache_read")
			output := metrics.AccountProbeTokens.WithLabelValues(tt.platform, "output")
			i, c, o := testutil.ToFloat64(input), testutil.ToFloat64(cached), testutil.ToFloat64(output)
			recordProbeUsage(tt.platform, strings.NewReader(tt.body))
			if testutil.ToFloat64(input)-i != tt.input || testutil.ToFloat64(cached)-c != tt.cached || testutil.ToFloat64(output)-o != 1 {
				t.Fatal("unexpected probe usage buckets")
			}
		})
	}
	missing := metrics.AccountProbeUsage.WithLabelValues("other", "missing")
	before := testutil.ToFloat64(missing)
	recordProbeUsage("unbounded-upstream-name", strings.NewReader(`{}`))
	if testutil.ToFloat64(missing) != before+1 {
		t.Fatal("missing usage must remain explicit with bounded platform")
	}
}

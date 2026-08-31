package server

import (
	"strings"
	"testing"

	dto "github.com/prometheus/client_model/go"

	relaybiz "micro-one-api/internal/biz"
	usagepkg "micro-one-api/internal/server/usage"
	"micro-one-api/platform/metrics"
)

// TestExtractRawUsageCacheCreationBuckets verifies the Phase 1 §1.1 parsing of
// cache_creation_input_tokens + nested ephemeral_5m/1h per ADR §3.3/§4.2,
// covering the four canonical detail shapes plus the total-without-detail
// default. This is a pure parsing test (no live upstream), so it is not
// affected by the sandbox network-bind restriction on httptest.NewServer.
func TestExtractRawUsageCacheCreationBuckets(t *testing.T) {
	cases := []struct {
		name   string
		body   string
		want5m int64
		want1h int64
	}{
		{
			name:   "mixed 5m+1h detail",
			body:   `{"usage":{"input_tokens":300,"output_tokens":25,"cache_read_input_tokens":60,"cache_creation_input_tokens":110,"cache_creation":{"ephemeral_5m_input_tokens":40,"ephemeral_1h_input_tokens":70}}}`,
			want5m: 40,
			want1h: 70,
		},
		{
			name:   "5m only detail",
			body:   `{"usage":{"input_tokens":300,"output_tokens":25,"cache_read_input_tokens":60,"cache_creation_input_tokens":40,"cache_creation":{"ephemeral_5m_input_tokens":40}}}`,
			want5m: 40,
			want1h: 0,
		},
		{
			name:   "1h only detail",
			body:   `{"usage":{"input_tokens":300,"output_tokens":25,"cache_read_input_tokens":60,"cache_creation_input_tokens":70,"cache_creation":{"ephemeral_1h_input_tokens":70}}}`,
			want5m: 0,
			want1h: 70,
		},
		{
			name:   "total without detail defaults to 5m",
			body:   `{"usage":{"input_tokens":300,"output_tokens":25,"cache_read_input_tokens":60,"cache_creation_input_tokens":110}}`,
			want5m: 110,
			want1h: 0,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			u := extractRawUsage([]byte(tc.body), 0)
			if u.CacheCreation5mTokens != tc.want5m {
				t.Fatalf("5m = %d, want %d", u.CacheCreation5mTokens, tc.want5m)
			}
			if u.CacheCreation1hTokens != tc.want1h {
				t.Fatalf("1h = %d, want %d", u.CacheCreation1hTokens, tc.want1h)
			}
		})
	}
}

// TestRawStreamUsageTrackerMergesCacheCreation verifies the streaming SSE
// merge back-fills cache_creation from message_start when message_delta
// omits it (ADR §3.4), and that the final five buckets equal the non-stream
// result for the same upstream payload.
func TestRawStreamUsageTrackerMergesCacheCreation(t *testing.T) {
	tracker := newRawStreamUsageTracker(rawUsage{})
	tracker.Observe([]byte(`{"type":"message_start","message":{"id":"msg_6","usage":{"input_tokens":300,"output_tokens":1,"cache_read_input_tokens":60,"cache_creation_input_tokens":110,"cache_creation":{"ephemeral_5m_input_tokens":40,"ephemeral_1h_input_tokens":70}}}}`))
	tracker.Observe([]byte(`{"type":"message_delta","usage":{"output_tokens":25}}`))

	u := tracker.Usage()
	// ADR §2: real billing total = sum of all five canonical buckets =
	// 300 (uncached/input) + 60 (cache_read) + 40 (5m) + 70 (1h) + 25 (output).
	want := rawUsage{PromptTokens: 300, CompletionTokens: 25, CacheReadTokens: 60, CacheCreation5mTokens: 40, CacheCreation1hTokens: 70, TotalTokens: 495}
	gotShape := u.Shape
	u.Shape = usagepkg.FieldShapeSignals{}
	if u != want {
		t.Fatalf("got = %+v, want %+v", u, want)
	}
	// The accumulated shape proves anthropic_messages: message_start carried
	// cache_read_input_tokens and the nested cache_creation detail.
	if !gotShape.HasAnthropicCacheRead || !gotShape.HasAnthropicCacheCreation {
		t.Fatalf("shape = %v, want anthropic markers", gotShape)
	}
	if env := envelopeFromRawUsage(tracker.Usage()); env.Semantics != relaybiz.UsageSemanticsAnthropicExclusive {
		t.Fatalf("envelope semantics = %q, want anthropic_exclusive", env.Semantics)
	}
}

func TestRawStreamUsageTrackerAcceptsDataWithoutSpace(t *testing.T) {
	tracker := newRawStreamUsageTracker(rawUsage{})
	tracker.ObserveBytes([]byte("data:{\"response_id\":\"msg_7\",\"usage\":{\"input_tokens\":8,\"output_tokens\":3}}\n\n"))

	usage := tracker.Usage()
	if usage.PromptTokens != 8 || usage.CompletionTokens != 3 || usage.TotalTokens != 11 {
		t.Fatalf("usage = %+v", usage)
	}
	if tracker.ResponseID() != "msg_7" {
		t.Fatalf("response id = %q", tracker.ResponseID())
	}
}

// TestExtractRawUsageNegativeStaysAmbiguous verifies the remediation rule:
// an invalid bucket cannot be clamped and then trusted as verified usage.
func TestExtractRawUsageNegativeStaysAmbiguous(t *testing.T) {
	body := []byte(`{"usage":{"prompt_tokens":-10,"completion_tokens":50,"prompt_tokens_details":{"cached_tokens":-5}}}`)
	u := extractRawUsage(body, 40)
	if u.PromptTokens != -10 || u.CacheReadTokens != -5 || u.CompletionTokens != 50 {
		t.Fatalf("reported usage was sanitized before audit: %+v", u)
	}
	env := envelopeFromRawUsage(u)
	if env.ParseStatus != relaybiz.UsageParseAmbiguous || env.DecisionReason != relaybiz.UsageReasonNegativeBucket {
		t.Fatalf("status=%q reason=%q", env.ParseStatus, env.DecisionReason)
	}
}

// TestExtractRawUsageTTLDetailExceedsTotalRecordsAnomaly verifies ADR §4.2:
// when the ephemeral detail sum exceeds the flat cache_creation_input_tokens
// total, detail wins (billing unchanged) but a ttl_detail_exceeds_total
// anomaly is recorded.
func TestExtractRawUsageTTLDetailExceedsTotalRecordsAnomaly(t *testing.T) {
	before := readAnomalyCount(t, "ttl_detail_exceeds_total")
	body := []byte(`{"usage":{"input_tokens":300,"output_tokens":25,"cache_read_input_tokens":60,"cache_creation_input_tokens":50,"cache_creation":{"ephemeral_5m_input_tokens":40,"ephemeral_1h_input_tokens":70}}}`)
	u := extractRawUsage(body, 0)
	// Detail wins: 5m=40, 1h=70.
	if u.CacheCreation5mTokens != 40 || u.CacheCreation1hTokens != 70 {
		t.Fatalf("usage = %+v, want detail to win (5m=40 1h=70)", u)
	}
	after := readAnomalyCount(t, "ttl_detail_exceeds_total")
	if after <= before {
		t.Fatalf("ttl_detail_exceeds_total anomaly metric not incremented: before=%d after=%d", before, after)
	}
	env := envelopeFromRawUsage(u)
	if env.ParseStatus != relaybiz.UsageParseAmbiguous {
		t.Fatalf("inconsistent cache-creation detail must be ambiguous: %+v", env)
	}
}

func TestExtractRawUsageGeminiMetadata(t *testing.T) {
	body := []byte(`{"usageMetadata":{"promptTokenCount":10,"candidatesTokenCount":3,"totalTokenCount":13,"cachedContentTokenCount":4}}`)
	usage := extractRawUsage(body, 999)
	if usage.PromptTokens != 10 || usage.CompletionTokens != 3 || usage.TotalTokens != 13 || usage.CacheReadTokens != 4 {
		t.Fatalf("Gemini usage = %+v", usage)
	}
}

func TestExtractRawUsage_DerivedTotalIsNotReportedTotal(t *testing.T) {
	u := extractRawUsage([]byte(`{"usage":{"prompt_tokens":10,"completion_tokens":3}}`), 0)
	if u.TotalTokens != 13 || u.ReportedTotalTokens != 0 {
		t.Fatalf("usage totals = compat:%d reported:%d", u.TotalTokens, u.ReportedTotalTokens)
	}
	env := envelopeFromRawUsage(u)
	if env.Reported.TotalTokens != 0 || env.BillableTotal() != 13 {
		t.Fatalf("envelope reported/billable totals = %d/%d", env.Reported.TotalTokens, env.BillableTotal())
	}
}

// readAnomalyCount reads the current value of the
// relay_token_usage_parse_anomaly_total{reason=...} counter so tests can
// assert the parser recorded an anomaly. It does not reset the counter
// (prometheus counters are monotonic); tests compare before/after deltas.
func readAnomalyCount(t *testing.T, reason string) int64 {
	t.Helper()
	m := &dto.Metric{}
	if err := metrics.TokenUsageParseAnomaly.WithLabelValues(reason).Write(m); err != nil {
		t.Fatalf("read anomaly metric: %v", err)
	}
	if m.Counter == nil || m.Counter.Value == nil {
		return 0
	}
	return int64(*m.Counter.Value)
}

// TestRawUsageMergeBackfillsCacheCreation guards mergeRawUsage so a primary
// usage that omits cache_creation inherits it from the fallback (used by the
// streaming tracker merge).
func TestRawUsageMergeBackfillsCacheCreation(t *testing.T) {
	merged := mergeRawUsage(rawUsage{PromptTokens: 10}, rawUsage{CacheCreation5mTokens: 40, CacheCreation1hTokens: 70})
	if merged.CacheCreation5mTokens != 40 || merged.CacheCreation1hTokens != 70 {
		t.Fatalf("merge did not back-fill cache_creation: %+v", merged)
	}
}

// TestUsageLogInputCarriesCacheCreationFields is a compile-time guard that the
// two new fields exist on usageLogInput (catches accidental field removal).
func TestUsageLogInputCarriesCacheCreationFields(t *testing.T) {
	in := usageLogInput{CacheCreation5mTokens: 5, CacheCreation1hTokens: 7}
	if in.CacheCreation5mTokens != 5 || in.CacheCreation1hTokens != 7 {
		t.Fatal("usageLogInput lost its cache_creation fields")
	}
	// Touch strings to keep the import used in case future edits drop it.
	_ = strings.TrimSpace("")
}

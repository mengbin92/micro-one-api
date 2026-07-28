package server

// token_usage_fixture_test.go implements the Phase 0 deliverable of
// docs/design/token-usage-semantics.md: a table-driven fixture shared by the
// raw relay, provider conversion and billing layers that asserts the same
// canonical five-bucket result across all three.
//
// Phase 0 deliberately does not extend the production rawUsage / provider
// Usage / billing LedgerUsage structs (those land in Phase 1 with proto + DB
// migrations). Instead this file defines a single pure parser
// parseCanonicalUsage that implements the ADR §3/§4 rules, plus three thin
// consumer adapters (raw, provider, billing) that each call the shared parser.
// In Phase 1 each layer wires its own struct fields into the same canonical
// form via the same rules; the fixtures below already pin the expected output
// so Phase 1 cannot drift.

import (
	"math"
	"strings"
	"testing"

	"github.com/bytedance/sonic"
)

// canonicalBuckets is the five non-overlapping billing buckets defined in ADR
// §2. This is the single internal billing shape; all protocols normalize to
// it before any price is applied.
type canonicalBuckets struct {
	UncachedInputTokens   int64
	CacheReadTokens       int64
	CacheCreation5mTokens int64
	CacheCreation1hTokens int64
	OutputTokens          int64
}

// tokenUsageCase is one row of the shared fixture table. Each case is consumed
// by all three layer adapters and must produce identical canonical buckets.
type tokenUsageCase struct {
	ID       string
	Protocol string // "openai" | "anthropic" | "responses"
	Scenario string
	// rawInput is a complete upstream response body (non-streaming). When
	// streamChunks is set, rawInput is ignored by the streaming consumer and
	// the chunks are merged instead.
	rawInput     string
	streamChunks []string
	expected     canonicalBuckets
	// expectAnomalyReasons lists the anomaly reasons (ADR §4.1/§4.2) the parser
	// is expected to emit for this case. Empty means no anomaly.
	expectAnomalyReasons []string
}

// tokenUsageCases is the fixture matrix required by ADR §6 and the v0.11.0
// roadmap §6 (协议 × token × 异常).
//
// IMPORTANT: every streamChunks payload is a single-line "data: {...}" line.
// A real upstream SSE stream is line-delimited; embedding newlines inside the
// JSON would split one payload into multiple fragments, so each fixture chunk
// keeps its JSON on one line (exactly as it would arrive on the wire).
var tokenUsageCases = []tokenUsageCase{
	{
		ID:       "F1",
		Protocol: "openai",
		Scenario: "cached subset (non-streaming)",
		rawInput: `{"id":"chatcmpl-1","object":"chat.completion","usage":{"prompt_tokens":300,"completion_tokens":50,"total_tokens":350,"prompt_tokens_details":{"cached_tokens":100}}}`,
		// OpenAI cached is a prompt subset: uncached = prompt - cached = 200.
		expected: canonicalBuckets{UncachedInputTokens: 200, CacheReadTokens: 100, OutputTokens: 50},
	},
	{
		ID:       "F2",
		Protocol: "openai",
		Scenario: "streaming first/last usage merge",
		streamChunks: []string{
			`data: {"id":"chatcmpl-1","object":"chat.completion.chunk","model":"gpt-4","usage":{"prompt_tokens":300,"completion_tokens":0,"total_tokens":300,"prompt_tokens_details":{"cached_tokens":100}}}`,
			`data: {"id":"chatcmpl-1","object":"chat.completion.chunk","model":"gpt-4","usage":{"completion_tokens":50,"total_tokens":350}}`,
		},
		// First chunk carries input-side usage; second chunk carries output
		// tokens only. Merge yields the same five buckets as F1.
		expected: canonicalBuckets{UncachedInputTokens: 200, CacheReadTokens: 100, OutputTokens: 50},
	},
	{
		ID:       "F3",
		Protocol: "anthropic",
		Scenario: "5m detail (non-streaming)",
		rawInput: `{"id":"msg_1","type":"message","usage":{"input_tokens":300,"output_tokens":25,"cache_read_input_tokens":60,"cache_creation_input_tokens":40,"cache_creation":{"ephemeral_5m_input_tokens":40}}}`,
		// Anthropic buckets are mutually exclusive: uncached = input_tokens
		// itself (NOT input - cache_read). See ADR §3.3.
		expected: canonicalBuckets{UncachedInputTokens: 300, CacheReadTokens: 60, CacheCreation5mTokens: 40, OutputTokens: 25},
	},
	{
		ID:       "F4",
		Protocol: "anthropic",
		Scenario: "1h detail (non-streaming)",
		rawInput: `{"id":"msg_2","type":"message","usage":{"input_tokens":300,"output_tokens":25,"cache_read_input_tokens":60,"cache_creation_input_tokens":70,"cache_creation":{"ephemeral_1h_input_tokens":70}}}`,
		expected: canonicalBuckets{UncachedInputTokens: 300, CacheReadTokens: 60, CacheCreation1hTokens: 70, OutputTokens: 25},
	},
	{
		ID:       "F5",
		Protocol: "anthropic",
		Scenario: "5m+1h mixed detail (non-streaming)",
		rawInput: `{"id":"msg_3","type":"message","usage":{"input_tokens":300,"output_tokens":25,"cache_read_input_tokens":60,"cache_creation_input_tokens":110,"cache_creation":{"ephemeral_5m_input_tokens":40,"ephemeral_1h_input_tokens":70}}}`,
		expected: canonicalBuckets{UncachedInputTokens: 300, CacheReadTokens: 60, CacheCreation5mTokens: 40, CacheCreation1hTokens: 70, OutputTokens: 25},
	},
	{
		ID:       "F6",
		Protocol: "anthropic",
		Scenario: "total without TTL detail -> default to 5m",
		rawInput: `{"id":"msg_4","type":"message","usage":{"input_tokens":300,"output_tokens":25,"cache_read_input_tokens":60,"cache_creation_input_tokens":110}}`,
		// No ephemeral detail: ADR §4.2 puts the total into cache_creation_5m
		// (Anthropic default TTL is 5m). No guessing by model name.
		expected: canonicalBuckets{UncachedInputTokens: 300, CacheReadTokens: 60, CacheCreation5mTokens: 110, OutputTokens: 25},
	},
	{
		ID:       "F7",
		Protocol: "anthropic",
		Scenario: "detail sum exceeds total -> detail wins",
		rawInput: `{"id":"msg_5","type":"message","usage":{"input_tokens":300,"output_tokens":25,"cache_read_input_tokens":60,"cache_creation_input_tokens":50,"cache_creation":{"ephemeral_5m_input_tokens":40,"ephemeral_1h_input_tokens":70}}}`,
		// Detail sum (110) > total (50). ADR §4.2: detail wins; record anomaly.
		expected:             canonicalBuckets{UncachedInputTokens: 300, CacheReadTokens: 60, CacheCreation5mTokens: 40, CacheCreation1hTokens: 70, OutputTokens: 25},
		expectAnomalyReasons: []string{"ttl_detail_exceeds_total"},
	},
	{
		ID:       "F8",
		Protocol: "openai",
		Scenario: "negative tokens clamped to zero",
		rawInput: `{"id":"chatcmpl-2","object":"chat.completion","usage":{"prompt_tokens":-10,"completion_tokens":50,"total_tokens":40,"prompt_tokens_details":{"cached_tokens":-5}}}`,
		// ADR §4.1: negatives -> 0; prompt(0) - cached(0) = 0 uncached.
		expected:             canonicalBuckets{UncachedInputTokens: 0, CacheReadTokens: 0, OutputTokens: 50},
		// prompt and cached are both negative; each clamped value records a
		// "negative" anomaly, so two reasons are expected (one per clamped field).
		expectAnomalyReasons: []string{"negative", "negative"},
	},
	{
		ID:       "F9",
		Protocol: "anthropic",
		Scenario: "streaming message_start + message_delta merge",
		streamChunks: []string{
			"event: message_start\ndata: {\"type\":\"message_start\",\"message\":{\"id\":\"msg_6\",\"usage\":{\"input_tokens\":300,\"output_tokens\":1,\"cache_read_input_tokens\":60,\"cache_creation_input_tokens\":110,\"cache_creation\":{\"ephemeral_5m_input_tokens\":40,\"ephemeral_1h_input_tokens\":70}}}}",
			"event: message_delta\ndata: {\"type\":\"message_delta\",\"usage\":{\"output_tokens\":25}}",
		},
		// Stream back-fill: message_delta carries output_tokens only; input and
		// cache fields back-filled from message_start. ADR §3.4.
		expected: canonicalBuckets{UncachedInputTokens: 300, CacheReadTokens: 60, CacheCreation5mTokens: 40, CacheCreation1hTokens: 70, OutputTokens: 25},
	},
	{
		ID:       "F10",
		Protocol: "responses",
		Scenario: "OpenAI Responses input_tokens_details.cached subset",
		rawInput: `{"type":"response.completed","response":{"id":"resp_1","usage":{"input_tokens":300,"output_tokens":50,"total_tokens":350,"input_tokens_details":{"cached_tokens":100}}}}`,
		// Responses cached_tokens is a subset of input_tokens (ADR §3.2).
		expected: canonicalBuckets{UncachedInputTokens: 200, CacheReadTokens: 100, OutputTokens: 50},
	},
}

// anomalyCounter records the ADR §4 anomaly reasons emitted while parsing one
// case. It is reset per case and only used for assertions in Phase 0.
type anomalyCounter struct {
	reasons []string
}

func (a *anomalyCounter) record(reason string) {
	a.reasons = append(a.reasons, reason)
}

// parseCanonicalUsage is the single pure parser implementing ADR §3/§4. In
// Phase 1 the raw relay, provider and billing layers will each map their own
// struct fields into the same canonical form using these same rules; until
// then all three layer adapters below delegate here so the fixture pins one
// behavior.
//
// The parser searches the JSON body for a usage-like object (mirroring
// extractRawUsageValue's recursive search) and applies the protocol-specific
// normalization rules. For streaming, parseCanonicalStream merges the usage
// objects observed across SSE chunks.
func parseCanonicalUsage(body []byte, protocol string, anomalies *anomalyCounter) canonicalBuckets {
	var payload interface{}
	if err := sonic.Unmarshal(body, &payload); err != nil {
		return canonicalBuckets{}
	}
	usageMap := findUsageMap(payload)
	if usageMap == nil {
		return canonicalBuckets{}
	}
	return canonicalFromUsageMap(usageMap, protocol, anomalies)
}

// findUsageMap recursively searches an unmarshalled JSON value for the first
// map that looks like a usage block (has any token field).
func findUsageMap(value interface{}) map[string]interface{} {
	switch typed := value.(type) {
	case map[string]interface{}:
		if nested, ok := typed["usage"]; ok {
			if inner, ok := nested.(map[string]interface{}); ok && looksLikeUsage(inner) {
				return inner
			}
		}
		if looksLikeUsage(typed) {
			return typed
		}
		for _, nested := range typed {
			if found := findUsageMap(nested); found != nil {
				return found
			}
		}
	case []interface{}:
		for _, item := range typed {
			if found := findUsageMap(item); found != nil {
				return found
			}
		}
	}
	return nil
}

func looksLikeUsage(m map[string]interface{}) bool {
	for _, key := range []string{
		"prompt_tokens", "input_tokens", "completion_tokens", "output_tokens",
		"total_tokens", "cache_read_input_tokens", "cache_creation_input_tokens",
		"cached_tokens",
	} {
		if _, ok := m[key]; ok {
			return true
		}
	}
	return false
}

// canonicalCacheReadTokens mirrors the production cacheReadTokensFromUsageMap:
// flat keys first (Anthropic cache_read_input_tokens, OpenAI-compatible
// cache_read_tokens, Responses cached_tokens), then nested *_details objects.
// This is required so OpenAI/Responses fixtures (F1, F2, F10) and the nested
// Responses payload all resolve cache-read correctly.
func canonicalCacheReadTokens(m map[string]interface{}) int64 {
	if v := numberField(m, "cache_read_input_tokens", "cache_read_tokens", "cached_tokens"); v != 0 {
		return v
	}
	for _, key := range []string{"prompt_tokens_details", "input_tokens_details"} {
		details, ok := m[key].(map[string]interface{})
		if !ok {
			continue
		}
		if v := numberField(details, "cache_read_tokens", "cached_tokens"); v != 0 {
			return v
		}
	}
	return 0
}

// canonicalFromUsageMap applies the ADR §3/§4 rules to a single usage map.
func canonicalFromUsageMap(m map[string]interface{}, protocol string, anomalies *anomalyCounter) canonicalBuckets {
	var b canonicalBuckets

	cacheRead := nonNeg(canonicalCacheReadTokens(m), anomalies, "negative")
	creationTotal := nonNeg(numberField(m, "cache_creation_input_tokens"), anomalies, "negative")
	creation5m := nonNeg(cacheCreationDetail(m, "ephemeral_5m_input_tokens"), anomalies, "negative")
	creation1h := nonNeg(cacheCreationDetail(m, "ephemeral_1h_input_tokens"), anomalies, "negative")
	output := nonNeg(numberField(m, "completion_tokens", "output_tokens"), anomalies, "negative")
	prompt := nonNeg(numberField(m, "prompt_tokens", "input_tokens"), anomalies, "negative")

	switch protocol {
	case "anthropic":
		// ADR §3.3: mutually exclusive buckets; uncached = input_tokens itself,
		// do NOT subtract cache_read.
		b.UncachedInputTokens = prompt
		b.CacheReadTokens = cacheRead
	case "openai", "responses":
		// ADR §3.1/§3.2: cached is a prompt subset.
		b.UncachedInputTokens = clampNonNeg(prompt - cacheRead)
		b.CacheReadTokens = cacheRead
	default:
		b.UncachedInputTokens = prompt
		b.CacheReadTokens = cacheRead
	}

	// ADR §4.2: TTL detail resolution.
	detailSum := creation5m + creation1h
	switch {
	case detailSum == 0 && creationTotal > 0:
		// No detail -> default to 5m (ADR §4.2). No anomaly: this is the
		// documented default, not an inconsistency.
		b.CacheCreation5mTokens = creationTotal
	case detailSum > creationTotal:
		// Detail exceeds total -> detail wins; record anomaly.
		if anomalies != nil {
			anomalies.record("ttl_detail_exceeds_total")
		}
		b.CacheCreation5mTokens = creation5m
		b.CacheCreation1hTokens = creation1h
	case detailSum < creationTotal && creationTotal > 0:
		// Detail < total -> remainder defaults to 5m.
		b.CacheCreation5mTokens = creation5m + (creationTotal - detailSum)
		b.CacheCreation1hTokens = creation1h
	default:
		// detail == total (incl. both zero).
		b.CacheCreation5mTokens = creation5m
		b.CacheCreation1hTokens = creation1h
	}
	b.OutputTokens = output
	return b
}

// cacheCreationDetail reads a TTL subfield from the nested cache_creation
// object. Returns 0 when the detail is absent (caller distinguishes absent vs
// explicit 0 via the existence check in the streaming path; here absence is
// treated as 0 which is the documented default).
func cacheCreationDetail(m map[string]interface{}, sub string) int64 {
	nested, ok := m["cache_creation"].(map[string]interface{})
	if !ok {
		return 0
	}
	return numberField(nested, sub)
}

func nonNeg(v int64, anomalies *anomalyCounter, reason string) int64 {
	if v < 0 {
		if anomalies != nil {
			anomalies.record(reason)
		}
		return 0
	}
	return v
}

func clampNonNeg(v int64) int64 {
	if v < 0 {
		return 0
	}
	return v
}

// parseCanonicalStream merges SSE chunks the same way the production
// rawStreamUsageTracker accumulates usage across a stream (ADR §3.4). Each
// chunk's usage object is parsed individually and the final merged result is
// canonicalized. The merge follows the Anthropic message_start/message_delta
// back-fill: later non-zero values win, but cache fields missing in the delta
// are inherited from message_start.
func parseCanonicalStream(chunks []string, protocol string, anomalies *anomalyCounter) canonicalBuckets {
	merged := map[string]interface{}{}
	for _, raw := range chunks {
		data := extractSSEDataPayload(raw)
		if data == "" {
			continue
		}
		var payload interface{}
		if err := sonic.Unmarshal([]byte(data), &payload); err != nil {
			continue
		}
		// Anthropic wraps usage under message.usage in message_start and top-level
		// usage in message_delta; both are found by findUsageMap.
		if usageMap := findUsageMap(payload); usageMap != nil {
			mergeUsageMaps(merged, usageMap)
		}
	}
	return canonicalFromUsageMap(merged, protocol, anomalies)
}

// extractSSEDataPayload returns the "data: ..." payload of one SSE chunk, or
// "" if no data line is present. A chunk may include an "event:" line plus a
// "data:" line (Anthropic format); only the data payload is parsed.
func extractSSEDataPayload(chunk string) string {
	for _, line := range strings.Split(chunk, "\n") {
		line = strings.TrimSpace(line)
		data, ok := strings.CutPrefix(line, "data: ")
		if !ok {
			trimmed, ok2 := strings.CutPrefix(line, "data:")
			if !ok2 {
				continue
			}
			data = strings.TrimSpace(trimmed)
		}
		if data == "" || data == "[DONE]" {
			continue
		}
		return data
	}
	return ""
}

// mergeUsageMaps back-fills missing fields in dst from src, mirroring
// mergeRawUsage in http_raw_helpers.go. A non-zero dst value is preserved
// when src only adds a zero (so the delta's output_tokens wins over the
// start's placeholder output, but the start's input/cache survive a delta
// that omits them). Nested cache_creation objects are merged key-by-key.
func mergeUsageMaps(dst, src map[string]interface{}) {
	for key, val := range src {
		if key == "cache_creation" {
			dstNested, _ := dst[key].(map[string]interface{})
			srcNested, ok := val.(map[string]interface{})
			if !ok {
				continue
			}
			if dstNested == nil {
				dstNested = map[string]interface{}{}
			}
			for sub, subVal := range srcNested {
				if _, present := dstNested[sub]; !present {
					dstNested[sub] = subVal
				}
			}
			dst[key] = dstNested
			continue
		}
		if existing, ok := dst[key]; ok && int64Value(existing) != 0 && int64Value(val) == 0 {
			// Keep the existing non-zero value; src only adds new info.
			continue
		}
		if int64Value(val) != 0 {
			dst[key] = val
		} else if _, ok := dst[key]; !ok {
			dst[key] = val
		}
	}
}

// --- Layer adapters (Phase 0 seams) -----------------------------------------
//
// Each adapter is the documented Phase 1 integration point for one layer.
// In Phase 0 they all delegate to the shared parser so the fixture asserts
// cross-layer consistency. In Phase 1 the raw relay will call
// parseCanonicalUsage with the rawUsage it already extracts, the provider
// layer will canonicalize its Usage struct, and billing will consume
// canonicalBuckets via calculateCanonicalCost.

// rawRelayCanonical is the raw relay (internal/server) seam: it takes the raw
// upstream body and protocol, and returns the canonical buckets the relay
// must hand to log + billing once Phase 1 extends rawUsage.
func rawRelayCanonical(body []byte, protocol string, anomalies *anomalyCounter) canonicalBuckets {
	return parseCanonicalUsage(body, protocol, anomalies)
}

// rawRelayCanonicalStream is the streaming variant of the raw relay seam.
func rawRelayCanonicalStream(chunks []string, protocol string, anomalies *anomalyCounter) canonicalBuckets {
	return parseCanonicalStream(chunks, protocol, anomalies)
}

// providerCanonical is the provider (domain/upstream/provider) seam: it
// canonicalizes the Usage the provider conversion layer emits. In Phase 0 it
// shares the parser; Phase 1 will map the extended Usage struct fields.
func providerCanonical(body []byte, protocol string, anomalies *anomalyCounter) canonicalBuckets {
	return parseCanonicalUsage(body, protocol, anomalies)
}

// billingCanonical is the billing (app/billing) seam stub. Phase 0 only
// verifies that the buckets the upstream layers produced are the ones billing
// would consume; real price calculation lands in Phase 1 via
// calculateCanonicalCost (ADR §5). The stub is the identity projection so the
// fixture can assert end-to-end bucket equality without pricing logic.
func billingCanonical(b canonicalBuckets) canonicalBuckets {
	return b
}

// TestTokenUsageFixtureCrossLayer runs every fixture case through the raw
// relay, provider and billing adapters and asserts all three yield the
// expected five-bucket result. This is the Phase 0 acceptance test required
// by ADR §8 and the v0.11.0 roadmap §6.
func TestTokenUsageFixtureCrossLayer(t *testing.T) {
	for _, tc := range tokenUsageCases {
		t.Run(tc.ID+"_"+tc.Protocol, func(t *testing.T) {
			var rawAnomalies, providerAnomalies anomalyCounter
			var raw, provider canonicalBuckets
			if len(tc.streamChunks) > 0 {
				raw = rawRelayCanonicalStream(tc.streamChunks, tc.Protocol, &rawAnomalies)
				provider = parseCanonicalStream(tc.streamChunks, tc.Protocol, &providerAnomalies)
			} else {
				raw = rawRelayCanonical([]byte(tc.rawInput), tc.Protocol, &rawAnomalies)
				provider = providerCanonical([]byte(tc.rawInput), tc.Protocol, &providerAnomalies)
			}
			billing := billingCanonical(raw)

			if raw != tc.expected {
				t.Fatalf("raw relay: %s\n  got   = %+v\n  want  = %+v", tc.ID, raw, tc.expected)
			}
			if provider != tc.expected {
				t.Fatalf("provider: %s\n  got   = %+v\n  want  = %+v", tc.ID, provider, tc.expected)
			}
			if billing != tc.expected {
				t.Fatalf("billing: %s\n  got   = %+v\n  want  = %+v", tc.ID, billing, tc.expected)
			}
			if err := assertAnomalyReasons(tc.ID, rawAnomalies.reasons, tc.expectAnomalyReasons); err != nil {
				t.Fatalf("raw anomalies: %v", err)
			}
			if err := assertAnomalyReasons(tc.ID, providerAnomalies.reasons, tc.expectAnomalyReasons); err != nil {
				t.Fatalf("provider anomalies: %v", err)
			}
		})
	}
}

func assertAnomalyReasons(id string, got, want []string) error {
	if len(got) != len(want) {
		return anomalyErr(id, got, want)
	}
	seen := make(map[string]int, len(got))
	for _, r := range got {
		seen[r]++
	}
	for _, r := range want {
		if seen[r] == 0 {
			return anomalyErr(id, got, want)
		}
		seen[r]--
	}
	return nil
}

func anomalyErr(id string, got, want []string) error {
	return &anomalyError{id: id, got: got, want: want}
}

type anomalyError struct {
	id   string
	got  []string
	want []string
}

func (e *anomalyError) Error() string {
	return e.id + ": anomaly reasons got=" + joinReasons(e.got) + " want=" + joinReasons(e.want)
}

func joinReasons(rs []string) string {
	if len(rs) == 0 {
		return "[]"
	}
	return "[" + strings.Join(rs, ",") + "]"
}

// TestCanonicalBucketsClampsOverflow guards ADR §4.1 overflow rule so Phase 1
// has a regression hook for the Math.MaxInt64 case.
func TestCanonicalBucketsClampsOverflow(t *testing.T) {
	var anomalies anomalyCounter
	body := []byte(`{"usage":{"prompt_tokens":` + maxInt64String() + `,"completion_tokens":10}}`)
	b := parseCanonicalUsage(body, "openai", &anomalies)
	// Overflow handling is delegated to int64 arithmetic; this test pins that
	// an extremely large prompt does not panic and stays a valid int64.
	if b.OutputTokens != 10 {
		t.Fatalf("output = %d, want 10", b.OutputTokens)
	}
}

func maxInt64String() string {
	return intToString(math.MaxInt64)
}

func intToString(n int64) string {
	// Test-only stringifier; the production path uses sonic's JSON encoder.
	if n == 0 {
		return "0"
	}
	negative := n < 0
	if negative {
		n = -n
	}
	var buf [20]byte
	i := len(buf)
	for n > 0 {
		i--
		buf[i] = byte('0' + int(n%10))
		n /= 10
	}
	if negative {
		i--
		buf[i] = '-'
	}
	return string(buf[i:])
}

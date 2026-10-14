package evidence

import (
	"os"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
	"micro-one-api/pkg/jsonx"
)

const deepseekPilotUsage = `{"prompt_tokens":8,"completion_tokens":1,"total_tokens":9,"prompt_tokens_details":{"cached_tokens":0},"prompt_cache_hit_tokens":0,"prompt_cache_miss_tokens":8}`

func TestDeepSeekCandidateObservedOfficialJSON(t *testing.T) {
	raw := []byte(`{"model":"deepseek-flash","usage":` + deepseekPilotUsage + `}`)
	legacy := Parse(raw, "openai_chat")
	require.Equal(t, "unsupported", legacy.Status, "existing charged profile remains unchanged")
	candidate := ParseDeepSeekCandidate(raw)
	require.Equal(t, "verified", candidate.Status)
	require.NotNil(t, candidate.Buckets)
	require.Equal(t, [5]int64{8, 0, 0, 0, 1}, *candidate.Buckets)
}
func TestDeepSeekCandidateObservedNullUsageSSE(t *testing.T) {
	raw := []byte("data: {\"object\":\"chat.completion.chunk\",\"choices\":[{\"finish_reason\":null,\"delta\":{}}],\"usage\":null}\n\ndata: {\"object\":\"chat.completion.chunk\",\"choices\":[{\"finish_reason\":\"stop\",\"delta\":{}}],\"usage\":" + deepseekPilotUsage + "}\n\ndata: [DONE]\n\n")
	tracker := NewDeepSeekCandidateTracker()
	_, err := tracker.Write(raw)
	require.NoError(t, err)
	o, complete := tracker.Finish()
	require.True(t, complete)
	require.Equal(t, "verified", o.Status)
	require.Equal(t, [5]int64{8, 0, 0, 0, 1}, *o.Buckets)
}

func TestDeepSeekCandidateCachePartitionAndUnknownDimensions(t *testing.T) {
	for _, tc := range []struct {
		body, status string
		b            *[5]int64
	}{
		{`{"usage":{"prompt_tokens":1000,"completion_tokens":100,"total_tokens":1100,"prompt_cache_hit_tokens":600,"prompt_cache_miss_tokens":400,"prompt_tokens_details":{"cached_tokens":600}}}`, "verified", &[5]int64{400, 600, 0, 0, 100}},
		{`{"usage":{"prompt_tokens":0,"completion_tokens":0,"total_tokens":0,"prompt_cache_hit_tokens":0,"prompt_cache_miss_tokens":0}}`, "verified", &[5]int64{}},
		{`{"usage":{"prompt_tokens":8,"completion_tokens":1,"prompt_cache_hit_tokens":1,"prompt_cache_miss_tokens":8}}`, "ambiguous", nil},
		{`{"usage":{"prompt_tokens":8,"completion_tokens":1,"prompt_cache_hit_tokens":1,"prompt_cache_miss_tokens":7,"prompt_tokens_details":{"cached_tokens":0}}}`, "ambiguous", nil},
		{`{"usage":{"prompt_tokens":8,"completion_tokens":1,"prompt_cache_hit_tokens":0,"prompt_cache_miss_tokens":null}}`, "invalid", nil},
		{`{"usage":{"prompt_tokens":8,"completion_tokens":1,"prompt_cache_hit_tokens":0}}`, "missing", nil},
		{`{"usage":{"prompt_tokens":8,"completion_tokens":1,"total_tokens":10,"prompt_cache_hit_tokens":0,"prompt_cache_miss_tokens":8}}`, "ambiguous", nil},
		{`{"usage":{"prompt_tokens":8,"completion_tokens":1,"prompt_cache_hit_tokens":0,"prompt_cache_miss_tokens":8,"new_billed_dimension":3}}`, "unsupported", nil},
		{`{"usage":{"prompt_tokens":8,"completion_tokens":1,"prompt_cache_hit_tokens":0,"prompt_cache_hit_tokens":1,"prompt_cache_miss_tokens":8}}`, "invalid", nil},
		{`{"usage":{"prompt_tokens":8,"completion_tokens":1,"prompt_cache_hit_tokens":-1,"prompt_cache_miss_tokens":9}}`, "invalid", nil},
		{`{"usage":{"prompt_tokens":8,"completion_tokens":1,"prompt_cache_hit_tokens":0.5,"prompt_cache_miss_tokens":8}}`, "invalid", nil},
		{`{"usage":{"prompt_tokens":8,"completion_tokens":1,"prompt_cache_hit_tokens":"0","prompt_cache_miss_tokens":8}}`, "invalid", nil},
		{`{"usage":{"prompt_tokens":9223372036854775807,"completion_tokens":1,"prompt_cache_hit_tokens":9223372036854775807,"prompt_cache_miss_tokens":1}}`, "invalid", nil},
		{`{"usage":{"prompt_tokens":9223372036854775807,"completion_tokens":1,"total_tokens":9223372036854775807,"prompt_cache_hit_tokens":0,"prompt_cache_miss_tokens":9223372036854775807}}`, "invalid", nil},
	} {
		o := ParseDeepSeekCandidate([]byte(tc.body))
		require.Equal(t, tc.status, o.Status, tc.body)
		require.Equal(t, tc.b, o.Buckets)
		if tc.b != nil {
			require.Contains(t, string(o.Usage), "prompt_cache_hit_tokens", "original aliases must remain in evidence")
		}
	}
}

func TestDeepSeekCandidatePreservesLimitsAndRevisions(t *testing.T) {
	for _, body := range []string{
		`{"padding":"` + strings.Repeat("x", MaxFrameBytes) + `","usage":` + deepseekPilotUsage + `}`,
		`{"usage":{"prompt_tokens":8,"completion_tokens":1,"prompt_cache_hit_tokens":0,"prompt_cache_miss_tokens":8,"padding":"` + strings.Repeat("x", MaxUsageBytes) + `"}}`,
	} {
		o := ParseDeepSeekCandidate([]byte(body))
		require.Equal(t, "unobserved", o.Status)
		require.Nil(t, o.Buckets)
	}
	tracker := NewDeepSeekCandidateTracker()
	require.Equal(t, DeepSeekCandidateParser, tracker.Snapshot().ParserRevision)
	o, complete := tracker.Finish()
	require.False(t, complete)
	require.Equal(t, DeepSeekCandidateContract, o.ContractRevision)
	_, err := tracker.Write([]byte(strings.Repeat("x", MaxFrameBytes+1)))
	require.NoError(t, err)
	o, complete = tracker.Finish()
	require.False(t, complete)
	require.Equal(t, "frame_limit", o.Reason)
	require.Equal(t, DeepSeekCandidateParser, o.ParserRevision)
}
func TestDeepSeekCandidateStreamReplayNeverAddsCumulativeUsageTwice(t *testing.T) {
	terminal := `{"object":"chat.completion.chunk","choices":[{"finish_reason":"stop","delta":{}}],"usage":` + deepseekPilotUsage + `}`
	tracker := NewDeepSeekCandidateTracker()
	tracker.ObserveFrame([]byte(terminal))
	tracker.ObserveFrame([]byte(terminal))
	o, complete := tracker.Finish()
	require.True(t, complete)
	require.Equal(t, [5]int64{8, 0, 0, 0, 1}, *o.Buckets)
	bad := NewDeepSeekCandidateTracker()
	bad.ObserveFrame([]byte(`{"object":"chat.completion.chunk","choices":[{"finish_reason":"stop"}],"usage":null}`))
	o, complete = bad.Finish()
	require.False(t, complete)
	require.Equal(t, "invalid", o.Status)
}

func TestDeepSeekCandidateOfficialPilotReplay(t *testing.T) {
	raw, err := os.ReadFile("testdata/deepseek-official-pilot.json")
	require.NoError(t, err)
	var fixtures []struct {
		Name            string
		Stream          bool
		Body            string
		ExpectedBuckets [5]int64 `json:"expected_buckets"`
	}
	require.NoError(t, jsonx.Unmarshal(raw, &fixtures))
	for _, f := range fixtures {
		t.Run(f.Name, func(t *testing.T) {
			var o Observation
			if f.Stream {
				tracker := NewDeepSeekCandidateTracker()
				_, err := tracker.Write([]byte(f.Body))
				require.NoError(t, err)
				var complete bool
				o, complete = tracker.Finish()
				require.True(t, complete)
			} else {
				o = ParseDeepSeekCandidate([]byte(f.Body))
			}
			require.Equal(t, "verified", o.Status)
			require.NotNil(t, o.Buckets)
			require.Equal(t, f.ExpectedBuckets, *o.Buckets)
		})
	}
}

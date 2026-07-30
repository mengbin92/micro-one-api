package usage

import (
	"testing"

	relaybiz "micro-one-api/internal/biz"
)

func TestExtractFromJSON(t *testing.T) {
	cases := []struct {
		name            string
		body            string
		fallback        int64
		promptExclusive bool
		want            relaybiz.CanonicalUsage
	}{
		{
			name:     "openai chat completion",
			body:     `{"usage":{"prompt_tokens":10,"completion_tokens":5,"total_tokens":15}}`,
			fallback: 0,
			want: relaybiz.CanonicalUsage{
				PromptTokens:     10,
				CompletionTokens: 5,
				TotalTokens:      15,
			},
		},
		{
			name: "openai with cached tokens in details",
			body: `{"usage":{"prompt_tokens":10,"completion_tokens":5,"total_tokens":15,"prompt_tokens_details":{"cached_tokens":3}}}`,
			want: relaybiz.CanonicalUsage{
				PromptTokens:     10,
				CompletionTokens: 5,
				CacheReadTokens:  3,
				TotalTokens:      15,
			},
		},
		{
			name: "anthropic with nested cache creation",
			body: `{"usage":{"input_tokens":10,"output_tokens":5,"cache_creation_input_tokens":7,"cache_creation":{"ephemeral_5m_input_tokens":4,"ephemeral_1h_input_tokens":3}}}`,
			want: relaybiz.CanonicalUsage{
				PromptTokens:          10,
				CompletionTokens:      5,
				CacheCreation5mTokens: 4,
				CacheCreation1hTokens: 3,
				TotalTokens:           22,
			},
		},
		{
			name: "provider flattened cache creation buckets",
			body: `{"usage":{"prompt_tokens":10,"completion_tokens":5,"cache_creation_5m_tokens":4,"cache_creation_1h_tokens":3}}`,
			want: relaybiz.CanonicalUsage{
				PromptTokens:          10,
				CompletionTokens:      5,
				CacheCreation5mTokens: 4,
				CacheCreation1hTokens: 3,
				TotalTokens:           22,
			},
		},
		{
			name:     "fallback when usage missing",
			body:     `{"choices":[]}`,
			fallback: 42,
			want: relaybiz.CanonicalUsage{
				TotalTokens: 42,
			},
		},
		{
			name:            "prompt exclusive carried",
			body:            `{"usage":{"prompt_tokens":1,"completion_tokens":1}}`,
			promptExclusive: true,
			want: relaybiz.CanonicalUsage{
				PromptTokens:     1,
				CompletionTokens: 1,
				TotalTokens:      2,
				PromptExclusive:  true,
			},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := ExtractFromJSON([]byte(tc.body), tc.fallback, tc.promptExclusive)
			if got != tc.want {
				t.Fatalf("ExtractFromJSON() = %+v, want %+v", got, tc.want)
			}
		})
	}
}

func TestExtractFromJSON_NestedUsage(t *testing.T) {
	body := `{"object":"response","usage":{"input_tokens":8,"output_tokens":2,"total_tokens":10,"input_tokens_details":{"cached_tokens":1,"cache_creation_5m_tokens":2,"cache_creation_1h_tokens":1}}}`
	got := ExtractFromJSON([]byte(body), 0, false)
	want := relaybiz.CanonicalUsage{
		PromptTokens:          8,
		CompletionTokens:      2,
		CacheReadTokens:       1,
		CacheCreation5mTokens: 2,
		CacheCreation1hTokens: 1,
		TotalTokens:           10,
	}
	if got != want {
		t.Fatalf("got %+v, want %+v", got, want)
	}
}

func TestCanonicalUsageIsEmpty(t *testing.T) {
	if !(relaybiz.CanonicalUsage{}).IsEmpty() {
		t.Fatal("expected empty usage")
	}
	if (relaybiz.CanonicalUsage{TotalTokens: 1}).IsEmpty() {
		t.Fatal("expected non-empty usage")
	}
}

func TestCanonicalUsageDerivedTotal(t *testing.T) {
	u := relaybiz.CanonicalUsage{
		PromptTokens:          1,
		CompletionTokens:      2,
		CacheReadTokens:       3,
		CacheCreation5mTokens: 4,
		CacheCreation1hTokens: 5,
	}
	if got, want := u.DerivedTotal(), int64(15); got != want {
		t.Fatalf("DerivedTotal() = %d, want %d", got, want)
	}
}

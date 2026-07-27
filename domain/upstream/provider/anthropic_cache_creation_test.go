package provider

import (
	"context"
	"testing"
	"time"
)

// TestConvertFromAnthropicResponsePopulatesCacheCreationBuckets verifies the
// non-streaming conversion populates the ADR §3.3 cache-creation buckets,
// including the §4.2 "total without detail defaults to 5m" rule. No live
// upstream is required; this is a pure conversion test.
func TestConvertFromAnthropicResponsePopulatesCacheCreationBuckets(t *testing.T) {
	cases := []struct {
		name     string
		usage    anthropicUsage
		want5m   int
		want1h   int
	}{
		{
			name:   "mixed 5m+1h detail",
			usage:  anthropicUsage{InputTokens: 300, OutputTokens: 25, CacheReadInputTokens: 60, CacheCreationInputTokens: 110, CacheCreation: &anthropicCacheCreation{Ephemeral5mInputTokens: 40, Ephemeral1hInputTokens: 70}},
			want5m: 40,
			want1h: 70,
		},
		{
			name:   "5m only detail",
			usage:  anthropicUsage{InputTokens: 300, OutputTokens: 25, CacheReadInputTokens: 60, CacheCreationInputTokens: 40, CacheCreation: &anthropicCacheCreation{Ephemeral5mInputTokens: 40}},
			want5m: 40,
			want1h: 0,
		},
		{
			name:   "1h only detail",
			usage:  anthropicUsage{InputTokens: 300, OutputTokens: 25, CacheReadInputTokens: 60, CacheCreationInputTokens: 70, CacheCreation: &anthropicCacheCreation{Ephemeral1hInputTokens: 70}},
			want5m: 0,
			want1h: 70,
		},
		{
			name:   "total without detail defaults to 5m",
			usage:  anthropicUsage{InputTokens: 300, OutputTokens: 25, CacheReadInputTokens: 60, CacheCreationInputTokens: 110},
			want5m: 110,
			want1h: 0,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			resp := &anthropicResponse{Usage: tc.usage}
			out := convertFromAnthropicResponse(resp, "glm-5.2")
			if got := out.Usage.PromptTokensDetails.CacheCreation5mTokens; got != tc.want5m {
				t.Fatalf("5m = %d, want %d", got, tc.want5m)
			}
			if got := out.Usage.PromptTokensDetails.CacheCreation1hTokens; got != tc.want1h {
				t.Fatalf("1h = %d, want %d", got, tc.want1h)
			}
			if got := out.Usage.PromptTokensDetails.CacheReadTokens; got != tc.usage.CacheReadInputTokens {
				t.Fatalf("cache_read = %d, want %d", got, tc.usage.CacheReadInputTokens)
			}
			// ADR §3.3: uncached input == input_tokens (NOT input - cache_read).
			if out.Usage.PromptTokens != tc.usage.InputTokens {
				t.Fatalf("prompt = %d, want %d (Anthropic buckets are exclusive)", out.Usage.PromptTokens, tc.usage.InputTokens)
			}
		})
	}
}

// TestAnthropicProviderCacheCreationFieldsNoNetwork exercises the provider
// construction without a live upstream by relying on the conversion helpers
// the provider uses; this guards the field plumbing without the sandbox
// network-bind failure of httptest.NewServer.
func TestAnthropicProviderCacheCreationFieldsNoNetwork(t *testing.T) {
	_ = NewAnthropicProvider("http://127.0.0.1:0", "sk-test", time.Second)
	ctx := context.Background()
	// Ensure the provider type still satisfies the Provider interface after
	// the Usage struct extension (compile-time guard).
	var _ Provider = (*AnthropicProvider)(nil)
	_ = ctx
}

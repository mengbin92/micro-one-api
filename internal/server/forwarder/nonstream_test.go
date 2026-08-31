package forwarder

import (
	"testing"

	relayprovider "micro-one-api/domain/upstream/provider"
	relaybiz "micro-one-api/internal/biz"
)

func TestExtractCanonicalUsagePreservesBuckets(t *testing.T) {
	plan := &relaybiz.RelayPlan{
		Channel: &relaybiz.Channel{Type: relayprovider.ChannelTypeOpenAI},
	}
	body := []byte(`{
		"usage": {
			"prompt_tokens": 10,
			"completion_tokens": 5,
			"total_tokens": 25,
			"prompt_tokens_details": {"cached_tokens": 2},
			"cache_creation_5m_tokens": 4,
			"cache_creation_1h_tokens": 3
		}
	}`)

	env := extractCanonicalUsage(body, plan)
	if env == nil {
		t.Fatal("expected usage, got nil")
	}
	if env.ParseStatus != relaybiz.UsageParseVerified || env.Semantics != relaybiz.UsageSemanticsOpenAISubset {
		t.Fatalf("status=%q semantics=%q, want verified subset", env.ParseStatus, env.Semantics)
	}
	reported := env.Reported
	if reported.PromptTokens != 10 || reported.OutputTokens != 5 || reported.CacheReadTokens != 2 ||
		reported.CacheCreation5mTokens != 4 || reported.CacheCreation1hTokens != 3 || reported.TotalTokens != 25 {
		t.Fatalf("Reported = %+v, buckets not preserved", reported)
	}
	canonical := env.CanonicalOrZero()
	if canonical.UncachedInputTokens != 8 {
		t.Errorf("UncachedInputTokens = %d, want 8 (10-2)", canonical.UncachedInputTokens)
	}
	if canonical.CacheReadTokens != 2 || canonical.CacheCreation5mTokens != 4 || canonical.CacheCreation1hTokens != 3 || canonical.OutputTokens != 5 {
		t.Errorf("Canonical = %+v, buckets not preserved", canonical)
	}
}

// The semantics verdict comes from the response's field shape, NOT from the
// channel type (§4.2/F15): an Anthropic channel returning an OpenAI-shaped
// body yields subset, and vice versa.
func TestExtractCanonicalUsageSemanticsFromShapeNotChannel(t *testing.T) {
	anthropicPlan := &relaybiz.RelayPlan{
		Channel: &relaybiz.Channel{Type: relayprovider.ChannelTypeAnthropic},
	}
	env := extractCanonicalUsage([]byte(`{"usage":{"prompt_tokens":1,"completion_tokens":1}}`), anthropicPlan)
	if env == nil {
		t.Fatal("expected usage")
	}
	if env.ParseStatus != relaybiz.UsageParseVerified {
		t.Fatalf("ParseStatus = %q, want verified", env.ParseStatus)
	}

	openAIPlan := &relaybiz.RelayPlan{
		Channel: &relaybiz.Channel{Type: relayprovider.ChannelTypeOpenAI},
	}
	env = extractCanonicalUsage([]byte(`{"usage":{"input_tokens":130,"output_tokens":9,"cache_read_input_tokens":45056}}`), openAIPlan)
	if env == nil {
		t.Fatal("expected usage")
	}
	if env.Semantics != relaybiz.UsageSemanticsAnthropicExclusive {
		t.Fatalf("Semantics = %q, want anthropic_exclusive proven by field shape", env.Semantics)
	}
	if env.CanonicalOrZero().UncachedInputTokens != 130 {
		t.Fatalf("UncachedInputTokens = %d, want 130 (no subtraction under exclusive semantics)", env.CanonicalOrZero().UncachedInputTokens)
	}
}

func TestExtractCanonicalUsageEmpty(t *testing.T) {
	plan := &relaybiz.RelayPlan{Channel: &relaybiz.Channel{Type: relayprovider.ChannelTypeOpenAI}}
	if usage := extractCanonicalUsage([]byte(`{"choices":[]}`), plan); usage != nil {
		t.Fatalf("expected nil usage for empty body, got %+v", usage)
	}
}

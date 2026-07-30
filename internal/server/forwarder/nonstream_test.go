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

	usage := extractCanonicalUsage(body, plan)
	if usage == nil {
		t.Fatal("expected usage, got nil")
	}
	if usage.PromptTokens != 10 {
		t.Errorf("PromptTokens = %d, want 10", usage.PromptTokens)
	}
	if usage.CompletionTokens != 5 {
		t.Errorf("CompletionTokens = %d, want 5", usage.CompletionTokens)
	}
	if usage.CacheReadTokens != 2 {
		t.Errorf("CacheReadTokens = %d, want 2", usage.CacheReadTokens)
	}
	if usage.CacheCreation5mTokens != 4 {
		t.Errorf("CacheCreation5mTokens = %d, want 4", usage.CacheCreation5mTokens)
	}
	if usage.CacheCreation1hTokens != 3 {
		t.Errorf("CacheCreation1hTokens = %d, want 3", usage.CacheCreation1hTokens)
	}
	if usage.TotalTokens != 25 {
		t.Errorf("TotalTokens = %d, want 25", usage.TotalTokens)
	}
	if usage.PromptExclusive {
		t.Error("OpenAI channel should not be prompt-exclusive")
	}
}

func TestExtractCanonicalUsagePromptExclusive(t *testing.T) {
	plan := &relaybiz.RelayPlan{
		Channel: &relaybiz.Channel{Type: relayprovider.ChannelTypeAnthropic},
	}
	usage := extractCanonicalUsage([]byte(`{"usage":{"prompt_tokens":1,"completion_tokens":1}}`), plan)
	if usage == nil {
		t.Fatal("expected usage")
	}
	if !usage.PromptExclusive {
		t.Error("Anthropic channel should be prompt-exclusive")
	}
}

func TestExtractCanonicalUsageEmpty(t *testing.T) {
	plan := &relaybiz.RelayPlan{Channel: &relaybiz.Channel{Type: relayprovider.ChannelTypeOpenAI}}
	if usage := extractCanonicalUsage([]byte(`{"choices":[]}`), plan); usage != nil {
		t.Fatalf("expected nil usage for empty body, got %+v", usage)
	}
}

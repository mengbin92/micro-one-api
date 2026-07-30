package apicompat

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestResponsesToChatCompletionsPreservesCacheCreation verifies that
// Responses-API cache_creation buckets are carried through the conversion to
// Chat-Completions prompt_tokens_details so downstream extractors can see them.
func TestResponsesToChatCompletionsPreservesCacheCreation(t *testing.T) {
	resp := &ResponsesResponse{
		ID:     "resp_1",
		Object: "response",
		Status: "completed",
		Output: []ResponsesOutput{{
			Type: "message",
			Role: "assistant",
			Content: []ResponsesContentPart{{
				Type: "output_text",
				Text: "hello",
			}},
		}},
		Usage: &ResponsesUsage{
			InputTokens:  10,
			OutputTokens: 5,
			TotalTokens:  20,
			InputTokensDetails: &ResponsesInputTokensDetails{
				CachedTokens:          1,
				CacheCreation5mTokens: 3,
				CacheCreation1hTokens: 2,
			},
		},
	}

	chat := ResponsesToChatCompletions(resp, "gpt-4o-mini")
	require.NotNil(t, chat)
	require.NotNil(t, chat.Usage)
	require.NotNil(t, chat.Usage.PromptTokensDetails)
	assert.Equal(t, 1, chat.Usage.PromptTokensDetails.CachedTokens)
	assert.Equal(t, 3, chat.Usage.PromptTokensDetails.CacheCreation5mTokens)
	assert.Equal(t, 2, chat.Usage.PromptTokensDetails.CacheCreation1hTokens)
}

// TestPromptDetailsFromResponsesOmitsEmptyCacheCreation ensures the details
// object is omitted entirely when no cache-creation fields are set.
func TestPromptDetailsFromResponsesOmitsEmptyCacheCreation(t *testing.T) {
	details := promptDetailsFromResponses(&ResponsesInputTokensDetails{CachedTokens: 2})
	require.NotNil(t, details)
	assert.Equal(t, 2, details.CachedTokens)
	assert.Equal(t, 0, details.CacheCreation5mTokens)
	assert.Equal(t, 0, details.CacheCreation1hTokens)

	if promptDetailsFromResponses(&ResponsesInputTokensDetails{}) != nil {
		t.Fatal("expected nil details when all fields are zero")
	}
}

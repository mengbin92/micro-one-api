package provider

import "testing"

func TestEstimateChatTokensPrefersCompletionLimit(t *testing.T) {
	old, next := 100, 20
	r := &ChatCompletionsRequest{Messages: []Message{{Content: "12345678"}}, MaxTokens: &old, MaxCompletionTokens: &next}
	if got := EstimateChatTokens(r); got != 22 {
		t.Fatalf("estimate=%d", got)
	}
	r.MaxTokens = nil
	r.MaxCompletionTokens = nil
	if got := EstimateChatTokens(r); got != DefaultEstimatedOutputTokens+2 {
		t.Fatalf("default=%d", got)
	}
}

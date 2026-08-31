package server

import (
	"testing"

	relaybiz "micro-one-api/internal/biz"
)

func TestApplyModelWhitelistCaseInsensitive(t *testing.T) {
	s := &EnhancedHTTPServer{}

	available := []string{"gpt-4o", "claude-3-5-sonnet", "glm-4.5"}
	allowed := []string{"GPT-4O", "Claude-3-5-Sonnet"}

	filtered := s.applyModelWhitelist(available, allowed)

	if len(filtered) != 2 {
		t.Fatalf("expected 2 filtered models, got %d: %v", len(filtered), filtered)
	}

	seen := make(map[string]bool)
	for _, m := range filtered {
		seen[m] = true
	}
	if !seen["gpt-4o"] {
		t.Error("expected gpt-4o in filtered results")
	}
	if !seen["claude-3-5-sonnet"] {
		t.Error("expected claude-3-5-sonnet in filtered results")
	}
}

func TestApplyModelWhitelistEmpty(t *testing.T) {
	s := &EnhancedHTTPServer{}

	available := []string{"gpt-4o", "claude-3-5-sonnet"}

	// Empty allowed list means all models are allowed.
	filtered := s.applyModelWhitelist(available, nil)

	if len(filtered) != 2 {
		t.Fatalf("expected 2 models (no whitelist), got %d", len(filtered))
	}
}

func TestEnhancedModelMatchingIgnoresCaseAndExtendedContextSuffix(t *testing.T) {
	s := &EnhancedHTTPServer{}
	if !s.isModelAllowed([]string{"deepseek-v4-pro-0813"}, "DeepSeek-V4-Pro-0813[1M]") {
		t.Fatal("expected model to be allowed")
	}
	filtered := s.applyModelWhitelist(
		[]string{"deepseek-v4-pro-0813"},
		[]string{"DeepSeek-V4-Pro-0813[1M]"},
	)
	if len(filtered) != 1 || filtered[0] != "deepseek-v4-pro-0813" {
		t.Fatalf("filtered models = %v, want deepseek model", filtered)
	}
}

// Ensure relaybiz remains available to package-level compatibility tests.
var _ = relaybiz.RelayRequest{}

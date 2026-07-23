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

// Ensure relaybiz is referenced to avoid unused import.
var _ = relaybiz.RelayRequest{}

package server

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestShouldUseRelayOrchestratorRequiresEnabledAllowlistedPost(t *testing.T) {
	s := NewHTTPServer(nil, nil, nil, nil, nil)
	s.SetRelayOrchestratorEnabled(true)
	s.SetRelayOrchestratorTokenAllowlist([]string{
		sha256Hex("staging-token"),
		"not-a-digest",
	})

	tests := []struct {
		name   string
		method string
		auth   string
		want   bool
	}{
		{name: "allowlisted token", method: http.MethodPost, auth: "Bearer staging-token", want: true},
		{name: "unknown token", method: http.MethodPost, auth: "Bearer other-token", want: false},
		{name: "missing token", method: http.MethodPost, want: false},
		{name: "wrong method", method: http.MethodGet, auth: "Bearer staging-token", want: false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			req := httptest.NewRequest(tt.method, "/v1/chat/completions", nil)
			if tt.auth != "" {
				req.Header.Set("Authorization", tt.auth)
			}
			if got := s.shouldUseRelayOrchestrator(req); got != tt.want {
				t.Fatalf("shouldUseRelayOrchestrator() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestSetRelayOrchestratorTokenAllowlistNormalizesAndRejectsInvalidDigests(t *testing.T) {
	s := NewHTTPServer(nil, nil, nil, nil, nil)
	digest := sha256Hex("staging-token")
	s.SetRelayOrchestratorTokenAllowlist([]string{"  " + digest + "  ", "xyz"})

	if len(s.relayOrchestratorTokenAllowlist) != 1 {
		t.Fatalf("allowlist size = %d, want 1", len(s.relayOrchestratorTokenAllowlist))
	}
	if s.shouldUseRelayOrchestrator(httptest.NewRequest(http.MethodPost, "/v1/chat/completions", nil)) {
		t.Fatal("request without an allowlisted token entered the orchestrator route")
	}
}

func TestRelayOrchestratorGateDefaultsOffAndAllowlistClearRollsBack(t *testing.T) {
	s := NewHTTPServer(nil, nil, nil, nil, nil)
	request := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", nil)
	request.Header.Set("Authorization", "Bearer staging-token")

	if s.relayOrchestratorEnabled {
		t.Fatal("relay orchestrator is enabled by default")
	}
	if s.shouldUseRelayOrchestrator(request) {
		t.Fatal("default-off gate selected the orchestrator")
	}

	s.SetRelayOrchestratorEnabled(true)
	s.SetRelayOrchestratorTokenAllowlist([]string{sha256Hex("staging-token")})
	if !s.shouldUseRelayOrchestrator(request) {
		t.Fatal("enabled allowlisted gate did not select the orchestrator")
	}

	// Clearing the allowlist is the one-click rollback for a live staging
	// cohort: even with the feature flag still enabled, no request can enter
	// the staged path.
	s.SetRelayOrchestratorTokenAllowlist(nil)
	if s.shouldUseRelayOrchestrator(request) {
		t.Fatal("cleared allowlist still selected the orchestrator")
	}
}

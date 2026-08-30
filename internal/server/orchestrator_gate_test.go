package server

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/prometheus/client_golang/prometheus/testutil"

	"micro-one-api/platform/metrics"
)

const relayOrchestratorTestHMACKey = "test-service-token"

func relayOrchestratorTestDigest(token string) string {
	return hmacSHA256Hex([]byte(relayOrchestratorTestHMACKey), token)
}

func TestShouldUseRelayOrchestratorRequiresEnabledAllowlistedPost(t *testing.T) {
	s := NewHTTPServer(nil, nil, nil, nil, nil)
	s.SetRelayOrchestratorEnabled(true)
	s.SetRelayOrchestratorTokenHMACAllowlist(relayOrchestratorTestHMACKey, []string{
		relayOrchestratorTestDigest("staging-token"),
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

func TestRelayOrchestratorHandlerRecordsExecutionPath(t *testing.T) {
	s := NewHTTPServer(nil, nil, nil, nil, nil)

	legacyBefore := testutil.ToFloat64(metrics.RelayExecutorRequestsTotal.WithLabelValues(
		relayEndpointChatCompletions, relayStreamUnknown, relayExecutionPathLegacy, "405", "error",
	))
	legacyReq := httptest.NewRequest(http.MethodGet, "/v1/chat/completions", nil)
	legacyRec := httptest.NewRecorder()
	s.relayOrchestratorChatHandler(legacyRec, legacyReq)
	if legacyRec.Code != http.StatusMethodNotAllowed {
		t.Fatalf("legacy status = %d, want %d", legacyRec.Code, http.StatusMethodNotAllowed)
	}
	if got := testutil.ToFloat64(metrics.RelayExecutorRequestsTotal.WithLabelValues(
		relayEndpointChatCompletions, relayStreamUnknown, relayExecutionPathLegacy, "405", "error",
	)); got != legacyBefore+1 {
		t.Fatalf("legacy execution metric = %v, want %v", got, legacyBefore+1)
	}

	s.SetRelayOrchestratorEnabled(true)
	s.SetRelayOrchestratorTokenHMACAllowlist(relayOrchestratorTestHMACKey, []string{relayOrchestratorTestDigest("staging-token")})
	orchestratorBefore := testutil.ToFloat64(metrics.RelayExecutorRequestsTotal.WithLabelValues(
		relayEndpointChatCompletions, "false", relayExecutionPathOrchestrator, "503", "error",
	))
	orchestratorReq := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", strings.NewReader(
		`{"model":"test-model","messages":[{"role":"user","content":"ping"}]}`,
	))
	orchestratorReq.Header.Set("Authorization", "Bearer staging-token")
	orchestratorRec := httptest.NewRecorder()
	s.relayOrchestratorChatHandler(orchestratorRec, orchestratorReq)
	if orchestratorRec.Code != http.StatusServiceUnavailable {
		t.Fatalf("orchestrator status = %d, want %d", orchestratorRec.Code, http.StatusServiceUnavailable)
	}
	if got := testutil.ToFloat64(metrics.RelayExecutorRequestsTotal.WithLabelValues(
		relayEndpointChatCompletions, "false", relayExecutionPathOrchestrator, "503", "error",
	)); got != orchestratorBefore+1 {
		t.Fatalf("orchestrator execution metric = %v, want %v", got, orchestratorBefore+1)
	}
}

func TestSetRelayOrchestratorTokenHMACAllowlistNormalizesAndRejectsInvalidInputs(t *testing.T) {
	s := NewHTTPServer(nil, nil, nil, nil, nil)
	s.SetRelayOrchestratorEnabled(true)
	digest := relayOrchestratorTestDigest("staging-token")
	s.SetRelayOrchestratorTokenHMACAllowlist(relayOrchestratorTestHMACKey, []string{"  " + digest + "  ", "xyz"})

	if len(s.relayOrchestratorTokenHMACAllowlist) != 1 {
		t.Fatalf("allowlist size = %d, want 1", len(s.relayOrchestratorTokenHMACAllowlist))
	}
	request := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", nil)
	request.Header.Set("Authorization", "Bearer staging-token")
	if !s.shouldUseRelayOrchestrator(request) {
		t.Fatal("valid keyed digest did not allow the staging token")
	}

	s.SetRelayOrchestratorTokenHMACAllowlist("different-key", []string{digest})
	if s.shouldUseRelayOrchestrator(request) {
		t.Fatal("digest generated with another HMAC key allowed the staging token")
	}

	request.Header.Del("Authorization")
	if s.shouldUseRelayOrchestrator(request) {
		t.Fatal("request without an allowlisted token entered the orchestrator route")
	}

	s.SetRelayOrchestratorTokenHMACAllowlist("", []string{digest})
	if len(s.relayOrchestratorTokenHMACKey) != 0 || len(s.relayOrchestratorTokenHMACAllowlist) != 0 {
		t.Fatal("empty HMAC key did not clear the allowlist")
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
	s.SetRelayOrchestratorTokenHMACAllowlist(relayOrchestratorTestHMACKey, []string{relayOrchestratorTestDigest("staging-token")})
	if !s.shouldUseRelayOrchestrator(request) {
		t.Fatal("enabled allowlisted gate did not select the orchestrator")
	}

	// Clearing the allowlist is the one-click rollback for a live staging
	// cohort: even with the feature flag still enabled, no request can enter
	// the staged path.
	s.SetRelayOrchestratorTokenHMACAllowlist(relayOrchestratorTestHMACKey, nil)
	if s.shouldUseRelayOrchestrator(request) {
		t.Fatal("cleared allowlist still selected the orchestrator")
	}
}

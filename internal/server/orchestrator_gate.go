package server

import (
	"crypto/sha256"
	"encoding/hex"
	"net/http"
	"strings"
)

// relayOrchestratorChatHandler keeps the staging route opt-in per request.
// Requests that do not match the configured token allowlist remain on the
// legacy handler, so disabling or narrowing the allowlist is an immediate
// rollback without changing the billing or storage layers.
func (s *HTTPServer) relayOrchestratorChatHandler(w http.ResponseWriter, r *http.Request) {
	path := relayExecutionPathLegacy
	handler := s.handleChatCompletions
	if s.shouldUseRelayOrchestrator(r) {
		path = relayExecutionPathOrchestrator
		handler = s.handleChatCompletionsWithOrchestrator
	}
	serveObservedRelay(w, r, relayEndpointChatCompletions, path, relayStreamUnknown, handler)
}

func (s *HTTPServer) relayOrchestratorResponsesHandler(w http.ResponseWriter, r *http.Request) {
	path := relayExecutionPathLegacy
	stream := relayStreamUnknown
	handler := s.handleResponsesRelay
	if isOpenAIWSUpgradeRequest(r) {
		stream = "true"
	} else if r.Method == http.MethodPost && r.URL.Path == relayEndpointResponses && s.isRelayOrchestratorTokenAllowed(extractAPIKey(r)) {
		path = relayExecutionPathOrchestrator
		handler = s.handleResponsesWithOrchestrator
	}
	serveObservedRelay(w, r, relayEndpointResponses, path, stream, handler)
}

func (s *HTTPServer) relayOrchestratorMessagesHandler(w http.ResponseWriter, r *http.Request) {
	path := relayExecutionPathLegacy
	handler := s.handleAnthropicMessages
	if r.Method == http.MethodPost && s.isRelayOrchestratorTokenAllowed(extractAPIKey(r)) {
		path = relayExecutionPathOrchestrator
		handler = s.handleAnthropicMessagesWithOrchestrator
	}
	serveObservedRelay(w, r, relayEndpointMessages, path, relayStreamUnknown, handler)
}

func (s *HTTPServer) shouldUseRelayOrchestrator(r *http.Request) bool {
	if s == nil || !s.relayOrchestratorEnabled || r == nil || r.Method != http.MethodPost {
		return false
	}
	token, err := bearerTokenFromRequest(r)
	if err != nil {
		return false
	}
	return s.isRelayOrchestratorTokenAllowed(token)
}

func (s *HTTPServer) isRelayOrchestratorTokenAllowed(token string) bool {
	if s == nil || !s.relayOrchestratorEnabled || len(s.relayOrchestratorTokenAllowlist) == 0 || strings.TrimSpace(token) == "" {
		return false
	}
	_, ok := s.relayOrchestratorTokenAllowlist[sha256Hex(token)]
	return ok
}

func sha256Hex(value string) string {
	digest := sha256.Sum256([]byte(value))
	return hex.EncodeToString(digest[:])
}

// SetRelayOrchestratorTokenAllowlist configures SHA-256 bearer-token digests
// allowed to enter the staging executor route. Invalid digests are ignored so
// a malformed config entry cannot accidentally broaden the route.
func (s *HTTPServer) SetRelayOrchestratorTokenAllowlist(digests []string) {
	if s == nil {
		return
	}
	allowlist := make(map[string]struct{}, len(digests))
	for _, digest := range digests {
		digest = strings.ToLower(strings.TrimSpace(digest))
		if len(digest) != sha256.Size*2 {
			continue
		}
		if _, err := hex.DecodeString(digest); err != nil {
			continue
		}
		allowlist[digest] = struct{}{}
	}
	s.relayOrchestratorTokenAllowlist = allowlist
}

package server

import (
	"crypto/hmac"
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
	if s == nil || !s.relayOrchestratorEnabled || len(s.relayOrchestratorTokenHMACKey) == 0 || len(s.relayOrchestratorTokenHMACAllowlist) == 0 || strings.TrimSpace(token) == "" {
		return false
	}
	_, ok := s.relayOrchestratorTokenHMACAllowlist[hmacSHA256Hex(s.relayOrchestratorTokenHMACKey, token)]
	return ok
}

func hmacSHA256Hex(key []byte, value string) string {
	mac := hmac.New(sha256.New, key)
	_, _ = mac.Write([]byte(value))
	return hex.EncodeToString(mac.Sum(nil))
}

// SetRelayOrchestratorTokenHMACAllowlist configures HMAC-SHA256 bearer-token
// digests keyed by SERVICE_TOKEN. Invalid inputs fail closed so configuration
// disclosure alone cannot enable offline guessing of an allowlisted token.
func (s *HTTPServer) SetRelayOrchestratorTokenHMACAllowlist(key string, digests []string) {
	if s == nil {
		return
	}
	s.relayOrchestratorTokenHMACKey = nil
	s.relayOrchestratorTokenHMACAllowlist = nil
	if key == "" {
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
	if len(allowlist) == 0 {
		return
	}
	s.relayOrchestratorTokenHMACKey = append([]byte(nil), key...)
	s.relayOrchestratorTokenHMACAllowlist = allowlist
}

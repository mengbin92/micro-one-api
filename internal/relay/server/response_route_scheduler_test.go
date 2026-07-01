package server

import (
	"net/http/httptest"
	"strings"
	"testing"
)

func TestExtractPreviousResponseIDRejectsMessageIDs(t *testing.T) {
	got := extractPreviousResponseID([]byte(`{"previous_response_id":"msg_123"}`))
	if got != "" {
		t.Fatalf("expected empty result for message id, got %q", got)
	}
}

func TestExtractPreviousResponseIDAcceptsResponseIDs(t *testing.T) {
	got := extractPreviousResponseID([]byte(`{"previous_response_id":"resp_123"}`))
	if got != "resp_123" {
		t.Fatalf("expected resp_123, got %q", got)
	}
}

func TestExtractSessionHashFromBody(t *testing.T) {
	got := extractSessionHash([]byte(`{"session_hash":"session-a"}`))
	if got != "session-a" {
		t.Fatalf("expected session-a, got %q", got)
	}

	got = extractSessionHash([]byte(`{"sessionHash":"session-b"}`))
	if got != "session-b" {
		t.Fatalf("expected session-b, got %q", got)
	}
}

func TestExtractSessionHashFromRequestPrefersHeader(t *testing.T) {
	req := httptest.NewRequest("POST", "/v1/responses", strings.NewReader(`{"session_hash":"body-session"}`))
	req.Header.Set("X-Session-Hash", "header-session")

	got := extractSessionHashFromRequest(req, []byte(`{"session_hash":"body-session"}`))
	if got != "header-session" {
		t.Fatalf("expected header-session, got %q", got)
	}
}

func TestLookupResponseRouteWithStickyRejectsNonResponseIDs(t *testing.T) {
	srv := &HTTPServer{}
	if _, ok := srv.lookupResponseRouteWithSticky(nil, "token", "msg_123"); ok {
		t.Fatal("expected non-response id to be rejected")
	}
}

func TestLookupResponseRouteWithStickyPrefersLocalRoute(t *testing.T) {
	srv := &HTTPServer{
		responseRoutes: map[string]responseRoute{
			"resp_123": {Model: "gpt-5"},
		},
	}
	route, ok := srv.lookupResponseRouteWithSticky(nil, "token", "resp_123")
	if !ok {
		t.Fatal("expected local route hit")
	}
	if route.Model != "gpt-5" {
		t.Fatalf("route model = %q, want gpt-5", route.Model)
	}
}

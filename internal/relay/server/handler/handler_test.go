package handler

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"micro-one-api/internal/relay/server"
)

type stubOrchestrator struct {
	req *server.RelayRequest
}

func (o *stubOrchestrator) Execute(_ context.Context, req *server.RelayRequest) (*server.RelayResult, error) {
	o.req = req
	return &server.RelayResult{
		Response:   io.NopCloser(strings.NewReader(`{"id":"chatcmpl-test"}`)),
		Headers:    http.Header{"Content-Type": []string{"application/json"}},
		StatusCode: http.StatusCreated,
	}, nil
}

func TestChatHandlerWritesOrchestratorResponse(t *testing.T) {
	orchestrator := &stubOrchestrator{}
	handler := NewChatHandler(orchestrator)
	req := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", strings.NewReader(`{"model":"gpt-4o-mini","messages":[{"role":"user","content":"hi"}]}`))
	req.Header.Set("Authorization", "Bearer test-token")
	rec := httptest.NewRecorder()

	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusCreated {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusCreated)
	}
	if strings.TrimSpace(rec.Body.String()) != `{"id":"chatcmpl-test"}` {
		t.Fatalf("body = %q", rec.Body.String())
	}
	if orchestrator.req == nil || orchestrator.req.Body == nil {
		t.Fatalf("orchestrator request/body was not populated")
	}
	body, err := io.ReadAll(orchestrator.req.Body)
	if err != nil {
		t.Fatalf("read orchestrator body: %v", err)
	}
	if !strings.Contains(string(body), `"model":"gpt-4o-mini"`) {
		t.Fatalf("orchestrator body = %s", string(body))
	}
}

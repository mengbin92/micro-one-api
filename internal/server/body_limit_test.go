package server

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	khttp "github.com/go-kratos/kratos/v3/transport/http"
)

func TestReadRequestBodyDetectsOversize(t *testing.T) {
	req := httptest.NewRequest("POST", "/v1/chat/completions", strings.NewReader("12345"))
	if _, err := readRequestBody(req, 4); errRequestBodyTooLarge != err {
		t.Fatalf("error = %v, want request body too large", err)
	}
}

func TestReadRequestBodyAcceptsExactLimit(t *testing.T) {
	req := httptest.NewRequest("POST", "/v1/chat/completions", strings.NewReader("1234"))
	body, err := readRequestBody(req, 4)
	if err != nil {
		t.Fatalf("readRequestBody: %v", err)
	}
	if string(body) != "1234" {
		t.Fatalf("body = %q, want 1234", body)
	}
}

func TestDecodeJSONMapsMaxBytesReaderError(t *testing.T) {
	req := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", strings.NewReader("12345"))
	req.Body = http.MaxBytesReader(httptest.NewRecorder(), req.Body, 4)

	if err := decodeJSON(req.Body, &map[string]any{}); errRequestBodyTooLarge != err {
		t.Fatalf("error = %v, want request body too large", err)
	}
}

func TestRegisteredChatRouteRejectsOversizedDeclaredBody(t *testing.T) {
	srv := khttp.NewServer()
	NewHTTPServer(nil, nil, nil, nil, nil).RegisterRoutes(srv)
	req := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", strings.NewReader("{}"))
	req.ContentLength = jsonRequestBodyLimit + 1
	rec := httptest.NewRecorder()

	srv.ServeHTTP(rec, req)

	if rec.Code != http.StatusRequestEntityTooLarge {
		t.Fatalf("status = %d, want 413", rec.Code)
	}
	if body := rec.Body.String(); !strings.Contains(body, `"code":413`) {
		t.Fatalf("declared-length error body = %s", body)
	}
}

func TestRegisteredChatRouteRejectsOversizedChunkedBody(t *testing.T) {
	srv := khttp.NewServer()
	NewHTTPServer(nil, nil, nil, nil, nil).RegisterRoutes(srv)
	body := strings.NewReader(strings.Repeat("x", int(jsonRequestBodyLimit)+1))
	req := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", body)
	req.ContentLength = -1
	req.Header.Set("Authorization", "Bearer test-token")
	rec := httptest.NewRecorder()

	srv.ServeHTTP(rec, req)

	if rec.Code != http.StatusRequestEntityTooLarge {
		t.Fatalf("status = %d, want 413; body=%s", rec.Code, rec.Body.String())
	}
	if body := rec.Body.String(); !strings.Contains(body, `"code":413`) {
		t.Fatalf("chunked error body = %s", body)
	}
}

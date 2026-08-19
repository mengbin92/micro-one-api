package middleware

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestRequestBodyLimitForPath(t *testing.T) {
	tests := []struct {
		path string
		want int64
	}{
		{path: "/v1/chat/completions", want: JSONRequestBodyLimit},
		{path: "/v1/messages", want: JSONRequestBodyLimit},
		{path: "/v1/responses/resp_1", want: JSONRequestBodyLimit},
		{path: "/v1/completions", want: JSONRequestBodyLimit},
		{path: "/v1/images/generations", want: JSONRequestBodyLimit},
		{path: "/v1/audio/speech", want: JSONRequestBodyLimit},
		{path: "/v1/audio/transcriptions", want: LargeRequestBodyLimit},
		{path: "/v1/oneapi/proxy/42/chat", want: LargeRequestBodyLimit},
		{path: "/v1/models", want: DefaultMaxBodySize},
	}
	for _, tt := range tests {
		t.Run(tt.path, func(t *testing.T) {
			if got := RequestBodyLimitForPath(tt.path); got != tt.want {
				t.Fatalf("limit = %d, want %d", got, tt.want)
			}
		})
	}
}

func TestRequestBodyLimitByPathRejectsContentLength(t *testing.T) {
	body := strings.NewReader(strings.Repeat("x", int(JSONRequestBodyLimit)+1))
	req := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", body)
	rec := httptest.NewRecorder()
	called := false
	handler := RequestBodyLimitByPath(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		called = true
	}))

	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusRequestEntityTooLarge {
		t.Fatalf("status = %d, want 413", rec.Code)
	}
	if called {
		t.Fatal("next handler was called for oversized content length")
	}
}

func TestRequestBodyLimitByPathPreservesAnthropicErrorEnvelope(t *testing.T) {
	req := httptest.NewRequest(http.MethodPost, "/v1/messages", strings.NewReader("{}"))
	req.ContentLength = JSONRequestBodyLimit + 1
	rec := httptest.NewRecorder()

	RequestBodyLimitByPath(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		t.Fatal("next handler was called")
	})).ServeHTTP(rec, req)

	if rec.Code != http.StatusRequestEntityTooLarge {
		t.Fatalf("status = %d, want 413", rec.Code)
	}
	if body := rec.Body.String(); !strings.Contains(body, `"type":"error"`) || !strings.Contains(body, `"type":"invalid_request_error"`) {
		t.Fatalf("unexpected Anthropic error body: %s", body)
	}
}

func TestRequestBodyLimitByPathWrapsChunkedBody(t *testing.T) {
	body := strings.NewReader(strings.Repeat("x", int(JSONRequestBodyLimit)+1))
	req := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", body)
	req.ContentLength = -1
	rec := httptest.NewRecorder()
	handler := RequestBodyLimitByPath(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, err := io.ReadAll(r.Body)
		if err == nil {
			t.Error("expected MaxBytesReader error")
		}
	}))

	handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want handler status 200", rec.Code)
	}
}

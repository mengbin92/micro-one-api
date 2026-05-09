package provider

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestOpenAIProviderForward(t *testing.T) {
	t.Setenv("PROVIDER_DISABLE_SSRF_CHECK", "true")

	var gotPath string
	var gotQuery string
	var gotAuth string
	var gotContentType string
	var gotBody string

	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		gotQuery = r.URL.RawQuery
		gotAuth = r.Header.Get("Authorization")
		gotContentType = r.Header.Get("Content-Type")
		body, err := io.ReadAll(r.Body)
		if err != nil {
			t.Fatalf("read body: %v", err)
		}
		gotBody = string(body)

		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("X-Upstream", "ok")
		w.WriteHeader(http.StatusCreated)
		_, _ = w.Write([]byte(`{"id":"resp-1"}`))
	}))
	defer upstream.Close()

	provider, err := NewOpenAIProvider(upstream.URL+"/v1", "sk-upstream", time.Second)
	if err != nil {
		t.Fatalf("NewOpenAIProvider: %v", err)
	}

	resp, err := provider.Forward(context.Background(), &RawRequest{
		Method: http.MethodPost,
		Path:   "/embeddings",
		Query:  "trace=1",
		Header: http.Header{
			"Authorization": []string{"Bearer caller-token"},
			"Content-Type":  []string{"application/json"},
			"X-Request-ID":  []string{"req-1"},
		},
		Body: []byte(`{"model":"text-embedding-ada-002","input":"hello"}`),
	})
	if err != nil {
		t.Fatalf("Forward returned error: %v", err)
	}

	if gotPath != "/v1/embeddings" {
		t.Fatalf("path = %q, want /v1/embeddings", gotPath)
	}
	if gotQuery != "trace=1" {
		t.Fatalf("query = %q, want trace=1", gotQuery)
	}
	if gotAuth != "Bearer sk-upstream" {
		t.Fatalf("auth = %q, want provider key", gotAuth)
	}
	if gotContentType != "application/json" {
		t.Fatalf("content-type = %q", gotContentType)
	}
	if gotBody != `{"model":"text-embedding-ada-002","input":"hello"}` {
		t.Fatalf("body = %q", gotBody)
	}
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("status = %d", resp.StatusCode)
	}
	if string(resp.Body) != `{"id":"resp-1"}` {
		t.Fatalf("body = %q", string(resp.Body))
	}
	if resp.Header.Get("X-Upstream") != "ok" {
		t.Fatalf("missing response header")
	}
}

func TestOpenAIProviderForwardReturnsErrorForNon2xx(t *testing.T) {
	t.Setenv("PROVIDER_DISABLE_SSRF_CHECK", "true")

	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "bad upstream", http.StatusBadGateway)
	}))
	defer upstream.Close()

	provider, err := NewOpenAIProvider(upstream.URL, "sk-upstream", time.Second)
	if err != nil {
		t.Fatalf("NewOpenAIProvider: %v", err)
	}

	_, err = provider.Forward(context.Background(), &RawRequest{
		Method: http.MethodPost,
		Path:   "/moderations",
		Body:   []byte(`{"input":"hello"}`),
	})
	if err == nil {
		t.Fatalf("expected error")
	}
	if !strings.Contains(err.Error(), "status=502") {
		t.Fatalf("error = %q, want status=502", err.Error())
	}
}

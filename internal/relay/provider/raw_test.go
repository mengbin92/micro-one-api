package provider

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
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

func TestAzureProviderForwardAddsDeploymentPathAndAPIVersion(t *testing.T) {
	t.Setenv("PROVIDER_DISABLE_SSRF_CHECK", "true")

	var gotPath string
	var gotQuery string
	var gotAuth string
	var gotBody string
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		gotQuery = r.URL.RawQuery
		gotAuth = r.Header.Get("api-key")
		body, err := io.ReadAll(r.Body)
		if err != nil {
			t.Fatalf("read body: %v", err)
		}
		gotBody = string(body)
		_, _ = w.Write([]byte(`{"usage":{"total_tokens":3}}`))
	}))
	defer upstream.Close()

	provider, err := NewAzureProvider(upstream.URL, "azure-key", "2024-02-15-preview", time.Second)
	if err != nil {
		t.Fatalf("NewAzureProvider: %v", err)
	}
	_, err = provider.Forward(context.Background(), &RawRequest{
		Method: http.MethodPost,
		Path:   "/embeddings",
		Query:  "trace=1",
		Header: http.Header{"Authorization": []string{"Bearer caller-token"}},
		Body:   []byte(`{"model":"embedding-deploy","input":"hello"}`),
	})
	if err != nil {
		t.Fatalf("Forward returned error: %v", err)
	}

	if gotPath != "/openai/deployments/embedding-deploy/embeddings" {
		t.Fatalf("path = %q", gotPath)
	}
	values, err := url.ParseQuery(gotQuery)
	if err != nil {
		t.Fatalf("invalid query = %q: %v", gotQuery, err)
	}
	if values.Get("trace") != "1" || values.Get("api-version") != "2024-02-15-preview" {
		t.Fatalf("query = %q", gotQuery)
	}
	if gotAuth != "azure-key" {
		t.Fatalf("api-key = %q", gotAuth)
	}
	if strings.Contains(gotBody, `"model"`) {
		t.Fatalf("azure request should omit model from body, got %s", gotBody)
	}
}

func TestVoyageAIProviderForwardEmbeddingsConvertsResponseToOpenAIShape(t *testing.T) {
	t.Setenv("PROVIDER_DISABLE_SSRF_CHECK", "true")

	var gotPath string
	var gotAuth string
	var gotBody string
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		gotAuth = r.Header.Get("Authorization")
		body, err := io.ReadAll(r.Body)
		if err != nil {
			t.Fatalf("read body: %v", err)
		}
		gotBody = string(body)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"object":"list","data":[{"object":"embedding","embedding":[0.1,0.2],"index":0}],"model":"voyage-3","usage":{"total_tokens":7}}`))
	}))
	defer upstream.Close()

	provider, err := NewVoyageAIProvider(upstream.URL+"/v1", "pa-voyage-key", time.Second)
	if err != nil {
		t.Fatalf("NewVoyageAIProvider: %v", err)
	}
	resp, err := provider.Forward(context.Background(), &RawRequest{
		Method: http.MethodPost,
		Path:   "/embeddings",
		Header: http.Header{"Authorization": []string{"Bearer caller-token"}},
		Body:   []byte(`{"model":"voyage-3","input":"hello"}`),
	})
	if err != nil {
		t.Fatalf("Forward returned error: %v", err)
	}

	if gotPath != "/v1/embeddings" {
		t.Fatalf("path = %q", gotPath)
	}
	if gotAuth != "Bearer pa-voyage-key" {
		t.Fatalf("auth = %q", gotAuth)
	}
	if gotBody != `{"model":"voyage-3","input":"hello"}` {
		t.Fatalf("body = %q", gotBody)
	}
	body := string(resp.Body)
	for _, want := range []string{`"object":"list"`, `"embedding":[0.1,0.2]`, `"prompt_tokens":7`, `"total_tokens":7`} {
		if !strings.Contains(body, want) {
			t.Fatalf("response missing %s: %s", want, body)
		}
	}
}

func TestVoyageAIProviderRejectsUnsupportedRawPath(t *testing.T) {
	t.Setenv("PROVIDER_DISABLE_SSRF_CHECK", "true")

	provider, err := NewVoyageAIProvider("https://api.voyageai.com/v1", "pa-voyage-key", time.Second)
	if err != nil {
		t.Fatalf("NewVoyageAIProvider: %v", err)
	}
	_, err = provider.Forward(context.Background(), &RawRequest{
		Method: http.MethodPost,
		Path:   "/chat/completions",
		Body:   []byte(`{"model":"voyage-3"}`),
	})
	if err == nil || !strings.Contains(err.Error(), "not supported") {
		t.Fatalf("error = %v, want not supported", err)
	}
}

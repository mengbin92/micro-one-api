package provider

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"
)

func TestOpenAIProviderForwardStreamTimesOutWaitingForHeaders(t *testing.T) {
	const streamTimeout = 100 * time.Millisecond
	releaseUpstream := make(chan struct{})
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		<-releaseUpstream
	}))
	defer func() {
		close(releaseUpstream)
		upstream.Close()
	}()

	provider, err := NewOpenAIProvider(upstream.URL, "sk-upstream", streamTimeout)
	if err != nil {
		t.Fatalf("NewOpenAIProvider: %v", err)
	}

	startedAt := time.Now()
	_, err = provider.ForwardStream(context.Background(), &RawRequest{
		Method: http.MethodPost,
		Path:   "/responses",
		Body:   []byte(`{"model":"gpt-5","stream":true}`),
	})
	if err == nil {
		t.Fatal("expected response-header timeout")
	}
	if elapsed := time.Since(startedAt); elapsed > 5*streamTimeout {
		t.Fatalf("response-header timeout took %v, want <= %v", elapsed, 5*streamTimeout)
	}
}

func TestOpenAIProviderForwardStreamTimesOutWhenBodyGoesIdle(t *testing.T) {
	const streamTimeout = 100 * time.Millisecond
	releaseUpstream := make(chan struct{})
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("data: first\n\n"))
		w.(http.Flusher).Flush()
		<-releaseUpstream
	}))
	defer func() {
		close(releaseUpstream)
		upstream.Close()
	}()

	provider, err := NewOpenAIProvider(upstream.URL, "sk-upstream", streamTimeout)
	if err != nil {
		t.Fatalf("NewOpenAIProvider: %v", err)
	}
	resp, err := provider.ForwardStream(context.Background(), &RawRequest{
		Method: http.MethodPost,
		Path:   "/responses",
		Body:   []byte(`{"model":"gpt-5","stream":true}`),
	})
	if err != nil {
		t.Fatalf("ForwardStream: %v", err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if !errors.Is(err, ErrStreamIdleTimeout) {
		t.Fatalf("ReadAll error = %v, want ErrStreamIdleTimeout (body=%q)", err, body)
	}
	if string(body) != "data: first\n\n" {
		t.Fatalf("body = %q, want first SSE event before timeout", body)
	}
}

func TestOpenAIProviderForwardStreamKeepsActiveLongStreamAlive(t *testing.T) {
	const streamTimeout = 150 * time.Millisecond
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(http.StatusOK)
		flusher := w.(http.Flusher)
		for _, chunk := range []string{"one", "two", "three", "four", "five"} {
			_, _ = w.Write([]byte(chunk))
			flusher.Flush()
			time.Sleep(streamTimeout / 3)
		}
	}))
	defer upstream.Close()

	provider, err := NewOpenAIProvider(upstream.URL, "sk-upstream", streamTimeout)
	if err != nil {
		t.Fatalf("NewOpenAIProvider: %v", err)
	}
	startedAt := time.Now()
	resp, err := provider.ForwardStream(context.Background(), &RawRequest{
		Method: http.MethodPost,
		Path:   "/responses",
		Body:   []byte(`{"model":"gpt-5","stream":true}`),
	})
	if err != nil {
		t.Fatalf("ForwardStream: %v", err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("ReadAll: %v", err)
	}
	if elapsed := time.Since(startedAt); elapsed <= streamTimeout {
		t.Fatalf("stream duration = %v, test must exceed hard timeout %v", elapsed, streamTimeout)
	}
	if string(body) != "onetwothreefourfive" {
		t.Fatalf("body = %q", body)
	}
}

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

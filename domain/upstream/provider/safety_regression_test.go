package provider

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestProviderSuccessBodyKeepsResponseLimit(t *testing.T) {
	t.Setenv("PROVIDER_DISABLE_SSRF_CHECK", "true")
	body := strings.Repeat("x", 2*MaxUpstreamErrorBody)
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusCreated)
		_, _ = w.Write([]byte(body))
	}))
	defer upstream.Close()

	for _, channelType := range []int32{ChannelTypeOpenAI, ChannelTypeAzure, ChannelTypeAnthropic, ChannelTypeVoyageAI} {
		t.Run(channelTypeName(channelType), func(t *testing.T) {
			p, err := NewProviderFactory(time.Second).CreateProvider(channelType, upstream.URL, "test-key")
			if err != nil {
				t.Fatal(err)
			}
			resp, err := p.Forward(context.Background(), &RawRequest{Path: "/embeddings", Body: []byte(`{"model":"test"}`)})
			if err != nil {
				t.Fatal(err)
			}
			if string(resp.Body) != body {
				t.Fatalf("successful body truncated to %d bytes, want %d", len(resp.Body), len(body))
			}
		})
	}
}

func TestProviderNonStreamingErrorBodyLimit(t *testing.T) {
	t.Setenv("PROVIDER_DISABLE_SSRF_CHECK", "true")
	body := strings.Repeat("x", 2*MaxUpstreamErrorBody)
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Retry-After", "3")
		w.WriteHeader(http.StatusTooManyRequests)
		_, _ = w.Write([]byte(body))
	}))
	defer upstream.Close()

	for _, channelType := range []int32{ChannelTypeOpenAI, ChannelTypeAzure, ChannelTypeAnthropic, ChannelTypeGemini, ChannelTypeVoyageAI} {
		p, err := NewProviderFactory(time.Second).CreateProvider(channelType, upstream.URL, "test-key")
		if err != nil {
			t.Fatal(err)
		}
		for _, raw := range []bool{false, true} {
			if channelType == ChannelTypeGemini && raw || channelType == ChannelTypeVoyageAI && !raw {
				continue
			}
			t.Run(strings.Join([]string{channelTypeName(channelType), map[bool]string{true: "raw", false: "chat"}[raw]}, "/"), func(t *testing.T) {
				var err error
				if raw {
					_, err = p.Forward(context.Background(), &RawRequest{Path: "/embeddings", Body: []byte(`{"model":"test"}`)})
				} else {
					_, err = p.ChatCompletions(context.Background(), &ChatCompletionsRequest{Model: "test", Messages: []Message{{Role: "user", Content: "test"}}})
				}
				var upstreamErr *UpstreamHTTPError
				if !errors.As(err, &upstreamErr) {
					t.Fatalf("error type = %T, want UpstreamHTTPError", err)
				}
				if upstreamErr.StatusCode != http.StatusTooManyRequests || upstreamErr.RetryAfter != 3*time.Second {
					t.Fatalf("upstream status/retry metadata lost: status=%d retry=%s", upstreamErr.StatusCode, upstreamErr.RetryAfter)
				}
				if len(upstreamErr.Body) != MaxUpstreamErrorBody {
					t.Fatalf("buffered error body = %d bytes, want %d", len(upstreamErr.Body), MaxUpstreamErrorBody)
				}
			})
		}
	}
}

func channelTypeName(channelType int32) string {
	return map[int32]string{ChannelTypeOpenAI: "openai", ChannelTypeAzure: "azure", ChannelTypeAnthropic: "anthropic", ChannelTypeGemini: "gemini", ChannelTypeVoyageAI: "voyageai"}[channelType]
}

func TestProviderTypedChatUnexpectedSuccessStatusBodyLimit(t *testing.T) {
	t.Setenv("PROVIDER_DISABLE_SSRF_CHECK", "true")
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusCreated)
		_, _ = w.Write([]byte(strings.Repeat("x", 2*MaxUpstreamErrorBody)))
	}))
	defer upstream.Close()
	for _, channelType := range []int32{ChannelTypeOpenAI, ChannelTypeAzure, ChannelTypeAnthropic, ChannelTypeGemini} {
		t.Run(channelTypeName(channelType), func(t *testing.T) {
			p, err := NewProviderFactory(time.Second).CreateProvider(channelType, upstream.URL, "test-key")
			if err != nil {
				t.Fatal(err)
			}
			_, err = p.ChatCompletions(context.Background(), &ChatCompletionsRequest{Model: "test"})
			var upstreamErr *UpstreamHTTPError
			if !errors.As(err, &upstreamErr) || upstreamErr.StatusCode != http.StatusCreated {
				t.Fatalf("expected upstream 201 error, got %v", err)
			}
			if len(upstreamErr.Body) != MaxUpstreamErrorBody {
				t.Fatalf("unexpected 2xx error buffered %d bytes, want %d", len(upstreamErr.Body), MaxUpstreamErrorBody)
			}
		})
	}
}

package server

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	relayprovider "micro-one-api/domain/upstream/provider"
	relaybiz "micro-one-api/internal/biz"
	appmiddleware "micro-one-api/platform/middleware"
)

func TestIsRelayCORSResponseHeader(t *testing.T) {
	for _, key := range []string{
		"Access-Control-Allow-Origin",
		"access-control-allow-headers",
		" Access-Control-Expose-Headers ",
	} {
		if !IsRelayCORSResponseHeader(key) {
			t.Errorf("IsRelayCORSResponseHeader(%q) = false", key)
		}
	}
	if IsRelayCORSResponseHeader("Content-Type") {
		t.Error("Content-Type must not be treated as a CORS response header")
	}
}

func TestRelayResponseWritersDropUpstreamCORSHeaders(t *testing.T) {
	upstreamHeader := http.Header{
		"Content-Type":                     {"application/json"},
		"Access-Control-Allow-Origin":      {"*"},
		"Access-Control-Allow-Credentials": {"true"},
		"X-Upstream-Request-ID":            {"upstream-request"},
	}
	tests := []struct {
		name  string
		write func(http.ResponseWriter)
	}{
		{
			name: "orchestrated non-stream",
			write: func(w http.ResponseWriter) {
				writeOrchestratedRelayResult(w, &RelayResult{
					Response:   io.NopCloser(strings.NewReader(`{"ok":true}`)),
					Headers:    upstreamHeader.Clone(),
					StatusCode: http.StatusOK,
				})
			},
		},
		{
			name: "orchestrated stream",
			write: func(w http.ResponseWriter) {
				writeOrchestratedRelayStream(w, relaybiz.ExecutionResponse{
					StatusCode: http.StatusOK,
					Headers:    httpHeaderToMap(upstreamHeader),
					Stream:     io.NopCloser(strings.NewReader("data: [DONE]\n\n")),
				})
			},
		},
		{
			name: "raw non-stream",
			write: func(w http.ResponseWriter) {
				writeRawResponse(w, &relayprovider.RawResponse{
					StatusCode: http.StatusOK,
					Header:     upstreamHeader.Clone(),
					Body:       []byte(`{"ok":true}`),
				})
			},
		},
		{
			name: "raw stream",
			write: func(w http.ResponseWriter) {
				writeRawStreamResponse(w, &relayprovider.RawStreamResponse{
					StatusCode: http.StatusOK,
					Header:     upstreamHeader.Clone(),
					Body:       io.NopCloser(strings.NewReader("data: [DONE]\n\n")),
				})
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			rec := httptest.NewRecorder()
			tt.write(rec)
			assertNoUpstreamCORSHeaders(t, rec.Header())
			if got := rec.Header().Get("X-Upstream-Request-ID"); got != "upstream-request" {
				t.Fatalf("non-CORS upstream header = %q", got)
			}
		})
	}
}

func TestAnthropicResponseHeaderCopyDropsUpstreamCORSHeaders(t *testing.T) {
	destination := make(http.Header)
	copyAnthropicUpstreamHeaders(destination, http.Header{
		"Access-Control-Allow-Origin": {"*"},
		"Request-ID":                  {"anthropic-request"},
	})

	assertNoUpstreamCORSHeaders(t, destination)
	if got := destination.Get("Request-ID"); got != "anthropic-request" {
		t.Fatalf("non-CORS upstream header = %q", got)
	}
}

func TestRelayCORSMiddlewareRemainsSingleHeaderOwner(t *testing.T) {
	t.Setenv("CORS_ALLOWED_ORIGINS", "https://console.mengbin.top")
	handler := appmiddleware.CORS(appmiddleware.RelayCORSConfig())(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		writeOrchestratedRelayResult(w, &RelayResult{
			Response: io.NopCloser(strings.NewReader(`{"ok":true}`)),
			Headers: http.Header{
				"Content-Type":                {"application/json"},
				"Access-Control-Allow-Origin": {"*"},
			},
			StatusCode: http.StatusOK,
		})
	}))
	req := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", nil)
	req.Header.Set("Origin", "https://console.mengbin.top")
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if got := rec.Header().Values("Access-Control-Allow-Origin"); len(got) != 1 || got[0] != "https://console.mengbin.top" {
		t.Fatalf("Access-Control-Allow-Origin = %v", got)
	}
}

func assertNoUpstreamCORSHeaders(t *testing.T, header http.Header) {
	t.Helper()
	for key := range header {
		if IsRelayCORSResponseHeader(key) {
			t.Fatalf("upstream CORS header leaked: %s=%v", key, header.Values(key))
		}
	}
}

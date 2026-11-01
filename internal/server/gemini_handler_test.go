package server

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	khttp "github.com/go-kratos/kratos/v3/transport/http"
	"github.com/stretchr/testify/require"

	relayprovider "micro-one-api/domain/upstream/provider"
	relaybiz "micro-one-api/internal/biz"
	relaydata "micro-one-api/internal/data"
	"micro-one-api/pkg/jsonx"
)

const geminiTestResponse = `{"responseId":"native-1","candidates":[{"index":0,"content":{"role":"model","parts":[{"text":"pong"},{"functionCall":{"id":"f1","name":"lookup","args":{"q":"x"}},"thoughtSignature":"opaque+/="}]},"finishReason":"STOP"}],"usageMetadata":{"promptTokenCount":10,"cachedContentTokenCount":3,"candidatesTokenCount":4,"thoughtsTokenCount":2,"totalTokenCount":16}}`
const geminiTestRequest = `{"contents":[{"role":"model","parts":[{"functionCall":{"id":"f0","name":"lookup","args":{}},"thoughtSignature":"previous+/="}]},{"role":"user","parts":[{"functionResponse":{"id":"f0","name":"lookup","response":{"value":1}}},{"text":"ping"}]}],"tools":[{"functionDeclarations":[{"name":"lookup","parameters":{"type":"OBJECT"}}]}],"generationConfig":{"maxOutputTokens":32}}`

func geminiTestGateway(t *testing.T, channel rawChannelClient, billing *rawBillingClient, identity rawIdentityClient) *khttp.Server {
	t.Helper()
	uc := relaybiz.NewRelayUsecase(relaydata.NewIdentityAdapter(identity), relaydata.NewChannelAdapter(channel), nil, &relaybiz.RetryPolicy{MaxAttempts: 3})
	s := NewHTTPServer(identity, channel, billing, relayprovider.NewProviderFactory(2*time.Second), uc)
	routes := khttp.NewServer()
	s.RegisterRoutes(routes)
	return routes
}

func TestGeminiNativeGateway(t *testing.T) {
	t.Setenv("PROVIDER_DISABLE_SSRF_CHECK", "true")
	// This new ingress always uses canonical billing, including on legacy
	// deployments where the existing OpenAI producer switch is disabled.
	t.Setenv("RELAY_CANONICAL_USAGE_PRODUCER", "false")
	for _, stream := range []bool{false, true} {
		for _, auth := range []string{"x-goog-api-key", "Authorization", "query"} {
			t.Run(fmt.Sprintf("stream=%t/auth=%s", stream, auth), func(t *testing.T) {
				var calls atomic.Int64
				upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					calls.Add(1)
					method := "generateContent"
					if stream {
						method = "streamGenerateContent"
						require.Equal(t, "sse", r.URL.Query().Get("alt"))
					}
					require.Equal(t, "/proxy/v1beta/models/vendor-model:"+method, r.URL.Path)
					require.Equal(t, "supplier-secret", r.Header.Get("x-goog-api-key"))
					require.Empty(t, r.Header.Get("Authorization"))
					require.Empty(t, r.Header.Get("x-api-key"))
					require.Empty(t, r.URL.Query().Get("key"))
					body, _ := io.ReadAll(r.Body)
					require.NotContains(t, string(body), `"model":`)
					require.Contains(t, string(body), "previous+/=")
					require.Contains(t, string(body), `"functionResponse"`)
					if stream {
						w.Header().Set("Content-Type", "text/event-stream")
						fmt.Fprintf(w, "data: %s\n\n", geminiTestResponse)
					} else {
						fmt.Fprint(w, geminiTestResponse)
					}
				}))
				defer upstream.Close()
				billing := &rawBillingClient{}
				routes := geminiTestGateway(t, rawChannelClient{baseURL: upstream.URL + "/proxy", key: "supplier-secret", chType: 3, upstreamModel: "vendor-model"}, billing, rawIdentityClient{})
				method := "generateContent"
				if stream {
					method = "streamGenerateContent?alt=sse"
				}
				path := "/v1beta/models/client-alias:" + method
				if auth == "query" {
					separator := "?"
					if stream {
						separator = "&"
					}
					path += separator + "key=gateway-key"
				}
				req := httptest.NewRequest("POST", path, strings.NewReader(geminiTestRequest))
				if auth == "Authorization" {
					req.Header.Set(auth, "Bearer gateway-key")
				} else if auth != "query" {
					req.Header.Set(auth, "gateway-key")
				}
				rec := httptest.NewRecorder()
				routes.ServeHTTP(rec, req)
				require.Equal(t, 200, rec.Code, rec.Body.String())
				require.Contains(t, rec.Body.String(), "opaque+/=")
				require.EqualValues(t, 1, calls.Load())
				require.Len(t, billing.reserveRequests, 1)
				require.Equal(t, 1, billing.commits)
				require.Zero(t, billing.releases)
				require.GreaterOrEqual(t, billing.reserveRequests[0].EstimatedTokens, int64(32+4096))
				require.Equal(t, "gemini", billing.reserveRequests[0].ServiceTierProtocol)
				commit := billing.commitRequests[0]
				require.EqualValues(t, 16, commit.ActualTokens)
				require.EqualValues(t, 1, commit.UsageContractVersion)
				require.EqualValues(t, 7, commit.Usage.Canonical.UncachedInputTokens)
				require.EqualValues(t, 3, commit.Usage.Canonical.CacheReadTokens)
				require.EqualValues(t, 6, commit.Usage.Canonical.OutputTokens)
				require.Equal(t, "gemini", commit.Usage.Reported.SourceProtocol)
			})
		}
	}
}

func TestGeminiNativeRejections(t *testing.T) {
	t.Setenv("PROVIDER_DISABLE_SSRF_CHECK", "true")
	for _, tc := range []struct {
		name, path, body, key string
		code                  int
		channelType           int32
		deny                  bool
		allowed               []string
	}{
		{name: "missing_key", code: 401},
		{name: "conflicting_key", path: "/v1beta/models/m:generateContent?key=other", key: "key", code: 401},
		{name: "duplicate_query", path: "/v1beta/models/m:generateContent?key=a&key=b", code: 400},
		{name: "unknown_query", path: "/v1beta/models/m:generateContent?other=secret", key: "key", code: 400},
		{name: "unsupported_method", path: "/v1beta/models/m:countTokens", key: "key", code: 501},
		{name: "path_injection", path: "/v1beta/models/a/b:generateContent", key: "key", code: 501},
		{name: "invalid_json", body: "{bad", key: "key", code: 400},
		{name: "media", body: `{"contents":[{"parts":[{"inlineData":{"mimeType":"image/png","data":"AA"}}]}]}`, key: "key", code: 400},
		{name: "wrong_source", key: "key", channelType: 1, code: 501},
		{name: "whitelist", key: "key", allowed: []string{"another-model"}, code: 403},
	} {
		t.Run(tc.name, func(t *testing.T) {
			billing := &rawBillingClient{}
			typ := int32(3)
			if tc.channelType != 0 {
				typ = tc.channelType
			}
			routes := geminiTestGateway(t, rawChannelClient{baseURL: "https://example.com", chType: typ, denyRoute: tc.deny}, billing, rawIdentityClient{allowedModels: tc.allowed})
			path := tc.path
			if path == "" {
				path = "/v1beta/models/m:generateContent"
			}
			body := tc.body
			if body == "" {
				body = `{"contents":[{"parts":[{"text":"p"}]}]}`
			}
			req := httptest.NewRequest("POST", path, strings.NewReader(body))
			if tc.key != "" {
				req.Header.Set("x-goog-api-key", tc.key)
			}
			rec := httptest.NewRecorder()
			routes.ServeHTTP(rec, req)
			require.Equal(t, tc.code, rec.Code, rec.Body.String())
			require.Empty(t, billing.reserveRequests)
			var payload struct {
				Error struct {
					Code   int
					Status string
				}
			}
			require.NoError(t, jsonx.Unmarshal(rec.Body.Bytes(), &payload))
			require.Equal(t, tc.code, payload.Error.Code)
			require.NotEmpty(t, payload.Error.Status)
		})
	}
}

func TestGeminiFailuresNeverReplayOrCharge(t *testing.T) {
	t.Setenv("PROVIDER_DISABLE_SSRF_CHECK", "true")
	for _, failure := range []string{"missing_usage", "invalid_usage", "stream_missing_terminal", "stream_truncated", "stream_error", "commit", "zero_usage"} {
		for _, stream := range []bool{false, true} {
			if strings.HasPrefix(failure, "stream_") && !stream {
				continue
			}
			t.Run(fmt.Sprintf("%s/stream=%t", failure, stream), func(t *testing.T) {
				calls := 0
				upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					calls++
					body := geminiTestResponse
					switch failure {
					case "missing_usage":
						body = strings.Split(body, `,"usageMetadata"`)[0] + "}"
					case "invalid_usage":
						body = strings.Replace(body, `"totalTokenCount":16`, `"totalTokenCount":15`, 1)
					case "stream_missing_terminal":
						body = strings.Replace(body, `,"finishReason":"STOP"`, "", 1)
					case "zero_usage":
						body = `{"candidates":[{"finishReason":"SAFETY"}],"usageMetadata":{"promptTokenCount":0,"totalTokenCount":0}}`
					}
					if !stream {
						fmt.Fprint(w, body)
						return
					}
					w.Header().Set("Content-Type", "text/event-stream")
					if strings.HasPrefix(failure, "stream_") {
						fmt.Fprint(w, "data: {\"candidates\":[{\"content\":{\"role\":\"model\",\"parts\":[{\"text\":\"partial\"}]}}]}\n\n")
					}
					ending := "\n\n"
					if failure == "stream_truncated" {
						ending = "\n"
					}
					fmt.Fprintf(w, "data: %s%s", body, ending)
					if failure == "stream_error" {
						fmt.Fprint(w, "data: {\"error\":{\"message\":\"secret\"}}\n\n")
					}
				}))
				defer upstream.Close()
				billing := &rawBillingClient{}
				if failure == "commit" {
					billing.commitMessage = "billing timeout"
				}
				routes := geminiTestGateway(t, rawChannelClient{baseURL: upstream.URL, key: "upstream", chType: 3}, billing, rawIdentityClient{})
				method := "generateContent"
				if stream {
					method = "streamGenerateContent?alt=sse"
				}
				req := httptest.NewRequest("POST", "/v1beta/models/m:"+method, strings.NewReader(geminiTestRequest))
				req.Header.Set("x-goog-api-key", "key")
				rec := httptest.NewRecorder()
				routes.ServeHTTP(rec, req)
				require.Equal(t, 1, calls)
				require.Len(t, billing.reserveRequests, 1)
				if failure == "commit" || failure == "zero_usage" {
					require.Equal(t, 1, billing.commits)
					require.Zero(t, billing.releases)
				} else {
					require.Zero(t, billing.commits)
					require.Equal(t, 1, billing.releases)
					require.NotContains(t, rec.Body.String(), "finishReason")
				}
				if failure == "zero_usage" {
					require.Zero(t, billing.commitRequests[0].ActualTokens)
				}
			})
		}
	}
}

func TestGeminiCanceledStreamReleasesWithDetachedContext(t *testing.T) {
	var committed bool
	var released bool
	stream := newFinalizingRelayStream(EndpointGemini, newConvertedRelayStream(strings.NewReader("data: "+geminiTestResponse+"\n\n"), nil), relaybiz.UsageEnvelope{}, func(_ relaybiz.UsageEnvelope, _ string, complete bool) error {
		committed = complete
		released = !complete
		return nil
	})
	buffer := make([]byte, 1)
	_, err := stream.Read(buffer)
	require.NoError(t, err)
	markRelayStreamInterrupted(stream, context.Canceled)
	require.NoError(t, stream.Close())
	require.NoError(t, stream.Close())
	require.False(t, committed)
	require.True(t, released)
}

func TestGeminiCredentialsReachSharedMiddlewareWithoutQuerySecrets(t *testing.T) {
	s := NewHTTPServer(nil, nil, nil, nil, nil)
	calls := 0
	s.UseRouteMiddleware(func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			calls++
			require.Equal(t, "Bearer gateway-secret", r.Header.Get("Authorization"))
			require.Empty(t, r.Header.Get("x-goog-api-key"))
			require.NotContains(t, r.URL.String(), "gateway-secret")
			require.NotContains(t, r.RequestURI, "gateway-secret")
			next.ServeHTTP(w, r)
		})
	})
	routes := khttp.NewServer()
	s.RegisterRoutes(routes)
	req := httptest.NewRequest("POST", "/v1beta/models/m:generateContent?key=gateway-secret", strings.NewReader(geminiTestRequest))
	rec := httptest.NewRecorder()
	routes.ServeHTTP(rec, req)
	require.Equal(t, 503, rec.Code)
	require.Equal(t, 1, calls)
	req = httptest.NewRequest("POST", "/v1beta/models/m:generateContent?key=gateway-secret", strings.NewReader(geminiTestRequest))
	req.Header.Set("Idempotency-Key", "same-key")
	rec = httptest.NewRecorder()
	routes.ServeHTTP(rec, req)
	require.Equal(t, 400, rec.Code)
	require.Equal(t, 1, calls, "unsupported response caching must be rejected before middleware")
}

func TestGeminiRejectsDurablePilotBeforeReservation(t *testing.T) {
	t.Setenv("RELAY_EXECUTION_DELIVERY_V1", "true")
	t.Setenv("RELAY_NATIVE_USAGE_V2_ALLOWLIST", "channel:11:m")
	billing := &rawBillingClient{}
	routes := geminiTestGateway(t, rawChannelClient{chType: 3}, billing, rawIdentityClient{})
	req := httptest.NewRequest("POST", "/v1beta/models/m:generateContent", strings.NewReader(geminiTestRequest))
	req.Header.Set("x-goog-api-key", "key")
	rec := httptest.NewRecorder()
	routes.ServeHTTP(rec, req)
	require.Equal(t, 501, rec.Code, rec.Body.String())
	require.Empty(t, billing.reserveRequests)
}

package server

import (
	"compress/gzip"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"
	"time"

	khttp "github.com/go-kratos/kratos/v3/transport/http"

	"micro-one-api/domain/upstream/provider"
	"micro-one-api/internal/biz"
	"micro-one-api/internal/data"
	"micro-one-api/pkg/jsonx"
)

// Safeguard payloads are deliberately opaque fixtures: gateways must preserve
// unknown fields, not maintain their own copy of the classifier's schema.
func TestAnthropicSafeguardsPassthrough(t *testing.T) {
	for _, channelType := range []int32{provider.ChannelTypeAnthropic, provider.ChannelTypeClaudeOAuth} {
		for _, stream := range []bool{false, true} {
			t.Run(fmt.Sprintf("channel=%d/stream=%t", channelType, stream), func(t *testing.T) {
				body := fmt.Sprintf(`{"model":"claude-native","stream":%t,"max_tokens":16,"messages":[{"role":"user","content":"inspect"}],"safeguards":{"opaque_future_field":["keep"]}}`, stream)
				response := `{"id":"msg_native","type":"message","role":"assistant","model":"claude-native","content":[{"type":"tool_use","id":"toolu_original","name":"Read","input":{}}],"safeguard_results":{"opaque_future_field":"keep"},"stop_reason":"tool_use","usage":{"input_tokens":9,"output_tokens":3}}`
				contentType := "application/json"
				if stream {
					contentType = "text/event-stream"
					response = "event: message_start\ndata: {\"type\":\"message_start\",\"message\":{\"id\":\"msg_native\",\"type\":\"message\",\"role\":\"assistant\",\"model\":\"claude-native\",\"content\":[],\"usage\":{\"input_tokens\":9,\"output_tokens\":0}}}\n\n" +
						"event: ping\ndata: {\"type\":\"ping\"}\n\n" +
						"event: content_block_start\ndata: {\"type\":\"content_block_start\",\"index\":0,\"content_block\":{\"type\":\"tool_use\",\"id\":\"toolu_original\",\"name\":\"Read\",\"input\":{}}}\n\n" +
						"event: content_block_stop\ndata: {\"type\":\"content_block_stop\",\"index\":0}\n\n" +
						"event: message_delta\ndata: {\"type\":\"message_delta\",\"delta\":{\"stop_reason\":\"tool_use\"},\"safeguard_results\":{\"opaque_future_field\":\"keep\"},\"usage\":{\"output_tokens\":3}}\n\n" +
						"event: message_stop\ndata: {\"type\":\"message_stop\"}\n\n"
				}
				headers := http.Header{}
				headers.Set("User-Agent", "claude-cli/test")
				headers.Set("x-app", "cli")
				headers.Set("anthropic-version", "2023-01-01")
				headers.Add("anthropic-beta", "opaque-beta-one")
				headers.Add("anthropic-beta", "opaque-beta-two")
				headers.Set("anthropic-workspace-id", "workspace-test")
				called := false
				upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					called = true
					for _, key := range []string{"Cookie", "X-Private-Token", "X-Another-Hop"} {
						if r.Header.Get(key) != "" {
							t.Errorf("gateway-only header %s leaked upstream", key)
						}
					}
					for key, want := range headers {
						if got := r.Header.Values(key); !reflect.DeepEqual(got, want) {
							t.Errorf("%s = %q, want %q", key, got, want)
						}
					}
					if channelType == provider.ChannelTypeClaudeOAuth {
						if r.Header.Get("Authorization") != "Bearer upstream-key" || r.Header.Get("x-api-key") != "" {
							t.Error("OAuth credentials were not replaced")
						}
					} else if r.Header.Get("x-api-key") != "upstream-key" || r.Header.Get("Authorization") != "" {
						t.Error("API-key credentials were not replaced")
					}
					gotBody, err := io.ReadAll(r.Body)
					if err != nil {
						t.Error(err)
					}
					var got, want any
					if err := jsonx.Unmarshal(gotBody, &got); err != nil {
						t.Error(err)
					}
					if err := jsonx.Unmarshal([]byte(body), &want); err != nil {
						t.Error(err)
					}
					if !reflect.DeepEqual(got, want) {
						t.Errorf("request changed: %s", gotBody)
					}
					w.Header().Set("Content-Type", contentType)
					if strings.Contains(r.Header.Get("Accept-Encoding"), "gzip") {
						w.Header().Set("Content-Encoding", "gzip")
						compressed := gzip.NewWriter(w)
						_, _ = io.WriteString(compressed, response)
						_ = compressed.Close()
					} else {
						_, _ = io.WriteString(w, response)
					}
				}))
				defer upstream.Close()

				identityClient := rawIdentityClient{}
				channelClient := rawChannelClient{baseURL: upstream.URL, key: "upstream-key", chType: channelType}
				usecase := biz.NewRelayUsecase(data.NewIdentityAdapter(identityClient), data.NewChannelAdapter(channelClient), nil, &biz.RetryPolicy{MaxAttempts: 1})
				billingClient := &rawBillingClient{}
				server := NewHTTPServer(identityClient, channelClient, billingClient, provider.NewProviderFactory(time.Second), usecase)
				server.SetHybridAdaptorEnabled(true)
				srv := khttp.NewServer()
				server.RegisterRoutes(srv)
				req := httptest.NewRequest(http.MethodPost, "/v1/messages?beta=true", strings.NewReader(body))
				req.Header = headers.Clone()
				req.Header.Set("x-api-key", "gateway-key")
				req.Header.Set("Authorization", "Bearer gateway-key")
				req.Header.Set("Accept-Encoding", "gzip, br")
				req.Header.Set("Cookie", "gateway_session=secret")
				req.Header.Add("Connection", "keep-alive, x-private-token")
				req.Header.Add("Connection", "X-Another-Hop")
				req.Header.Set("X-Private-Token", "gateway-only")
				req.Header.Set("X-Another-Hop", "gateway-only")
				rec := httptest.NewRecorder()
				srv.ServeHTTP(rec, req)
				if !called || rec.Code != http.StatusOK || rec.Body.String() != response {
					t.Fatalf("called=%t status=%d response=%q; want unchanged upstream response", called, rec.Code, rec.Body.String())
				}
				if len(billingClient.commitRequests) != 1 || billingClient.commitRequests[0].ActualTokens != 12 {
					t.Fatalf("usage was not decoded: %#v", billingClient.commitRequests)
				}
			})
		}
	}
}

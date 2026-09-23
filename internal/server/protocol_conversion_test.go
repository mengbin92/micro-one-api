package server

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	khttp "github.com/go-kratos/kratos/v3/transport/http"
	"github.com/stretchr/testify/require"
	relayprovider "micro-one-api/domain/upstream/provider"
	relaybiz "micro-one-api/internal/biz"
	relaydata "micro-one-api/internal/data"
	"micro-one-api/pkg/jsonx"
)

func TestGatewayProtocolConversion(t *testing.T) {
	for _, tc := range []struct {
		name, inbound, upstream string
		channelType             int32
		calls                   int
		rejection               int
	}{
		{"responses_to_chat_404", "/v1/responses", "/v1/chat/completions", relayprovider.ChannelTypeOpenAI, 2, 404},
		{"responses_to_chat_405", "/v1/responses", "/v1/chat/completions", relayprovider.ChannelTypeOpenAI, 2, 405},
		{"responses_to_chat_501", "/v1/responses", "/v1/chat/completions", relayprovider.ChannelTypeOpenAI, 2, 501},
		{"responses_to_messages", "/v1/responses", "/v1/messages", relayprovider.ChannelTypeAnthropic, 1, 0},
		{"messages_to_chat", "/v1/messages", "/v1/chat/completions", relayprovider.ChannelTypeOpenAI, 1, 0},
	} {
		for _, orchestrator := range []bool{false, true} {
			for _, stream := range []bool{false, true} {
				t.Run(fmt.Sprintf("%s/orchestrator=%t/stream=%t", tc.name, orchestrator, stream), func(t *testing.T) {
					t.Setenv("PROVIDER_DISABLE_SSRF_CHECK", "true")
					t.Setenv("RELAY_CANONICAL_USAGE_PRODUCER", "true")
					calls := 0
					upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
						calls++
						if r.URL.Path == "/v1/responses" && tc.channelType == relayprovider.ChannelTypeOpenAI {
							w.WriteHeader(tc.rejection)
							return
						}
						if r.URL.Path != tc.upstream {
							t.Errorf("upstream path=%s, want %s", r.URL.Path, tc.upstream)
							w.WriteHeader(http.StatusNotFound)
							return
						}
						var payload struct {
							Model    string             `json:"model"`
							Messages []jsonx.RawMessage `json:"messages"`
						}
						if err := jsonx.NewDecoder(r.Body).Decode(&payload); err != nil || payload.Model != "m" || len(payload.Messages) != 1 {
							t.Errorf("invalid converted request: %+v, err=%v", payload, err)
						}
						writeProtocolConversionToolFixture(w, tc.upstream, stream)
					}))
					defer upstream.Close()
					identity := rawIdentityClient{}
					channel := rawChannelClient{baseURL: upstream.URL + "/v1", key: "secret", chType: tc.channelType}
					billing := &rawBillingClient{}
					uc := relaybiz.NewRelayUsecase(relaydata.NewIdentityAdapter(identity), relaydata.NewChannelAdapter(channel), nil, &relaybiz.RetryPolicy{MaxAttempts: 3})
					s := NewHTTPServer(identity, channel, billing, relayprovider.NewProviderFactory(time.Second), uc)
					s.SetRelayOrchestratorEnabled(orchestrator)
					s.SetRelayOrchestratorTokenHMACAllowlist(relayOrchestratorTestHMACKey, []string{relayOrchestratorTestDigest("user-token")})
					srv := khttp.NewServer()
					s.RegisterRoutes(srv)
					body := fmt.Sprintf(`{"model":"m","input":"ping","stream":%t}`, stream)
					if tc.inbound == "/v1/messages" {
						body = fmt.Sprintf(`{"model":"m","messages":[{"role":"user","content":"ping"}],"max_tokens":16,"stream":%t}`, stream)
					}
					req := httptest.NewRequest("POST", tc.inbound, strings.NewReader(body))
					req.Header.Set("Authorization", "Bearer user-token")
					rec := httptest.NewRecorder()
					srv.ServeHTTP(rec, req)
					require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
					require.Contains(t, rec.Body.String(), "pong")
					require.Contains(t, rec.Body.String(), "lookup")
					if stream {
						terminal := "response.completed"
						if tc.inbound == "/v1/messages" {
							terminal = "message_stop"
						}
						require.Contains(t, rec.Body.String(), terminal)
					}
					require.Equal(t, tc.calls, calls)
					require.Len(t, billing.reserveRequests, 1)
					require.Equal(t, 1, billing.commits)
					require.Equal(t, 0, billing.releases)
					require.EqualValues(t, 12, billing.commitRequests[0].ActualTokens, "%+v", billing.commitRequests[0].Usage)
					canonical := billing.commitRequests[0].GetUsage().GetCanonical()
					require.NotNil(t, canonical, "%+v; response=%s", billing.commitRequests[0].Usage, rec.Body.String())
					require.EqualValues(t, 4, canonical.GetUncachedInputTokens())
					require.EqualValues(t, 2, canonical.GetCacheReadTokens())
					require.EqualValues(t, 1, canonical.GetCacheCreation_5MTokens())
					require.EqualValues(t, 5, canonical.GetOutputTokens())
				})
			}
		}
	}
}

func writeProtocolConversionToolFixture(w http.ResponseWriter, path string, stream bool) {
	if !stream {
		w.Header().Set("Content-Type", "application/json")
		if path == "/v1/messages" {
			fmt.Fprint(w, `{"id":"msg-test","type":"message","role":"assistant","model":"m","content":[{"type":"text","text":"pong"},{"type":"tool_use","id":"toolu_1","name":"lookup","input":{"q":"test"}}],"stop_reason":"tool_use","usage":{"input_tokens":4,"output_tokens":5,"cache_read_input_tokens":2,"cache_creation_input_tokens":1}}`)
		} else {
			fmt.Fprint(w, `{"id":"chat-test","model":"m","choices":[{"index":0,"message":{"role":"assistant","content":"pong","tool_calls":[{"id":"call_1","type":"function","function":{"name":"lookup","arguments":"{\"q\":\"test\"}"}}]},"finish_reason":"tool_calls"}],"usage":{"prompt_tokens":7,"completion_tokens":5,"total_tokens":12,"prompt_tokens_details":{"cached_tokens":2,"cache_creation_5m_tokens":1}}}`)
		}
		return
	}
	w.Header().Set("Content-Type", "text/event-stream")
	if path == "/v1/messages" {
		for _, event := range []string{
			`{"type":"message_start","message":{"id":"msg-test","model":"m","role":"assistant","content":[],"usage":{"input_tokens":4,"output_tokens":0,"cache_read_input_tokens":2,"cache_creation_input_tokens":1}}}`,
			`{"type":"content_block_start","index":0,"content_block":{"type":"text","text":""}}`,
			`{"type":"content_block_delta","index":0,"delta":{"type":"text_delta","text":"pong"}}`,
			`{"type":"content_block_stop","index":0}`,
			`{"type":"content_block_start","index":1,"content_block":{"type":"tool_use","id":"toolu_1","name":"lookup","input":{}}}`,
			`{"type":"content_block_delta","index":1,"delta":{"type":"input_json_delta","partial_json":"{\"q\":\"test\"}"}}`,
			`{"type":"content_block_stop","index":1}`,
			`{"type":"message_delta","delta":{"stop_reason":"tool_use"},"usage":{"output_tokens":5}}`,
			`{"type":"message_stop"}`,
		} {
			fmt.Fprintf(w, "data: %s\n\n", event)
		}
	} else {
		fmt.Fprint(w, "data: "+`{"id":"chat-test","model":"m","choices":[{"index":0,"delta":{"role":"assistant","content":"pong","tool_calls":[{"index":0,"id":"call_1","type":"function","function":{"name":"lookup","arguments":"{\"q\":\"test\"}"}}]},"finish_reason":null}]}`+"\n\ndata: "+`{"id":"chat-test","model":"m","choices":[{"index":0,"delta":{},"finish_reason":"tool_calls"}],"usage":{"prompt_tokens":7,"completion_tokens":5,"total_tokens":12,"prompt_tokens_details":{"cached_tokens":2,"cache_creation_5m_tokens":1}}}`+"\n\ndata: [DONE]\n\n")
	}
}

func writeProtocolConversionFixture(w http.ResponseWriter, path string, stream bool) {
	if !stream {
		w.Header().Set("Content-Type", "application/json")
		if path == "/v1/messages" {
			fmt.Fprint(w, `{"id":"msg-test","type":"message","role":"assistant","model":"m","content":[{"type":"text","text":"pong"}],"stop_reason":"end_turn","usage":{"input_tokens":4,"output_tokens":5}}`)
		} else {
			fmt.Fprint(w, `{"id":"chat-test","object":"chat.completion","model":"m","choices":[{"index":0,"message":{"role":"assistant","content":"pong"},"finish_reason":"stop"}],"usage":{"prompt_tokens":4,"completion_tokens":5,"total_tokens":9}}`)
		}
		return
	}
	w.Header().Set("Content-Type", "text/event-stream")
	if path == "/v1/messages" {
		fmt.Fprint(w, "event: message_start\ndata: {\"type\":\"message_start\",\"message\":{\"id\":\"msg-test\",\"type\":\"message\",\"role\":\"assistant\",\"model\":\"m\",\"content\":[],\"usage\":{\"input_tokens\":4,\"output_tokens\":0}}}\n\nevent: content_block_start\ndata: {\"type\":\"content_block_start\",\"index\":0,\"content_block\":{\"type\":\"text\",\"text\":\"\"}}\n\nevent: content_block_delta\ndata: {\"type\":\"content_block_delta\",\"index\":0,\"delta\":{\"type\":\"text_delta\",\"text\":\"pong\"}}\n\nevent: content_block_stop\ndata: {\"type\":\"content_block_stop\",\"index\":0}\n\nevent: message_delta\ndata: {\"type\":\"message_delta\",\"delta\":{\"stop_reason\":\"end_turn\"},\"usage\":{\"output_tokens\":5}}\n\nevent: message_stop\ndata: {\"type\":\"message_stop\"}\n\n")
	} else {
		fmt.Fprint(w, "data: {\"id\":\"chat-test\",\"model\":\"m\",\"choices\":[{\"index\":0,\"delta\":{\"role\":\"assistant\",\"content\":\"pong\"},\"finish_reason\":null}]}\n\ndata: {\"id\":\"chat-test\",\"model\":\"m\",\"choices\":[{\"index\":0,\"delta\":{},\"finish_reason\":\"stop\"}],\"usage\":{\"prompt_tokens\":4,\"completion_tokens\":5,\"total_tokens\":9}}\n\ndata: [DONE]\n\n")
	}
}

func TestConvertedResponsesFailuresNeverReplay(t *testing.T) {
	for _, orchestrator := range []bool{false, true} {
		for _, channelType := range []int32{relayprovider.ChannelTypeOpenAI, relayprovider.ChannelTypeAnthropic} {
			for _, failure := range []string{"interrupted_stream", "commit", "client_error", "invalid_response"} {
				t.Run(fmt.Sprintf("orchestrator=%t/type=%d/%s", orchestrator, channelType, failure), func(t *testing.T) {
					t.Setenv("PROVIDER_DISABLE_SSRF_CHECK", "true")
					calls := 0
					upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
						calls++
						if r.URL.Path == "/v1/responses" {
							w.WriteHeader(405)
							return
						}
						if failure == "client_error" {
							w.WriteHeader(400)
							fmt.Fprint(w, `{"error":{"message":"invalid tools"}}`)
							return
						}
						if failure == "invalid_response" {
							fmt.Fprint(w, "{invalid-json")
							return
						}
						if failure == "interrupted_stream" {
							w.Header().Set("Content-Type", "text/event-stream")
							if channelType == relayprovider.ChannelTypeAnthropic {
								fmt.Fprint(w, "event: message_start\ndata: {\"type\":\"message_start\",\"message\":{\"id\":\"m\",\"model\":\"m\",\"content\":[]}}\n\nevent: content_block_delta\ndata: {\"type\":\"content_block_delta\",\"index\":0,\"delta\":{\"type\":\"text_delta\",\"text\":\"partial\"}}\n\n")
							} else {
								fmt.Fprint(w, "data: {\"choices\":[{\"index\":0,\"delta\":{\"content\":\"partial\"}}]}\n\n")
							}
							return
						}
						writeProtocolConversionFixture(w, r.URL.Path, false)
					}))
					defer upstream.Close()
					identity := rawIdentityClient{}
					channel := rawChannelClient{baseURL: upstream.URL + "/v1", chType: channelType, key: "secret"}
					billing := &rawBillingClient{}
					if failure == "commit" {
						billing.commitMessage = "billing timeout"
					}
					uc := relaybiz.NewRelayUsecase(relaydata.NewIdentityAdapter(identity), relaydata.NewChannelAdapter(channel), nil, &relaybiz.RetryPolicy{MaxAttempts: 3})
					s := NewHTTPServer(identity, channel, billing, relayprovider.NewProviderFactory(time.Second), uc)
					s.SetRelayOrchestratorEnabled(orchestrator)
					s.SetRelayOrchestratorTokenHMACAllowlist(relayOrchestratorTestHMACKey, []string{relayOrchestratorTestDigest("user-token")})
					srv := khttp.NewServer()
					s.RegisterRoutes(srv)
					req := httptest.NewRequest("POST", "/v1/responses", strings.NewReader(fmt.Sprintf(`{"model":"m","input":"ping","stream":%t}`, failure == "interrupted_stream")))
					req.Header.Set("Authorization", "Bearer user-token")
					rec := httptest.NewRecorder()
					srv.ServeHTTP(rec, req)
					wantCalls := 1
					if channelType == relayprovider.ChannelTypeOpenAI {
						wantCalls = 2
					}
					require.Equal(t, wantCalls, calls, rec.Body.String())
					require.Len(t, billing.reserveRequests, 1)
					if failure == "commit" {
						require.Equal(t, 1, billing.commits)
					} else {
						require.Zero(t, billing.commits, rec.Body.String())
						require.Equal(t, 1, billing.releases)
					}
					if failure == "interrupted_stream" {
						require.Contains(t, rec.Body.String(), "response.failed")
						require.NotContains(t, rec.Body.String(), "response.completed")
					}
					if failure == "client_error" {
						require.Equal(t, 400, rec.Code)
					}
					if failure == "invalid_response" {
						require.Equal(t, http.StatusBadGateway, rec.Code)
					}
				})
			}
		}
	}
}

func TestResponsesNativeAndStateCapabilities(t *testing.T) {
	for _, orchestrator := range []bool{false, true} {
		for _, stream := range []bool{false, true} {
			for _, upstreamMode := range []string{"native", "chat", "anthropic"} {
				t.Run(fmt.Sprintf("orchestrator=%t/stream=%t/%s", orchestrator, stream, upstreamMode), func(t *testing.T) {
					t.Setenv("PROVIDER_DISABLE_SSRF_CHECK", "true")
					calls := 0
					upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
						calls++
						if r.URL.Path != "/v1/responses" {
							t.Errorf("unexpected conversion: %s", r.URL.Path)
						}
						if upstreamMode != "native" {
							w.WriteHeader(http.StatusNotFound)
							return
						}
						if r.URL.Query().Get("trace") != "a b" {
							t.Errorf("native query lost: %s", r.URL.RawQuery)
						}
						var payload map[string]any
						if err := jsonx.NewDecoder(r.Body).Decode(&payload); err != nil || payload["store"] != true {
							t.Errorf("native request changed: %+v, err=%v", payload, err)
						}
						response := `{"id":"resp_native","object":"response","status":"completed","output":[],"usage":{"input_tokens":4,"output_tokens":5,"total_tokens":9}}`
						if stream {
							w.Header().Set("Content-Type", "text/event-stream")
							fmt.Fprintf(w, "event: response.completed\ndata: {\"type\":\"response.completed\",\"response\":%s}\n\ndata: [DONE]\n\n", response)
						} else {
							w.Header().Set("Content-Type", "application/json")
							fmt.Fprint(w, response)
						}
					}))
					defer upstream.Close()
					channelType := int32(relayprovider.ChannelTypeOpenAI)
					if upstreamMode == "anthropic" {
						channelType = relayprovider.ChannelTypeAnthropic
					}
					identity := rawIdentityClient{}
					channel := rawChannelClient{baseURL: upstream.URL + "/v1", key: "secret", chType: channelType}
					billing := &rawBillingClient{}
					uc := relaybiz.NewRelayUsecase(relaydata.NewIdentityAdapter(identity), relaydata.NewChannelAdapter(channel), nil, &relaybiz.RetryPolicy{MaxAttempts: 3})
					s := NewHTTPServer(identity, channel, billing, relayprovider.NewProviderFactory(time.Second), uc)
					s.SetRelayOrchestratorEnabled(orchestrator)
					s.SetRelayOrchestratorTokenHMACAllowlist(relayOrchestratorTestHMACKey, []string{relayOrchestratorTestDigest("user-token")})
					srv := khttp.NewServer()
					s.RegisterRoutes(srv)
					req := httptest.NewRequest("POST", "/v1/responses?trace=a%20b", strings.NewReader(fmt.Sprintf(`{"model":"m","input":"ping","store":true,"stream":%t}`, stream)))
					req.Header.Set("Authorization", "Bearer user-token")
					rec := httptest.NewRecorder()
					srv.ServeHTTP(rec, req)
					if upstreamMode == "native" {
						require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
						require.Contains(t, rec.Body.String(), "resp_native")
						require.Equal(t, 1, billing.commits)
						require.Zero(t, billing.releases)
					} else {
						require.Equal(t, http.StatusNotImplemented, rec.Code, rec.Body.String())
						require.Contains(t, rec.Body.String(), "store")
						require.Contains(t, rec.Body.String(), "native Responses")
						require.Zero(t, billing.commits)
						require.Equal(t, 1, billing.releases)
					}
					wantCalls := 1
					if upstreamMode == "anthropic" {
						wantCalls = 0
					}
					require.Equal(t, wantCalls, calls)
				})
			}
		}
	}
}

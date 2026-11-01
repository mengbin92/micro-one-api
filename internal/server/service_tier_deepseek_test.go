package server

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	billingdomain "micro-one-api/domain/billing"
	relayprovider "micro-one-api/domain/upstream/provider"
	relaybiz "micro-one-api/internal/biz"
	relaydata "micro-one-api/internal/data"
	"micro-one-api/pkg/jsonx"
)

func TestDeepSeekTierBoundedRequest(t *testing.T) {
	channel := &relaybiz.Channel{Type: relayprovider.ChannelTypeDeepSeek}
	for _, tc := range []struct {
		name, body, protocol string
		output               int64
	}{
		{"native chat limit", `{"messages":[{"role":"user","content":"hi"}],"max_tokens":32}`, "openai_chat", 32},
		{"explicit completion limit wins", `{"messages":[{"role":"user","content":"hi"}],"max_tokens":32,"max_completion_tokens":64}`, "openai_chat", 64},
		{"invalid explicit limit stays invalid", `{"messages":[{"role":"user","content":"hi"}],"max_tokens":32,"max_completion_tokens":null}`, "openai_chat", 0},
		{"responses", `{"input":"hi","max_output_tokens":32}`, "openai_responses", 32},
		{"missing output limit", `{"messages":[{"role":"user","content":"hi"}]}`, "openai_chat", 0},
		{"tools unbounded", `{"messages":[{"role":"user","content":"hi"}],"max_tokens":32,"tools":[{"type":"function"}]}`, "openai_chat", 0},
		{"hidden state unbounded", `{"input":"hi","max_output_tokens":32,"previous_response_id":"old"}`, "openai_responses", 0},
		{"media unbounded", `{"messages":[{"role":"user","content":[{"type":"image_url","image_url":"https://example.com/a"}]}],"max_tokens":32}`, "openai_chat", 0},
		{"other protocol", `{"messages":[{"role":"user","content":"hi"}],"max_tokens":32}`, "unsupported", 0},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ctx := tierAttemptContext(context.Background(), []byte(tc.body), tc.protocol, channel)
			request := billingdomain.GetServiceTierRequest(ctx)
			require.Equal(t, tc.output, request.OutputTokenBound)
			require.Equal(t, tc.output > 0, request.InputTokenBound > 0)
			require.Empty(t, request.RequestedServiceTier)
		})
	}
	ctx := tierAttemptContext(context.Background(), []byte(`{"messages":[{"role":"user","content":"hi"}],"max_tokens":32}`), "openai_chat", &relaybiz.Channel{Type: relayprovider.ChannelTypeDeepSeek, SubscriptionAccountID: 1})
	require.Equal(t, "unsupported", billingdomain.GetServiceTierRequest(ctx).Protocol)
}

func TestDeepSeekTierChatJSONAndStreamEvidence(t *testing.T) {
	t.Setenv("PROVIDER_DISABLE_SSRF_CHECK", "true")
	validUsage := `{"prompt_tokens":100,"completion_tokens":4,"total_tokens":104,"prompt_cache_hit_tokens":60,"prompt_cache_miss_tokens":40,"prompt_tokens_details":{"cached_tokens":60}}`
	for _, stream := range []bool{false, true} {
		for _, tc := range []struct {
			name, usage string
			accepted    bool
			truncated   bool
		}{
			{"matching cache aliases", validUsage, true, false},
			{"hit miss aliases without optional detail", `{"prompt_tokens":100,"completion_tokens":4,"total_tokens":104,"prompt_cache_hit_tokens":60,"prompt_cache_miss_tokens":40}`, true, false},
			{"standard cached detail", `{"prompt_tokens":100,"completion_tokens":4,"total_tokens":104,"prompt_tokens_details":{"cached_tokens":60}}`, true, false},
			{"cache detail disagrees", strings.Replace(validUsage, `"cached_tokens":60`, `"cached_tokens":59`, 1), false, false},
			{"cache miss disagrees", strings.Replace(validUsage, `"prompt_cache_miss_tokens":40`, `"prompt_cache_miss_tokens":41`, 1), false, false},
			{"missing cache miss", `{"prompt_tokens":100,"completion_tokens":4,"total_tokens":104,"prompt_cache_hit_tokens":60}`, false, false},
			{"fractional cache hit", strings.Replace(validUsage, `"prompt_cache_hit_tokens":60`, `"prompt_cache_hit_tokens":60.5`, 1), false, false},
			{"null cache hit", strings.Replace(validUsage, `"prompt_cache_hit_tokens":60`, `"prompt_cache_hit_tokens":null`, 1), false, false},
			{"negative cache hit", strings.Replace(validUsage, `"prompt_cache_hit_tokens":60`, `"prompt_cache_hit_tokens":-1`, 1), false, false},
			{"conflicting flat alias", strings.Replace(validUsage, `"prompt_cache_hit_tokens":60`, `"cache_read_tokens":59,"prompt_cache_hit_tokens":60`, 1), false, false},
			{"conflicting input detail", strings.Replace(validUsage, `"prompt_cache_hit_tokens":60`, `"input_tokens_details":{"cached_tokens":59},"prompt_cache_hit_tokens":60`, 1), false, false},
			{"stream cut after usage", validUsage, false, true},
		} {
			if tc.truncated && !stream {
				continue
			}
			t.Run(fmt.Sprintf("stream=%t/%s", stream, tc.name), func(t *testing.T) {
				upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					require.Equal(t, "/v1/chat/completions", r.URL.Path)
					if stream {
						w.Header().Set("Content-Type", "text/event-stream")
						_, _ = fmt.Fprint(w, "data: {\"id\":\"deepseek-stream\",\"choices\":[{\"index\":0,\"delta\":{\"content\":\"hello\"}}],\"usage\":null}\n\n")
						// DeepSeek reports usage on its final content chunk.
						_, _ = fmt.Fprintf(w, "data: {\"id\":\"deepseek-stream\",\"choices\":[{\"index\":0,\"delta\":{},\"finish_reason\":\"stop\"}],\"usage\":%s}\n\n", tc.usage)
						if !tc.truncated {
							_, _ = fmt.Fprint(w, "data: [DONE]\n\n")
						}
						return
					}
					_, _ = fmt.Fprintf(w, `{"id":"deepseek-json","object":"chat.completion","choices":[{"index":0,"message":{"role":"assistant","content":"hello"},"finish_reason":"stop"}],"usage":%s}`, tc.usage)
				}))
				defer upstream.Close()
				identity, channel := rawIdentityClient{}, rawChannelClient{baseURL: upstream.URL + "/v1", key: "test-upstream", chType: relayprovider.ChannelTypeDeepSeek}
				billing, logs := &rawBillingClient{tierPricingVersion: 1}, &rawLogClient{}
				uc := relaybiz.NewRelayUsecase(relaydata.NewIdentityAdapter(identity), relaydata.NewChannelAdapter(channel), nil, &relaybiz.RetryPolicy{MaxAttempts: 1})
				s := NewHTTPServer(identity, channel, billing, relayprovider.NewProviderFactory(time.Second), uc, logs)
				body := fmt.Sprintf(`{"model":"deepseek-flash","stream":%t,"messages":[{"role":"user","content":"hi"}],"max_tokens":64}`, stream)
				r := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", strings.NewReader(body))
				r.Header.Set("Authorization", "Bearer user-token")
				w := httptest.NewRecorder()
				s.handleChatCompletions(w, r)
				require.Equal(t, http.StatusOK, w.Code, w.Body.String())
				require.Len(t, billing.reserveRequests, 1)
				require.Equal(t, "openai_chat", billing.reserveRequests[0].ServiceTierProtocol)
				require.EqualValues(t, 64, billing.reserveRequests[0].OutputTokenBound)
				require.Positive(t, billing.reserveRequests[0].InputTokenBound)
				if !tc.accepted {
					require.Zero(t, billing.commits)
					require.Equal(t, 1, billing.releases)
					require.Len(t, logs.entries, 1)
					require.Contains(t, logs.entries[0].Message, "settlement_outcome=released/")
					return
				}
				require.Equal(t, 1, billing.commits)
				require.Zero(t, billing.releases)
				commit := billing.commitRequests[0]
				require.Empty(t, commit.ActualServiceTier)
				require.Equal(t, "missing", commit.ServiceTierStatus)
				require.EqualValues(t, 60, commit.CacheReadTokens)
				require.NotNil(t, commit.Usage)
				require.EqualValues(t, 40, commit.Usage.Canonical.UncachedInputTokens)
				require.EqualValues(t, 60, commit.Usage.Canonical.CacheReadTokens)
				require.EqualValues(t, 4, commit.Usage.Canonical.OutputTokens)
			})
		}
	}
}

func TestDeepSeekTierChatSendsFrozenOutputLimit(t *testing.T) {
	t.Setenv("PROVIDER_DISABLE_SSRF_CHECK", "true")
	for _, tc := range []struct {
		name, fields string
		limit        int64
	}{
		{"native limit", `"max_tokens":64`, 64},
		{"completion limit", `"max_completion_tokens":32`, 32},
		{"completion wins", `"max_tokens":64,"max_completion_tokens":32`, 32},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var sent map[string]jsonx.RawMessage
			upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				require.NoError(t, jsonx.NewDecoder(r.Body).Decode(&sent))
				_, _ = fmt.Fprint(w, `{"id":"deepseek-limit","choices":[],"usage":{"prompt_tokens":1,"completion_tokens":1,"total_tokens":2,"prompt_cache_hit_tokens":0,"prompt_cache_miss_tokens":1}}`)
			}))
			defer upstream.Close()
			identity, channel := rawIdentityClient{}, rawChannelClient{baseURL: upstream.URL + "/v1", key: "test-upstream", chType: relayprovider.ChannelTypeDeepSeek}
			billing := &rawBillingClient{tierPricingVersion: 1}
			uc := relaybiz.NewRelayUsecase(relaydata.NewIdentityAdapter(identity), relaydata.NewChannelAdapter(channel), nil, &relaybiz.RetryPolicy{MaxAttempts: 1})
			s := NewHTTPServer(identity, channel, billing, relayprovider.NewProviderFactory(time.Second), uc)
			r := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", strings.NewReader(`{"model":"deepseek-flash","messages":[{"role":"user","content":"hi"}],`+tc.fields+`}`))
			r.Header.Set("Authorization", "Bearer user-token")
			w := httptest.NewRecorder()
			s.handleChatCompletions(w, r)
			require.Equal(t, http.StatusOK, w.Code, w.Body.String())
			require.Len(t, billing.reserveRequests, 1)
			require.Equal(t, tc.limit, billing.reserveRequests[0].OutputTokenBound)
			var forwarded int64
			require.NoError(t, jsonx.Unmarshal(sent["max_tokens"], &forwarded))
			require.Equal(t, tc.limit, forwarded)
			require.NotContains(t, sent, "max_completion_tokens")
			require.Equal(t, 1, billing.commits)
		})
	}
}

func TestDeepSeekTierResponsesJSONAndStreamEvidence(t *testing.T) {
	t.Setenv("PROVIDER_DISABLE_SSRF_CHECK", "true")
	for _, stream := range []bool{false, true} {
		for _, truncated := range []bool{false, true} {
			if truncated && !stream {
				continue
			}
			t.Run(fmt.Sprintf("stream=%t/truncated=%t", stream, truncated), func(t *testing.T) {
				response := `{"id":"deepseek-responses","object":"response","status":"completed","usage":{"input_tokens":100,"output_tokens":4,"total_tokens":104,"input_tokens_details":{"cached_tokens":60}}}`
				upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					require.Equal(t, "/v1/responses", r.URL.Path)
					if stream {
						w.Header().Set("Content-Type", "text/event-stream")
						if truncated {
							_, _ = fmt.Fprint(w, "event: response.output_text.delta\ndata: {\"type\":\"response.output_text.delta\",\"delta\":\"hello\"}\n\n")
							return
						}
						_, _ = fmt.Fprintf(w, "event: response.completed\ndata: {\"type\":\"response.completed\",\"response\":%s}\n\n", response)
						return
					}
					_, _ = fmt.Fprint(w, response)
				}))
				defer upstream.Close()
				identity, channel := rawIdentityClient{}, rawChannelClient{baseURL: upstream.URL + "/v1", key: "test-upstream", chType: relayprovider.ChannelTypeDeepSeek}
				billing, logs := &rawBillingClient{tierPricingVersion: 1}, &rawLogClient{}
				uc := relaybiz.NewRelayUsecase(relaydata.NewIdentityAdapter(identity), relaydata.NewChannelAdapter(channel), nil, &relaybiz.RetryPolicy{MaxAttempts: 1})
				s := NewHTTPServer(identity, channel, billing, relayprovider.NewProviderFactory(time.Second), uc, logs)
				r := httptest.NewRequest(http.MethodPost, "/v1/responses", strings.NewReader(fmt.Sprintf(`{"model":"deepseek-flash","stream":%t,"input":"hi","max_output_tokens":64}`, stream)))
				r.Header.Set("Authorization", "Bearer user-token")
				w := httptest.NewRecorder()
				s.handleResponsesCreateLike(w, r, "/responses")
				require.Equal(t, http.StatusOK, w.Code, w.Body.String())
				require.Len(t, billing.reserveRequests, 1)
				require.Equal(t, "openai_responses", billing.reserveRequests[0].ServiceTierProtocol)
				require.EqualValues(t, 64, billing.reserveRequests[0].OutputTokenBound)
				require.Positive(t, billing.reserveRequests[0].InputTokenBound)
				if truncated {
					require.Zero(t, billing.commits)
					require.Equal(t, 1, billing.releases)
					return
				}
				require.Equal(t, 1, billing.commits)
				require.Zero(t, billing.releases)
				commit := billing.commitRequests[0]
				require.Empty(t, commit.ActualServiceTier)
				require.Equal(t, "missing", commit.ServiceTierStatus)
				require.EqualValues(t, 40, commit.Usage.Canonical.UncachedInputTokens)
				require.EqualValues(t, 60, commit.Usage.Canonical.CacheReadTokens)
				require.EqualValues(t, 4, commit.Usage.Canonical.OutputTokens)
			})
		}
	}
}

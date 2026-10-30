package server

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	relayprovider "micro-one-api/domain/upstream/provider"
	relaybiz "micro-one-api/internal/biz"
	relaydata "micro-one-api/internal/data"
	"micro-one-api/pkg/jsonx"
)

func TestTierChatAmbiguousFieldsCannotReachUpstream(t *testing.T) {
	t.Setenv("PROVIDER_DISABLE_SSRF_CHECK", "true")
	for _, channelType := range []int32{relayprovider.ChannelTypeOpenAI, relayprovider.ChannelTypeDeepSeek} {
		for _, field := range []string{
			`"MAX_TOKENS":999`, `"MAX_COMPLETION_TOKENS":999`,
			`"max_tokens":999`, `"max_completion_tokens":999`,
			`"\u006dax_tokens":999`, `"max_token\u017f":999`,
			`"TOOLS":[{"type":"function","function":{"name":"hidden"}}]`,
			`"TOOL_CHOICE":"auto"`, `"MODEL":"hidden-model"`,
			`"MESSAGES":[{"role":"user","content":"hidden"}]`,
			`"SERVICE_TIER":"priority"`,
		} {
			t.Run(fmt.Sprintf("channel=%d/%s", channelType, field), func(t *testing.T) {
				calls := 0
				upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					calls++
					_, _ = fmt.Fprint(w, `{"id":"unsafe","choices":[],"usage":{"prompt_tokens":1,"completion_tokens":1,"total_tokens":2}}`)
				}))
				defer upstream.Close()
				identity, channel := rawIdentityClient{}, rawChannelClient{baseURL: upstream.URL + "/v1", key: "fixture", chType: channelType}
				// A mismatched owner fixture still cannot bypass the Relay's
				// enabled-pricing request guard by returning an active contract.
				billing := &rawBillingClient{tierPricingVersion: 1}
				uc := relaybiz.NewRelayUsecase(relaydata.NewIdentityAdapter(identity), relaydata.NewChannelAdapter(channel), nil, &relaybiz.RetryPolicy{MaxAttempts: 1})
				s := NewHTTPServer(identity, channel, billing, relayprovider.NewProviderFactory(time.Second), uc)
				body := `{"model":"fixture-model","messages":[{"role":"user","content":"hi"}],"max_tokens":64,"max_completion_tokens":32,` + field + `}`
				r := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", strings.NewReader(body))
				r.Header.Set("Authorization", "Bearer user-token")
				w := httptest.NewRecorder()
				s.handleChatCompletions(w, r)
				require.Equal(t, http.StatusBadRequest, w.Code, w.Body.String())
				require.Zero(t, calls)
				require.Zero(t, billing.commits)
				require.Equal(t, 1, billing.releases)
			})
		}
	}
}

func TestTierResponsesAmbiguousFieldsCannotReachUpstream(t *testing.T) {
	t.Setenv("PROVIDER_DISABLE_SSRF_CHECK", "true")
	for _, channelType := range []int32{relayprovider.ChannelTypeOpenAI, relayprovider.ChannelTypeDeepSeek} {
		for _, field := range []string{
			`"MAX_OUTPUT_TOKENS":999`, `"max_output_tokens":999`,
			`"INPUT":"hidden"`, `"input":"hidden"`,
			`"TOOLS":[{"type":"web_search"}]`, `"TOOL_CHOICE":"auto"`,
			`"MODEL":"hidden-model"`, `"PREVIOUS_RESPONSE_ID":"remote-state"`,
			`"CONVERSATION":"remote-state"`,
		} {
			t.Run(fmt.Sprintf("channel=%d/%s", channelType, field), func(t *testing.T) {
				calls := 0
				upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					calls++
					_, _ = fmt.Fprint(w, `{"id":"unsafe","object":"response","status":"completed","usage":{"input_tokens":1,"output_tokens":1,"total_tokens":2}}`)
				}))
				defer upstream.Close()
				identity, channel := rawIdentityClient{}, rawChannelClient{baseURL: upstream.URL + "/v1", key: "fixture", chType: channelType}
				billing := &rawBillingClient{tierPricingVersion: 1}
				uc := relaybiz.NewRelayUsecase(relaydata.NewIdentityAdapter(identity), relaydata.NewChannelAdapter(channel), nil, &relaybiz.RetryPolicy{MaxAttempts: 1})
				s := NewHTTPServer(identity, channel, billing, relayprovider.NewProviderFactory(time.Second), uc)
				body := `{"model":"fixture-model","input":"hi","max_output_tokens":32,` + field + `}`
				r := httptest.NewRequest(http.MethodPost, "/v1/responses", strings.NewReader(body))
				r.Header.Set("Authorization", "Bearer user-token")
				w := httptest.NewRecorder()
				s.handleResponsesCreateLike(w, r, "/responses")
				require.Equal(t, http.StatusBadRequest, w.Code, w.Body.String())
				require.Zero(t, calls)
				require.Zero(t, billing.commits)
				require.Equal(t, 1, billing.releases)
			})
		}
	}
}

func TestTierChatForwardedLimitUsesFrozenBound(t *testing.T) {
	t.Setenv("PROVIDER_DISABLE_SSRF_CHECK", "true")
	for _, channelType := range []int32{relayprovider.ChannelTypeOpenAI, relayprovider.ChannelTypeDeepSeek} {
		for _, enabled := range []bool{false, true} {
			t.Run(fmt.Sprintf("channel=%d/tier=%t", channelType, enabled), func(t *testing.T) {
				var sent map[string]jsonx.RawMessage
				upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					require.NoError(t, jsonx.NewDecoder(r.Body).Decode(&sent))
					_, _ = fmt.Fprint(w, `{"id":"limit","choices":[],"usage":{"prompt_tokens":1,"completion_tokens":1,"total_tokens":2}}`)
				}))
				defer upstream.Close()
				identity, channel := rawIdentityClient{}, rawChannelClient{baseURL: upstream.URL + "/v1", key: "fixture", chType: channelType}
				billing := &rawBillingClient{}
				if enabled {
					billing.tierPricingVersion = 1
				}
				uc := relaybiz.NewRelayUsecase(relaydata.NewIdentityAdapter(identity), relaydata.NewChannelAdapter(channel), nil, &relaybiz.RetryPolicy{MaxAttempts: 1})
				s := NewHTTPServer(identity, channel, billing, relayprovider.NewProviderFactory(time.Second), uc)
				body := `{"model":"fixture-model","messages":[{"role":"user","content":"hi"}],"max_tokens":64,"max_completion_tokens":32}`
				if !enabled {
					body = strings.TrimSuffix(body, "}") + `,"MAX_TOKENS":999}`
				}
				r := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", strings.NewReader(body))
				r.Header.Set("Authorization", "Bearer user-token")
				w := httptest.NewRecorder()
				s.handleChatCompletions(w, r)
				require.Equal(t, http.StatusOK, w.Code, w.Body.String())
				require.Len(t, billing.reserveRequests, 1)
				if enabled {
					field, absent := "max_completion_tokens", "max_tokens"
					if channelType == relayprovider.ChannelTypeDeepSeek {
						field, absent = absent, field
					}
					var limit int64
					require.NoError(t, jsonx.Unmarshal(sent[field], &limit))
					require.Equal(t, billing.reserveRequests[0].OutputTokenBound, limit)
					require.NotContains(t, sent, absent)
				} else {
					// The legacy, unpriced path keeps both client-supplied fields.
					require.Contains(t, sent, "max_tokens")
					require.Contains(t, sent, "max_completion_tokens")
					var legacyLimit int64
					require.NoError(t, jsonx.Unmarshal(sent["max_tokens"], &legacyLimit))
					require.EqualValues(t, 999, legacyLimit)
				}
				require.Equal(t, 1, billing.commits)
			})
		}
	}
}

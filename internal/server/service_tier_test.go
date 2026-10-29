package server

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	kratoserrors "github.com/go-kratos/kratos/v3/errors"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc"
	billingv1 "micro-one-api/api/billing/v1"
	billingdomain "micro-one-api/domain/billing"
	relayprovider "micro-one-api/domain/upstream/provider"
	relaybiz "micro-one-api/internal/biz"
	relaydata "micro-one-api/internal/data"
)

func TestServiceTierTextBound(t *testing.T) {
	for _, tc := range []struct {
		name, body, protocol string
		bound, invalid       bool
	}{
		{"chat", `{"messages":[{"role":"user","content":"hello"}],"max_completion_tokens":32,"service_tier":"priority"}`, "openai_chat", true, false},
		{"responses", `{"input":"hello","max_output_tokens":32}`, "openai_responses", true, false},
		{"responses parts", `{"input":[{"role":"user","content":[{"type":"input_text","text":"hi"}]}],"max_output_tokens":32}`, "openai_responses", true, false},
		{"output required", `{"input":"hello"}`, "openai_responses", false, false},
		{"remote state", `{"input":"hello","previous_response_id":"old","max_output_tokens":32}`, "openai_responses", false, false},
		{"media", `{"input":[{"role":"user","content":[{"type":"input_image","image_url":"https://example.com/a"}]}],"max_output_tokens":32}`, "openai_responses", false, false},
		{"tools", `{"input":"hi","tools":[{"type":"web_search"}],"max_output_tokens":32}`, "openai_responses", false, false},
		{"multiple outputs", `{"messages":[{"role":"user","content":"hi"}],"max_completion_tokens":32,"n":2}`, "openai_chat", false, false},
		{"unsupported", `{"input":"hello","max_output_tokens":32}`, "unsupported", false, false},
		{"future request tier", `{"service_tier":"future"}`, "openai_responses", false, true},
		{"wrong type", `{"service_tier":7}`, "openai_responses", false, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			r, err := readServiceTierRequest([]byte(tc.body), tc.protocol)
			if tc.invalid {
				require.ErrorIs(t, err, errServiceTierRequest)
				return
			}
			require.NoError(t, err)
			require.Equal(t, tc.bound, r.InputTokenBound > 0)
			if tc.bound {
				require.Greater(t, r.InputTokenBound, int64(len(tc.body)))
				require.EqualValues(t, 32, r.OutputTokenBound)
			}
		})
	}
}

func TestServiceTierObservationNeverUsesRequestedValue(t *testing.T) {
	ctx := billingdomain.WithServiceTierRequest(context.Background(), billingdomain.ServiceTierRequest{RequestedServiceTier: "priority"})
	var in usageLogInput
	in.applyTier(ctx, tierObservation{})
	require.Equal(t, "priority", in.RequestedServiceTier)
	require.Empty(t, in.ActualServiceTier)
	require.Equal(t, "missing", in.ServiceTierStatus)
	tracker := newResponsesStreamUsageTracker(rawUsage{})
	tracker.ObserveBytes([]byte("data: {\"response\":{\"service_tier\":\"fl"))
	tracker.ObserveBytes([]byte("ex\"}}\n\n"))
	require.Equal(t, "flex", tracker.tier.actual)
	require.Equal(t, "reported", tracker.tier.status)
	tracker.Observe([]byte(`{"service_tier":"priority"}`))
	require.Empty(t, tracker.tier.actual)
	require.Equal(t, "conflict", tracker.tier.status)
	var future tierObservation
	future.observe("future")
	require.Equal(t, "future", future.actual)
	require.Equal(t, "unknown", future.status)
	future.observe(strings.Repeat("x", 33))
	require.Empty(t, future.actual)
	require.Equal(t, "unknown", future.status)
}

func TestServiceTierObservationCannotRecoverAfterInvalidEvidence(t *testing.T) {
	for _, tc := range []struct {
		name   string
		values []string
		status string
	}{
		{"conflict then oversized then default", []string{"priority", "flex", strings.Repeat("x", 33), "default"}, "conflict"},
		{"oversized then default", []string{strings.Repeat("x", 33), "default"}, "unknown"},
		{"reported then oversized then default", []string{"priority", strings.Repeat("x", 33), "default"}, "unknown"},
		{"future tier then default", []string{"future", "default"}, "conflict"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			tracker := newResponsesStreamUsageTracker(rawUsage{})
			for _, value := range tc.values {
				body := fmt.Sprintf("data: {\"response\":{\"service_tier\":%q}}\n\n", value)
				tracker.ObserveBytes([]byte(body))
			}
			require.Empty(t, tracker.tier.actual)
			require.Equal(t, tc.status, tracker.tier.status)
		})
	}
}

func TestServiceTierMalformedObservationCannotRestoreKnownTier(t *testing.T) {
	for _, malformed := range []string{"7", "null", `{}`, `[]`} {
		tracker := newResponsesStreamUsageTracker(rawUsage{})
		tracker.Observe([]byte(`{"response":{"service_tier":"default"}}`))
		tracker.Observe([]byte(`{"response":{"service_tier":` + malformed + `}}`))
		tracker.Observe([]byte(`{"response":{"service_tier":"default"}}`))
		require.Empty(t, tracker.tier.actual)
		require.Equal(t, "unknown", tracker.tier.status)
	}
}

func TestServiceTierChatStreamSettlementMatrix(t *testing.T) {
	for _, tc := range []struct {
		name, chunks, actual, status string
		committed                    bool
	}{
		{"conflict remains after oversized", tierObservationTestStream("priority", "flex", strings.Repeat("x", 33), "default"), "", "conflict", true},
		{"oversized cannot restore default", tierObservationTestStream(strings.Repeat("x", 33), "default"), "", "unknown", true},
		{"null tier cannot restore default", "data: {\"service_tier\":null,\"choices\":[]}\n\ndata: {\"service_tier\":\"default\",\"usage\":{\"prompt_tokens\":3,\"completion_tokens\":2,\"total_tokens\":5},\"choices\":[]}\n\ndata: [DONE]\n\n", "", "unknown", true},
		{"fallback", "data: {\"service_tier\":\"flex\",\"usage\":{\"prompt_tokens\":3,\"completion_tokens\":2,\"total_tokens\":5},\"choices\":[]}\n\ndata: [DONE]\n\n", "flex", "reported", true},
		{"missing tier", "data: {\"usage\":{\"prompt_tokens\":3,\"completion_tokens\":2,\"total_tokens\":5},\"choices\":[]}\n\ndata: [DONE]\n\n", "", "missing", true},
		{"future tier", "data: {\"service_tier\":\"future\",\"usage\":{\"prompt_tokens\":3,\"completion_tokens\":2,\"total_tokens\":5},\"choices\":[]}\n\ndata: [DONE]\n\n", "future", "unknown", true},
		{"missing usage", "data: {\"service_tier\":\"priority\",\"choices\":[]}\n\ndata: [DONE]\n\n", "", "", false},
		{"missing terminal", "data: {\"service_tier\":\"flex\",\"usage\":{\"prompt_tokens\":3,\"completion_tokens\":2,\"total_tokens\":5},\"choices\":[]}\n\n", "", "", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", "text/event-stream")
				_, _ = fmt.Fprint(w, tc.chunks)
			}))
			defer upstream.Close()
			provider, err := relayprovider.NewOpenAIProviderAllowLocal(upstream.URL, "test", time.Second)
			require.NoError(t, err)
			billing := &rawBillingClient{}
			logs := &rawLogClient{}
			s := &HTTPServer{billingClient: billing, logClient: logs}
			ctx := billingdomain.WithServiceTierRequest(context.Background(), billingdomain.ServiceTierRequest{RequestedServiceTier: "priority"})
			req := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", strings.NewReader("{}")).WithContext(ctx)
			reservation := &billingv1.ReserveQuotaResponse{ReservationId: "tier-test", ServiceTierPricingVersion: 1}
			input := usageLogInput{}
			input.applyReservation(reservation)
			_ = s.handleStreamingResponse(httptest.NewRecorder(), req, provider, &relayprovider.ChatCompletionsRequest{Model: "gpt-5.1", Stream: true}, reservation, input)
			if !tc.committed {
				require.Equal(t, 1, billing.releases)
				require.Zero(t, billing.commits)
				require.Len(t, logs.entries, 1)
				require.Equal(t, "priority", logs.entries[0].RequestedServiceTier)
				require.Zero(t, logs.entries[0].Quota)
				require.Contains(t, logs.entries[0].Message, "settlement_outcome=released/")
				return
			}
			require.Equal(t, 1, billing.commits)
			require.Zero(t, billing.releases)
			require.Equal(t, tc.actual, billing.commitRequests[0].ActualServiceTier)
			require.Equal(t, tc.status, billing.commitRequests[0].ServiceTierStatus)
			require.EqualValues(t, 1, billing.commitRequests[0].UsageContractVersion)
		})
	}
}

func tierObservationTestStream(values ...string) string {
	var stream strings.Builder
	for _, value := range values {
		_, _ = fmt.Fprintf(&stream, "data: {\"service_tier\":%q,\"choices\":[]}\n\n", value)
	}
	stream.WriteString("data: {\"usage\":{\"prompt_tokens\":3,\"completion_tokens\":2,\"total_tokens\":5},\"choices\":[]}\n\ndata: [DONE]\n\n")
	return stream.String()
}

type tierCancellingWriter struct {
	*httptest.ResponseRecorder
	cancel context.CancelFunc
}

func (w *tierCancellingWriter) Write(body []byte) (int, error) {
	w.cancel()
	return 0, context.Canceled
}

func TestServiceTierCancellationRetainsObservationAndReleases(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = fmt.Fprint(w, "data: {\"service_tier\":\"flex\",\"usage\":{\"prompt_tokens\":3,\"completion_tokens\":2},\"choices\":[]}\n\ndata: [DONE]\n\n")
	}))
	defer upstream.Close()
	provider, err := relayprovider.NewOpenAIProviderAllowLocal(upstream.URL, "test", time.Second)
	require.NoError(t, err)
	billing, logs := &rawBillingClient{}, &rawLogClient{}
	s := &HTTPServer{billingClient: billing, logClient: logs}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	ctx = billingdomain.WithServiceTierRequest(ctx, billingdomain.ServiceTierRequest{RequestedServiceTier: "priority"})
	req := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", strings.NewReader("{}")).WithContext(ctx)
	reservation := &billingv1.ReserveQuotaResponse{ReservationId: "cancelled-tier", ServiceTierPricingVersion: 1}
	in := usageLogInput{}
	in.applyReservation(reservation)
	err = s.handleStreamingResponse(&tierCancellingWriter{ResponseRecorder: httptest.NewRecorder(), cancel: cancel}, req, provider, &relayprovider.ChatCompletionsRequest{Model: "gpt-5.1", Stream: true}, reservation, in)
	require.Error(t, err)
	require.Equal(t, 1, billing.releases)
	require.Zero(t, billing.commits)
	require.Len(t, logs.entries, 1)
	require.Equal(t, "priority", logs.entries[0].RequestedServiceTier)
	require.Equal(t, "flex", logs.entries[0].ActualServiceTier)
	require.Contains(t, logs.entries[0].Message, "released/incomplete_or_cancelled_stream")
}

type tierCapabilityBillingClient struct {
	rawBillingClient
	capability, contract int32
}

func (c *tierCapabilityBillingClient) GetRoutingCapabilities(context.Context, *billingv1.GetRoutingCapabilitiesRequest, ...grpc.CallOption) (*billingv1.GetRoutingCapabilitiesResponse, error) {
	return &billingv1.GetRoutingCapabilitiesResponse{ServiceTierPricingVersion: c.capability}, nil
}

func (c *tierCapabilityBillingClient) ReserveQuota(ctx context.Context, req *billingv1.ReserveQuotaRequest, opts ...grpc.CallOption) (*billingv1.ReserveQuotaResponse, error) {
	response, err := c.rawBillingClient.ReserveQuota(ctx, req, opts...)
	response.ServiceTierContractVersion = c.contract
	return response, err
}

func TestServiceTierBillingRollingUpgradeAdmission(t *testing.T) {
	for _, tc := range []struct {
		name, requested              string
		capability, contract, frozen int32
		ok                           bool
		reserves, releases           int
	}{
		{"old capability", "", 0, 0, 0, false, 0, 0},
		{"old replica after capability", "auto", 1, 0, 0, false, 1, 1},
		{"disabled default compatible", "", 1, 1, 0, true, 1, 0},
		{"priority must freeze", "priority", 1, 1, 0, false, 1, 1},
		{"priority frozen", "priority", 1, 1, 1, true, 1, 0},
	} {
		t.Run(tc.name, func(t *testing.T) {
			client := &tierCapabilityBillingClient{capability: tc.capability, contract: tc.contract, rawBillingClient: rawBillingClient{tierPricingVersion: tc.frozen}}
			s := &HTTPServer{billingClient: client}
			ctx := billingdomain.WithServiceTierRequest(context.Background(), billingdomain.ServiceTierRequest{RequestedServiceTier: tc.requested, Protocol: "openai_responses", Version: 1, InputTokenBound: 1000, OutputTokenBound: 100})
			_, err := s.reserveQuota(ctx, "1", "tier-contract", 20, "gpt-5.1", "11", 0)
			if tc.ok {
				require.NoError(t, err)
			} else {
				require.Error(t, err)
			}
			require.Len(t, client.reserveRequests, tc.reserves)
			require.Equal(t, tc.releases, client.releases)
		})
	}
}

func TestServiceTierResponsesStreamPreservesReportedZeroAndRejectsPartialUsage(t *testing.T) {
	t.Setenv("PROVIDER_DISABLE_SSRF_CHECK", "true")
	for _, tc := range []struct {
		name, usage   string
		input, output int64
		complete      bool
	}{
		{"zero output", `{"input_tokens":3,"output_tokens":0,"total_tokens":3}`, 3, 0, true},
		{"zero input", `{"input_tokens":0,"output_tokens":2,"total_tokens":2}`, 0, 2, true},
		{"both zero", `{"input_tokens":0,"output_tokens":0,"total_tokens":0}`, 0, 0, true},
		{"missing output", `{"input_tokens":3,"total_tokens":3}`, 0, 0, false},
		{"string cached", `{"input_tokens":3,"output_tokens":2,"input_tokens_details":{"cached_tokens":"2"}}`, 0, 0, false},
		{"fractional cached", `{"input_tokens":3,"output_tokens":2,"input_tokens_details":{"cached_tokens":1.5}}`, 0, 0, false},
		{"null cached", `{"input_tokens":3,"output_tokens":2,"input_tokens_details":{"cached_tokens":null}}`, 0, 0, false},
		{"missing input", `{"output_tokens":2,"total_tokens":2}`, 0, 0, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", "text/event-stream")
				_, _ = fmt.Fprintf(w, "event: response.completed\ndata: {\"type\":\"response.completed\",\"response\":{\"id\":\"resp-tier-zero\",\"status\":\"completed\",\"service_tier\":\"flex\",\"usage\":%s}}\n\n", tc.usage)
			}))
			defer upstream.Close()
			identity, channel := rawIdentityClient{}, rawChannelClient{baseURL: upstream.URL + "/v1", key: "test-upstream"}
			billing, logs := &rawBillingClient{tierPricingVersion: 1}, &rawLogClient{}
			uc := relaybiz.NewRelayUsecase(relaydata.NewIdentityAdapter(identity), relaydata.NewChannelAdapter(channel), nil, &relaybiz.RetryPolicy{MaxAttempts: 1})
			s := NewHTTPServer(identity, channel, billing, relayprovider.NewProviderFactory(time.Second), uc, logs)
			r := httptest.NewRequest(http.MethodPost, "/v1/responses", strings.NewReader(`{"model":"gpt-5.1","service_tier":"priority","stream":true,"input":"hi","max_output_tokens":64}`))
			r.Header.Set("Authorization", "Bearer user-token")
			w := httptest.NewRecorder()
			s.handleResponsesCreateLike(w, r, "/responses")
			require.Equal(t, http.StatusOK, w.Code)
			if !tc.complete {
				require.Zero(t, billing.commits)
				require.Equal(t, 1, billing.releases)
				return
			}
			require.Equal(t, 1, billing.commits)
			require.Zero(t, billing.releases)
			commit := billing.commitRequests[0]
			require.Equal(t, tc.input, commit.Usage.Reported.PromptTokens)
			require.Equal(t, tc.output, commit.Usage.Reported.OutputTokens)
			require.Equal(t, tc.input, commit.Usage.Canonical.UncachedInputTokens)
			require.Equal(t, tc.output, commit.Usage.Canonical.OutputTokens)
		})
	}
}

func TestServiceTierChatRejectsUnprovenUsage(t *testing.T) {
	t.Setenv("PROVIDER_DISABLE_SSRF_CHECK", "true")
	for _, stream := range []bool{false, true} {
		for _, tc := range []struct {
			name, usage string
			priorValid  bool
		}{
			{"cache exceeds input", `{"prompt_tokens":3,"completion_tokens":2,"total_tokens":5,"prompt_tokens_details":{"cached_tokens":99}}`, false},
			{"negative cache detail", `{"prompt_tokens":3,"completion_tokens":2,"total_tokens":5,"prompt_tokens_details":{"cached_tokens":-1}}`, false},
			{"negative creation detail", `{"prompt_tokens":3,"completion_tokens":2,"total_tokens":5,"prompt_tokens_details":{"cache_creation_5m_tokens":-1}}`, false},
			{"null cache detail", `{"prompt_tokens":3,"completion_tokens":2,"total_tokens":5,"prompt_tokens_details":{"cached_tokens":null}}`, false},
			{"null total", `{"prompt_tokens":3,"completion_tokens":2,"total_tokens":null}`, false},
			{"null cache details object", `{"prompt_tokens":3,"completion_tokens":2,"prompt_tokens_details":null}`, false},
			{"partial", `{"prompt_tokens":3,"total_tokens":3}`, false},
			{"partial after valid", `{"prompt_tokens":3,"total_tokens":3}`, true},
		} {
			t.Run(fmt.Sprintf("stream=%t/%s", stream, tc.name), func(t *testing.T) {
				upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					if stream {
						w.Header().Set("Content-Type", "text/event-stream")
						if tc.priorValid {
							_, _ = fmt.Fprint(w, "data: {\"service_tier\":\"flex\",\"choices\":[],\"usage\":{\"prompt_tokens\":3,\"completion_tokens\":2,\"total_tokens\":5}}\n\n")
						}
						_, _ = fmt.Fprintf(w, "data: {\"service_tier\":\"flex\",\"choices\":[],\"usage\":%s}\n\ndata: [DONE]\n\n", tc.usage)
						return
					}
					_, _ = fmt.Fprintf(w, `{"id":"tier-invalid","service_tier":"flex","choices":[],"usage":%s}`, tc.usage)
				}))
				defer upstream.Close()
				identity, channel := rawIdentityClient{}, rawChannelClient{baseURL: upstream.URL + "/v1", key: "test-upstream"}
				billing, logs := &rawBillingClient{tierPricingVersion: 1}, &rawLogClient{}
				uc := relaybiz.NewRelayUsecase(relaydata.NewIdentityAdapter(identity), relaydata.NewChannelAdapter(channel), nil, &relaybiz.RetryPolicy{MaxAttempts: 1})
				s := NewHTTPServer(identity, channel, billing, relayprovider.NewProviderFactory(time.Second), uc, logs)
				r := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", strings.NewReader(fmt.Sprintf(`{"model":"gpt-5.1","service_tier":"priority","stream":%t,"messages":[{"role":"user","content":"hi"}],"max_completion_tokens":64}`, stream)))
				r.Header.Set("Authorization", "Bearer user-token")
				w := httptest.NewRecorder()
				s.handleChatCompletions(w, r)
				require.Equal(t, http.StatusOK, w.Code)
				require.Zero(t, billing.commits)
				require.Equal(t, 1, billing.releases)
				require.Len(t, logs.entries, 1)
				require.Contains(t, logs.entries[0].Message, "settlement_outcome=released/")
			})
		}
	}
}

type tierRejectedBillingClient struct {
	rawBillingClient
	reason   string
	reserves atomic.Int32
}

func (c *tierRejectedBillingClient) ReserveQuota(context.Context, *billingv1.ReserveQuotaRequest, ...grpc.CallOption) (*billingv1.ReserveQuotaResponse, error) {
	c.reserves.Add(1)
	return nil, kratoserrors.BadRequest(c.reason, "controlled tier admission rejection")
}

func TestServiceTierAdmissionErrorsRemainBadRequest(t *testing.T) {
	for _, endpoint := range []string{"/v1/chat/completions", "/v1/responses"} {
		for _, reason := range []string{"SERVICE_TIER_UNSUPPORTED", "SERVICE_TIER_BOUND_REQUIRED"} {
			t.Run(endpoint+"/"+reason, func(t *testing.T) {
				identity, channel := rawIdentityClient{}, rawChannelClient{}
				billing := &tierRejectedBillingClient{reason: reason}
				uc := relaybiz.NewRelayUsecase(relaydata.NewIdentityAdapter(identity), relaydata.NewChannelAdapter(channel), nil, &relaybiz.RetryPolicy{MaxAttempts: 3, RetryableStatus: map[int]bool{http.StatusBadRequest: true}})
				s := NewHTTPServer(identity, channel, billing, relayprovider.NewProviderFactory(time.Second), uc)
				body := `{"model":"gpt-5.1","service_tier":"priority","input":"hi","max_output_tokens":64}`
				if endpoint == "/v1/chat/completions" {
					body = `{"model":"gpt-5.1","service_tier":"priority","messages":[{"role":"user","content":"hi"}],"max_completion_tokens":64}`
				}
				r := httptest.NewRequest(http.MethodPost, endpoint, strings.NewReader(body))
				r.Header.Set("Authorization", "Bearer user-token")
				w := httptest.NewRecorder()
				if endpoint == "/v1/chat/completions" {
					s.handleChatCompletions(w, r)
				} else {
					s.handleResponsesCreateLike(w, r, "/responses")
				}
				require.Equal(t, http.StatusBadRequest, w.Code)
				require.Contains(t, w.Body.String(), "service_tier")
				require.NotContains(t, w.Body.String(), "insufficient quota")
				// The policy below treats 400 as retryable, which is why the
				// admission error must opt out explicitly: one reservation, no
				// failover to another source at the wrong price.
				require.EqualValues(t, 1, billing.reserves.Load())
			})
		}
	}
}

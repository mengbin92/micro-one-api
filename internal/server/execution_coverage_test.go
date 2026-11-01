package server

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	khttp "github.com/go-kratos/kratos/v3/transport/http"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc"
	billingv1 "micro-one-api/api/billing/v1"
	channelv1 "micro-one-api/api/channel/v1"
	billingdomain "micro-one-api/domain/billing"
	"micro-one-api/domain/execution"
	"micro-one-api/domain/upstream/evidence"
	"micro-one-api/domain/upstream/provider"
	"micro-one-api/internal/biz"
	"micro-one-api/internal/data"
	"micro-one-api/pkg/jsonx"
)

type coverageOutbox struct {
	biz.ExecutionOutboxRepo
	events       []execution.Event
	sent         []execution.Event
	acknowledged []string
}

func (r *coverageOutbox) Admit(_ context.Context, e execution.Event) error {
	if _, _, err := e.Encode(); err != nil {
		return err
	}
	r.events = append(r.events, e)
	return nil
}

func (r *coverageOutbox) Put(_ context.Context, e execution.Event) error {
	if _, _, err := e.Encode(); err != nil {
		return err
	}
	r.events = append(r.events, e)
	return nil
}

func (r *coverageOutbox) Send(_ context.Context, e execution.Event) (execution.Receipt, error) {
	_, digest, err := e.Encode()
	if err != nil {
		return execution.Receipt{}, err
	}
	r.sent = append(r.sent, e)
	return execution.Receipt{Received: true, Digest: digest, SettlementAccepted: true}, nil
}

func (r *coverageOutbox) Acknowledge(_ context.Context, id, digest string) error {
	r.acknowledged = append(r.acknowledged, id+"/"+digest)
	return nil
}

type coverageBillingClient struct {
	rawBillingClient
	capabilityRequests int
	executionCapable   bool
}

func (c *coverageBillingClient) GetRoutingCapabilities(context.Context, *billingv1.GetRoutingCapabilitiesRequest, ...grpc.CallOption) (*billingv1.GetRoutingCapabilitiesResponse, error) {
	c.capabilityRequests++
	caps := &billingv1.GetRoutingCapabilitiesResponse{ServiceTierPricingVersion: 1}
	if c.executionCapable {
		caps.ExecutionDeliveryVersion, caps.UsageContractVersion, caps.SettlementPolicyVersion = 1, 2, execution.Policy
	}
	return caps, nil
}

func (c *coverageBillingClient) ReserveQuota(ctx context.Context, req *billingv1.ReserveQuotaRequest, opts ...grpc.CallOption) (*billingv1.ReserveQuotaResponse, error) {
	resp, err := c.rawBillingClient.ReserveQuota(ctx, req, opts...)
	if err != nil || req.ExecutionDeliveryVersion == 0 {
		return resp, err
	}
	now := time.Now()
	i := execution.Freeze(execution.Intent{
		ReservationID: resp.ReservationId, RequestID: req.RequestId, RootRequestID: req.RootRequestId,
		AttemptNumber: req.AttemptNumber, Purpose: "user", SourceKind: req.SourceKind, SourceID: 11,
		Model: req.UpstreamModelId, UsageContractVersion: 2, ParserRevision: evidence.ParserRevision,
		ContractRevision: evidence.ContractRevision, CostAlgorithmVersion: execution.AlgorithmVersion,
		Policy: execution.Policy, LeaseUntil: now.Add(time.Minute).Unix(), EvidenceDeadline: now.Add(time.Hour).Unix(), CostBasis: "unknown",
	})
	resp.ExecutionIntentJson, err = jsonx.Marshal(i)
	resp.ExecutionDeliveryVersion, resp.UsageContractVersion, resp.SettlementPolicyVersion = 1, 2, execution.Policy
	return resp, err
}

type coverageChannelClient struct {
	rawChannelClient
	capabilityRequests int
}

func (c *coverageChannelClient) GetUsageCapabilities(context.Context, *channelv1.GetUsageCapabilitiesRequest, ...grpc.CallOption) (*channelv1.GetUsageCapabilitiesResponse, error) {
	c.capabilityRequests++
	return &channelv1.GetUsageCapabilitiesResponse{UsageContractVersion: 2}, nil
}

// This characterizes the registered legacy Responses path with the observed
// production flag combination and explicit fixture admission. The fake owner
// acknowledges valid delivery events; it does not prove live Billing persistence,
// historical flag values, or authorize widening production admission.
// Admitted controls use native Responses only: a durable intent cannot authorize
// the second supplier send required by the separate legacy protocol fallback.
func TestResponsesExecutionCoverageAdmissionAndSendTime(t *testing.T) {
	t.Setenv("PROVIDER_DISABLE_SSRF_CHECK", "true")
	t.Setenv("RELAY_EXECUTION_DELIVERY_V1", "true")
	t.Setenv("RELAY_ROUTING_CONTEXT_V2", "")
	t.Setenv("UPSTREAM_CONTRACTS_V1_ENABLED", "false")
	t.Setenv("RELAY_USAGE_SHADOW", "")
	for _, tc := range []struct {
		name, allowlist, sendTime string
		admitted                  bool
		fallback                  bool
	}{
		{name: "production_flags"},
		{name: "production_flags_legacy_chat_fallback", fallback: true},
		{name: "empty_allowlist_time_on", sendTime: "true"},
		{name: "other_source_time_on", allowlist: "channel:12:deepseek-flash", sendTime: "true"},
		{name: "exact_allowlist_time_off", allowlist: "channel:11:deepseek-flash", admitted: true},
		{name: "exact_allowlist_time_on", allowlist: "channel:11:deepseek-flash", sendTime: "true", admitted: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Setenv("RELAY_NATIVE_USAGE_V2_ALLOWLIST", tc.allowlist)
			t.Setenv("RELAY_UPSTREAM_EXECUTION_TIME_V1", tc.sendTime)
			upstreamCalls := 0
			upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				upstreamCalls++
				if tc.fallback && r.URL.Path == "/v1/responses" {
					w.WriteHeader(http.StatusNotFound)
					_, _ = io.WriteString(w, `{"error":{"message":"endpoint unsupported"}}`)
					return
				}
				if tc.fallback && r.URL.Path == "/v1/chat/completions" {
					w.Header().Set("Content-Type", "text/event-stream")
					_, _ = io.WriteString(w, "data: {\"choices\":[{\"delta\":{\"content\":\"pong\"},\"finish_reason\":null}]}\n\n")
					_, _ = io.WriteString(w, "data: {\"choices\":[{\"delta\":{},\"finish_reason\":\"stop\"}],\"usage\":{\"prompt_tokens\":11,\"completion_tokens\":7,\"total_tokens\":18}}\n\ndata: [DONE]\n\n")
					return
				}
				if r.URL.Path != "/v1/responses" {
					t.Errorf("upstream path = %s, want /v1/responses", r.URL.Path)
					http.Error(w, "unexpected upstream path", http.StatusNotFound)
					return
				}
				w.Header().Set("Content-Type", "text/event-stream")
				_, _ = io.WriteString(w, "data: {\"type\":\"response.output_text.delta\",\"delta\":\"pong\"}\n\n")
				_, _ = io.WriteString(w, "data: {\"type\":\"response.completed\",\"response\":{\"status\":\"completed\",\"usage\":{\"input_tokens\":11,\"output_tokens\":7,\"total_tokens\":18}}}\n\ndata: [DONE]\n\n")
			}))
			defer upstream.Close()
			identity := rawIdentityClient{}
			channel := &coverageChannelClient{rawChannelClient: rawChannelClient{baseURL: upstream.URL + "/v1", key: "fixture", chType: 6, upstreamModel: "deepseek-flash"}}
			billing, logs := &coverageBillingClient{executionCapable: tc.admitted}, &rawLogClient{}
			uc := biz.NewRelayUsecase(data.NewIdentityAdapter(identity), data.NewChannelAdapter(channel), nil, &biz.RetryPolicy{MaxAttempts: 1})
			s := NewHTTPServer(identity, channel, billing, provider.NewProviderFactory(time.Second), uc, logs)
			t.Cleanup(func() {
				s.executionAttempts.Range(func(_, value any) bool {
					if timer := value.(*durableAttempt).expiry; timer != nil {
						timer.Stop()
					}
					return true
				})
			})
			outbox := &coverageOutbox{}
			s.SetExecutionDelivery(biz.NewExecutionDelivery(outbox))
			exporter, summaries := timingSink(t)
			s.SetTimingExporter(exporter)
			srv := khttp.NewServer()
			s.RegisterRoutes(srv)
			req := httptest.NewRequest(http.MethodPost, "/v1/responses", strings.NewReader(`{"model":"deepseek-flash","input":"ping","stream":true}`))
			req.Header.Set("Authorization", "Bearer user-token")
			req.Header.Set("Content-Type", "application/json")
			w := httptest.NewRecorder()
			srv.ServeHTTP(w, req)
			require.Equal(t, http.StatusOK, w.Code, w.Body.String())
			wantCalls := 1
			if tc.fallback {
				wantCalls = 2
			}
			require.Equal(t, wantCalls, upstreamCalls)
			require.Contains(t, w.Body.String(), `"delta":"pong"`)
			require.Len(t, billing.reserveRequests, 1)
			require.Len(t, logs.entries, 1)
			summary := awaitTiming(t, summaries)
			require.Equal(t, "legacy", summary.ExecutionPath)
			reservation := billing.reserveRequests[0]
			t.Logf("path=%s reserve_delivery=%d reserve_usage=%d outbox_events=%d", summary.ExecutionPath, reservation.ExecutionDeliveryVersion, reservation.UsageContractVersion, len(outbox.events))
			// DeepSeek legacy Responses now checks the independent A3 pricing
			// contract. That shared capabilities RPC does not admit execution:
			// non-pilot fixtures advertise no execution support at all.
			require.Equal(t, "openai_responses", reservation.ServiceTierProtocol)
			require.EqualValues(t, 1, reservation.ServiceTierPricingVersion)
			require.Equal(t, 1, billing.capabilityRequests)
			if !tc.admitted {
				require.Zero(t, channel.capabilityRequests)
				require.Zero(t, reservation.ExecutionDeliveryVersion)
				require.Zero(t, reservation.UsageContractVersion)
				require.Empty(t, outbox.events, "delivery/time flags alone do not admit a source")
				require.Len(t, billing.commitRequests, 1)
				require.EqualValues(t, 18, billing.commitRequests[0].ActualTokens)
				return
			}
			require.Equal(t, 1, channel.capabilityRequests)
			require.EqualValues(t, 1, reservation.ExecutionDeliveryVersion)
			require.EqualValues(t, 2, reservation.UsageContractVersion)
			require.Equal(t, execution.Policy, reservation.SettlementPolicyVersion)
			require.Empty(t, billing.commitRequests, "admitted attempts settle through the execution receipt")
			categories := map[string]int{}
			for _, event := range outbox.events {
				categories[event.Category]++
				require.Equal(t, "user", event.Intent.Purpose)
				require.Equal(t, reservation.RequestId, event.Intent.RequestID)
				require.Equal(t, reservation.RootRequestId, event.Intent.RootRequestID)
				require.EqualValues(t, 11, event.Intent.SourceID)
				require.Equal(t, "deepseek-flash", event.Intent.Model)
				if event.Record != nil {
					if tc.sendTime == "true" {
						require.Positive(t, event.Record.StartedAtUnixNano, event.Category)
					} else {
						require.Zero(t, event.Record.StartedAtUnixNano, event.Category)
					}
				}
			}
			require.Equal(t, map[string]int{"intent": 1, "checkpoint": 1, "terminal": 1, "final": 1}, categories)
			require.Len(t, outbox.sent, 1)
			final := outbox.sent[0]
			require.Equal(t, "final", final.Category)
			require.NotNil(t, final.Record)
			require.Equal(t, "verified", final.Record.Observation.Status)
			require.True(t, final.Record.Complete)
			t.Logf("categories=%v send_timestamp_present=%v acknowledged=%d", categories, final.Record.StartedAtUnixNano > 0, len(outbox.acknowledged))
			var payload billingdomain.SettlementPayload
			require.NoError(t, jsonx.Unmarshal(final.Final, &payload))
			require.True(t, payload.Success)
			require.EqualValues(t, 18, payload.ActualTokens)
			_, digest, err := final.Encode()
			require.NoError(t, err)
			require.Equal(t, []string{final.ID + "/" + digest}, outbox.acknowledged)
		})
	}
}

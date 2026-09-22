package server

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"google.golang.org/grpc"
	"google.golang.org/grpc/metadata"
	billingv1 "micro-one-api/api/billing/v1"
	relayv1 "micro-one-api/api/relay/v1"
	"micro-one-api/domain/requesttrace"
	relayprovider "micro-one-api/domain/upstream/provider"
	relaybiz "micro-one-api/internal/biz"
	relayservice "micro-one-api/internal/service"
	"micro-one-api/pkg/jsonx"
)

type legacyAttemptBillingClient struct{ rawBillingClient }

func (c *legacyAttemptBillingClient) ReserveQuota(ctx context.Context, req *billingv1.ReserveQuotaRequest, opts ...grpc.CallOption) (*billingv1.ReserveQuotaResponse, error) {
	c.reserveRequests = append(c.reserveRequests, req)
	return &billingv1.ReserveQuotaResponse{Success: true, ReservationId: req.RequestId}, nil
}

func TestLegacyBillingResponsePreservesDetachedLogAndWSAttempts(t *testing.T) {
	billing, logs := &legacyAttemptBillingClient{}, &rawLogClient{}
	s := &HTTPServer{billingClient: billing, logClient: logs}
	ctx := requesttrace.WithAttempt(context.Background(), requesttrace.Attempt{RootRequestID: "root", Number: 1, SourceKind: "channel", UpstreamModelID: "Mapped-A"})
	reservation, err := s.reserveQuota(ctx, "1", "first", 10, "public", "7", 0)
	require.NoError(t, err)
	in := usageLogInput{RequestID: "first"}
	in.applyReservation(reservation)
	s.ingestUsageLogAfterResponse(in)
	require.Equal(t, "root", logs.entries[0].RootRequestId)
	require.Equal(t, "Mapped-A", logs.entries[0].UpstreamModelId)
	plan := &relaybiz.RelayPlan{Auth: &relaybiz.AuthSnapshot{UserID: 1}, GlobalModel: "public"}
	for number := int32(2); number <= 3; number++ {
		reservation, err = s.replaceResponsesWSReservation(ctx, plan, "public", []byte(`{"model":"public"}`), "first", reservation, &relaybiz.Channel{ID: 8})
		require.NoError(t, err)
		require.Equal(t, "root", reservation.RootRequestId)
		require.Equal(t, number, reservation.AttemptNumber)
	}
}

func TestGRPCFailoverPersistsActualOutgoingModel(t *testing.T) {
	t.Setenv("PROVIDER_DISABLE_SSRF_CHECK", "true")
	var sent []string
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body struct {
			Model string `json:"model"`
		}
		if err := jsonx.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Error(err)
			return
		}
		sent = append(sent, body.Model)
		if len(sent) == 1 {
			http.Error(w, `{"error":"temporary"}`, http.StatusBadGateway)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"id":"ok","model":"Mapped-B","choices":[],"usage":{"prompt_tokens":4,"completion_tokens":2,"total_tokens":6}}`))
	}))
	defer upstream.Close()
	first := &relaybiz.Channel{ID: 11, Type: relayprovider.ChannelTypeOpenAI, BaseURL: upstream.URL, ModelMapping: `{"gpt-4o-mini":"Mapped-A"}`}
	second := &relaybiz.Channel{ID: 22, Type: relayprovider.ChannelTypeOpenAI, BaseURL: upstream.URL, ModelMapping: `{"gpt-4o-mini":"Mapped-B"}`}
	uc := relaybiz.NewRelayUsecase(orchestratorIdentityClient{}, orchestratorFailoverChannelClient{first: first, second: second}, nil, &relaybiz.RetryPolicy{MaxAttempts: 2, RetryableStatus: map[int]bool{502: true}})
	billing, logs := &rawBillingClient{}, &rawLogClient{}
	s := relayservice.NewRelayGrpcService(rawIdentityClient{}, rawChannelClient{}, logs, billing, relayprovider.NewProviderFactory(time.Second), uc)
	ctx := metadata.NewIncomingContext(context.Background(), metadata.Pairs("authorization", "Bearer test-token"))
	_, err := s.ChatCompletion(ctx, &relayv1.ChatCompletionRequest{Model: "gpt-4o-mini", Messages: []*relayv1.Message{{Role: "user", Content: "hello"}}})
	require.NoError(t, err)
	require.Equal(t, []string{"Mapped-A", "Mapped-B"}, sent)
	require.Len(t, billing.reserveRequests, 2)
	a, b := billing.reserveRequests[0], billing.reserveRequests[1]
	require.NotEmpty(t, a.RootRequestId)
	require.Equal(t, a.RootRequestId, b.RootRequestId)
	require.NotEqual(t, a.RequestId, b.RequestId)
	require.EqualValues(t, 1, a.AttemptNumber)
	require.EqualValues(t, 2, b.AttemptNumber)
	require.Equal(t, sent[0], a.UpstreamModelId)
	require.Equal(t, sent[1], b.UpstreamModelId)
	require.Equal(t, 1, billing.releases)
	require.Len(t, billing.commitRequests, 1)
	require.Equal(t, "Mapped-B", billing.commitRequests[0].UpstreamModelId)
	require.Len(t, logs.entries, 1)
	require.Equal(t, b.RootRequestId, logs.entries[0].RootRequestId)
	require.Equal(t, b.RequestId, logs.entries[0].RequestId)
	require.EqualValues(t, 2, logs.entries[0].AttemptNumber)
	require.Equal(t, "Mapped-B", logs.entries[0].UpstreamModelId)
	require.Equal(t, billing.commitRequests[0].ReservationId, logs.entries[0].ReservationId)
}

func TestResponsesFailoverPersistsActualOutgoingModel(t *testing.T) {
	t.Setenv("PROVIDER_DISABLE_SSRF_CHECK", "true")
	for _, stream := range []bool{false, true} {
		t.Run(map[bool]string{false: "json", true: "stream"}[stream], func(t *testing.T) {
			var sent []string
			upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				var body struct {
					Model string `json:"model"`
				}
				if err := jsonx.NewDecoder(r.Body).Decode(&body); err != nil {
					t.Error(err)
					return
				}
				sent = append(sent, body.Model)
				if len(sent) == 1 {
					http.Error(w, `{"error":"temporary"}`, http.StatusBadGateway)
					return
				}
				if stream {
					w.Header().Set("Content-Type", "text/event-stream")
					_, _ = w.Write([]byte("event: response.completed\ndata: {\"type\":\"response.completed\",\"response\":{\"id\":\"resp-ok\",\"status\":\"completed\",\"usage\":{\"input_tokens\":4,\"output_tokens\":2,\"total_tokens\":6}}}\n\n"))
				} else {
					w.Header().Set("Content-Type", "application/json")
					_, _ = w.Write([]byte(`{"id":"resp-ok","status":"completed","output":[],"usage":{"input_tokens":4,"output_tokens":2,"total_tokens":6}}`))
				}
			}))
			defer upstream.Close()
			first := &relaybiz.Channel{ID: 11, Type: relayprovider.ChannelTypeOpenAI, BaseURL: upstream.URL, ModelMapping: `{"gpt-4o-mini":"Mapped-A"}`}
			second := &relaybiz.Channel{ID: 22, Type: relayprovider.ChannelTypeOpenAI, BaseURL: upstream.URL, ModelMapping: `{"gpt-4o-mini":"Mapped-B"}`}
			uc := relaybiz.NewRelayUsecase(orchestratorIdentityClient{}, orchestratorFailoverChannelClient{first: first, second: second}, nil, &relaybiz.RetryPolicy{MaxAttempts: 2, RetryableStatus: map[int]bool{502: true}})
			billing, logs := &rawBillingClient{}, &rawLogClient{}
			s := NewHTTPServer(nil, nil, billing, relayprovider.NewProviderFactory(time.Second), uc, logs)
			body := `{"model":"gpt-4o-mini","input":"hello","stream":false}`
			if stream {
				body = strings.Replace(body, "false", "true", 1)
			}
			r := httptest.NewRequest(http.MethodPost, "/v1/responses", strings.NewReader(body))
			r.Header.Set("Authorization", "Bearer test-token")
			w := httptest.NewRecorder()
			s.handleResponsesCreateLike(w, r, "/v1/responses")
			require.Equal(t, 200, w.Code, w.Body.String())
			require.Equal(t, []string{"Mapped-A", "Mapped-B"}, sent)
			require.Len(t, billing.reserveRequests, 2)
			a, b := billing.reserveRequests[0], billing.reserveRequests[1]
			require.NotEmpty(t, a.RootRequestId)
			require.Equal(t, a.RootRequestId, b.RootRequestId)
			require.NotEqual(t, a.RequestId, b.RequestId)
			require.EqualValues(t, 1, a.AttemptNumber)
			require.EqualValues(t, 2, b.AttemptNumber)
			require.Equal(t, sent[0], a.UpstreamModelId)
			require.Equal(t, sent[1], b.UpstreamModelId)
			require.Equal(t, 1, billing.releases)
			require.Len(t, billing.commitRequests, 1)
			require.Equal(t, "Mapped-B", billing.commitRequests[0].UpstreamModelId)
			require.Len(t, logs.entries, 1)
			require.Equal(t, b.RootRequestId, logs.entries[0].RootRequestId)
			require.Equal(t, b.RequestId, logs.entries[0].RequestId)
			require.EqualValues(t, 2, logs.entries[0].AttemptNumber)
			require.Equal(t, "Mapped-B", logs.entries[0].UpstreamModelId)
			require.Equal(t, billing.commitRequests[0].ReservationId, logs.entries[0].ReservationId)
		})
	}
}

func TestWebsocketFailoverPreservesRootAndChangesAttempt(t *testing.T) {
	billing := &rawBillingClient{}
	s := &HTTPServer{billingClient: billing}
	plan := &relaybiz.RelayPlan{Auth: &relaybiz.AuthSnapshot{UserID: 1}, GlobalModel: "public"}
	channel := &relaybiz.Channel{ID: 8, ModelMapping: `{"public":"Actual-B"}`}
	previous := &billingv1.ReserveQuotaResponse{ReservationId: "first", RootRequestId: "root", AttemptNumber: 1, RequestId: "attempt-1"}
	result, err := s.replaceResponsesWSReservation(context.Background(), plan, "public", []byte(`{"model":"public"}`), "attempt-1", previous, channel)
	require.NoError(t, err)
	require.Equal(t, 1, billing.releases)
	require.Equal(t, "root", result.RootRequestId)
	require.EqualValues(t, 2, result.AttemptNumber)
	require.NotEqual(t, "attempt-1", result.RequestId)
	require.Equal(t, "Actual-B", result.UpstreamModelId)
}

func TestPassthroughModelInLegacyAndOrchestrator(t *testing.T) {
	t.Setenv("PROVIDER_DISABLE_SSRF_CHECK", "true")
	for _, orchestrator := range []bool{false, true} {
		out := runChatCompletionPath(t, orchestrator, 200, `{"id":"ok","choices":[],"usage":{"prompt_tokens":4,"completion_tokens":2,"total_tokens":6}}`)
		require.Equal(t, 200, out.status)
		require.NotNil(t, out.log)
		require.NotEmpty(t, out.log.RootRequestId)
		require.EqualValues(t, 1, out.log.AttemptNumber)
		require.Equal(t, out.commit.ReservationId, out.log.ReservationId)
		require.Equal(t, "gpt-4o-mini", out.commit.UpstreamModelId)
		require.Equal(t, out.commit.UpstreamModelId, out.log.UpstreamModelId)
	}
}

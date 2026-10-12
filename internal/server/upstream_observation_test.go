package server

import (
	"context"
	"errors"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	coderws "github.com/coder/websocket"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc"
	channelv1 "micro-one-api/api/channel/v1"
	"micro-one-api/domain/routing"
	"micro-one-api/domain/upstream/provider"
	relayadaptor "micro-one-api/internal/adaptor"
	relaybiz "micro-one-api/internal/biz"
)

type observedMatrixChannelClient struct {
	matrixChannelClient
	observations []provider.Observation
}

func TestSubscriptionAttemptKeepsClaimedProbeWithoutMutatingPlan(t *testing.T) {
	account := &relaybiz.SubscriptionAccount{ID: 7, UpstreamConfigurationRevision: "v2", HealthProbeID: "old-probe"}
	base := &relaybiz.RelayPlan{Account: account, GlobalModel: "gpt-5"}
	claimed := &relaybiz.Channel{SubscriptionAccountID: 7, UpstreamConfigurationRevision: "v2", HealthProbeID: "claimed-probe"}
	plan, err := relayPlanForAttempt(context.Background(), nil, base, claimed, "gpt-5")
	if err != nil || plan.Account == account || plan.Account.HealthProbeID != "claimed-probe" || account.HealthProbeID != "old-probe" {
		t.Fatalf("claimed attempt lost its fence or mutated the shared account: plan=%+v err=%v", plan, err)
	}
}

type pinnedSourceInformationClient struct{ rawChannelClient }

func (c pinnedSourceInformationClient) GetChannel(ctx context.Context, req *channelv1.GetChannelRequest, options ...grpc.CallOption) (*channelv1.GetChannelReply, error) {
	reply, err := c.rawChannelClient.GetChannel(ctx, req, options...)
	if reply != nil && reply.Channel != nil {
		reply.Channel.UpstreamConfigurationRevision = "current-v2"
	}
	return reply, err
}

type pinnedExecutionClient struct {
	observedMatrixChannelClient
	denied bool
	claims []provider.ExecutionClaim
}

func (c *pinnedExecutionClient) ClaimUpstreamExecution(_ context.Context, claim provider.ExecutionClaim) (provider.ExecutionLease, error) {
	c.claims = append(c.claims, claim)
	if c.denied {
		return provider.ExecutionLease{}, errors.New("upstream circuit open")
	}
	return provider.ExecutionLease{ConfigurationRevision: claim.ConfigurationRevision, ProbeID: "pinned-probe"}, nil
}

func (*pinnedExecutionClient) CanRoute(_ context.Context, _, _ string, _ routing.Source) (routing.Permission, error) {
	return routing.Permission{Allowed: true, UpstreamModelID: "gpt-5"}, nil
}

func TestPinnedResponsesClaimsFreshConfigurationAndReportsOnlyRealOutcome(t *testing.T) {
	t.Setenv("PROVIDER_DISABLE_SSRF_CHECK", "true")
	for _, tc := range []struct {
		name   string
		stream bool
		denied bool
		status int
		body   string
		want   provider.HealthOutcome
	}{
		{"denied", false, true, 200, `{}`, provider.HealthNeutral},
		{"success", false, false, 200, `{"id":"resp-success","status":"completed","output":[]}`, provider.HealthSuccess},
		{"upstream failure", false, false, 503, `{"error":"unavailable"}`, provider.HealthFailure},
		{"completed stream", true, false, 200, "event: response.completed\ndata: {\"type\":\"response.completed\",\"response\":{\"id\":\"resp-complete\",\"status\":\"completed\"}}\n\n", provider.HealthSuccess},
		{"truncated stream", true, false, 200, "event: response.output_text.delta\ndata: {\"type\":\"response.output_text.delta\",\"delta\":\"partial\"}\n\n", provider.HealthNeutral},
		{"metadata lookup", false, false, 200, `{"id":"resp-existing","status":"completed"}`, provider.HealthNeutral},
	} {
		t.Run(tc.name, func(t *testing.T) {
			calls := 0
			upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				calls++
				require.Equal(t, "Bearer fresh-key", r.Header.Get("Authorization"), "configuration revision must correspond to the forwarded credential")
				if tc.stream {
					w.Header().Set("Content-Type", "text/event-stream")
				} else {
					w.Header().Set("Content-Type", "application/json")
				}
				w.WriteHeader(tc.status)
				_, _ = io.WriteString(w, tc.body)
			}))
			defer upstream.Close()
			control := &pinnedExecutionClient{denied: tc.denied}
			information := pinnedSourceInformationClient{rawChannelClient{baseURL: upstream.URL + "/v1", key: "fresh-key", getModels: "gpt-5"}}
			billing := &rawBillingClient{}
			server := NewHTTPServer(rawIdentityClient{}, information, billing, provider.NewProviderFactory(time.Second), relaybiz.NewRelayUsecase(nil, control, nil, nil))
			body := []byte(`{"model":"gpt-5","input":"hello"}`)
			req := httptest.NewRequest(http.MethodPost, "/v1/responses", strings.NewReader(string(body)))
			upstreamPath := "/responses"
			if tc.name == "metadata lookup" {
				req.Method = http.MethodGet
				upstreamPath = "/responses/resp-existing"
			}
			server.forwardResponsesToStoredRoute(httptest.NewRecorder(), req, upstreamPath, body, "user-token", responseRoute{Channel: relaybiz.Channel{ID: 11, Key: "old-key", UpstreamConfigurationRevision: "old-v1", HealthProbeID: "old-probe"}, Model: "gpt-5", UserID: 42}, tc.stream)
			require.Len(t, control.claims, 1)
			require.Equal(t, "current-v2", control.claims[0].ConfigurationRevision)
			require.Empty(t, control.claims[0].ProbeID, "stored probe belongs to a previous request")
			if tc.denied {
				require.Zero(t, calls)
				require.Empty(t, billing.reserveRequests)
			}
			if tc.want == provider.HealthNeutral {
				for _, observation := range control.observations {
					require.NotEqual(t, provider.HealthSuccess, observation.Outcome)
				}
			} else {
				require.Len(t, control.observations, 1)
				require.Equal(t, tc.want, control.observations[0].Outcome)
				require.Equal(t, "pinned-probe", control.observations[0].ProbeID)
			}
		})
	}
}

func (c *observedMatrixChannelClient) RecordUpstreamObservation(_ context.Context, observation provider.Observation) error {
	c.observations = append(c.observations, observation)
	return nil
}

func TestQuotaDependencyFailureHasNoUpstreamObservation(t *testing.T) {
	for _, cause := range []error{context.DeadlineExceeded, &net.OpError{Op: "dial", Net: "tcp", Err: errors.New("billing unavailable")}} {
		channel := &observedMatrixChannelClient{matrixChannelClient: matrixChannelClient{channel: &relaybiz.Channel{ID: 11, UpstreamConfigurationRevision: "v2"}}}
		hooks := &matrixLifecycleHooks{reserveErr: cause}
		forwarder := &matrixForwarder{}
		uc := matrixUsecase(channel, &relaybiz.RetryPolicy{MaxAttempts: 3})
		_, err := newMatrixExecutor(uc, hooks, forwarder).Execute(context.Background(), matrixRequest())
		require.ErrorIs(t, err, cause)
		require.Equal(t, 1, hooks.reserve)
		require.Zero(t, forwarder.calls)
		require.Empty(t, channel.observations)
		require.Zero(t, channel.healthCalls)
	}
}

type observedStreamForwarder struct{ body string }

func (observedStreamForwarder) Forward(context.Context, *relaybiz.RelayPlan, relaybiz.ExecutorRequest) (*relaybiz.ForwardResponse, error) {
	return nil, errors.New("unexpected nonstream call")
}

func (f observedStreamForwarder) ForwardStream(context.Context, *relaybiz.RelayPlan, relaybiz.ExecutorRequest) (*relaybiz.StreamForwardResponse, error) {
	return &relaybiz.StreamForwardResponse{StatusCode: http.StatusOK, Stream: io.NopCloser(strings.NewReader(f.body))}, nil
}

func TestOrchestratedStreamHealthWaitsForTerminal(t *testing.T) {
	for _, tc := range []struct {
		name     string
		body     string
		complete bool
	}{
		{"completed", "data: {\"choices\":[{\"finish_reason\":\"stop\"}]}\n\ndata: [DONE]\n\n", true},
		{"truncated", "data: {\"choices\":[{\"delta\":{\"content\":\"partial\"}}]}\n\n", false},
		{"terminal word in output", "data: {\"choices\":[{\"delta\":{\"content\":\"[DONE]\"}}]}\n\n", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			channel := &observedMatrixChannelClient{matrixChannelClient: matrixChannelClient{channel: &relaybiz.Channel{ID: 11, UpstreamConfigurationRevision: "v2", HealthProbeID: "half-open-probe"}}}
			hooks := &matrixLifecycleHooks{}
			executor := NewRelayExecutorWithForwarder(matrixUsecase(channel, nil), provider.NewProviderFactory(time.Second), hooks, observedStreamForwarder{body: tc.body}, nil)
			req := matrixRequest()
			req.Stream = true
			result, err := executor.Execute(context.Background(), req)
			require.NoError(t, err)
			require.Empty(t, channel.observations, "2xx headers cannot close a half-open circuit")
			_, err = io.ReadAll(result.Stream)
			require.NoError(t, err)
			require.NoError(t, result.Stream.Close())
			require.NoError(t, result.Stream.Close(), "completion must be idempotent")
			if tc.complete {
				require.Len(t, channel.observations, 1)
				require.Equal(t, provider.HealthSuccess, channel.observations[0].Outcome)
				require.Equal(t, "half-open-probe", channel.observations[0].ProbeID)
				require.Equal(t, 1, hooks.commit)
			} else {
				for _, observation := range channel.observations {
					require.NotEqual(t, provider.HealthSuccess, observation.Outcome, "partial stream cannot authorize recovery")
				}
				require.Equal(t, 1, hooks.release)
				require.Zero(t, hooks.commit)
			}
		})
	}
}

type observedAdaptorChannelClient struct {
	adaptorFailoverChannelClient
	observations []provider.Observation
}

func (c *observedAdaptorChannelClient) RecordUpstreamObservation(_ context.Context, observation provider.Observation) error {
	c.observations = append(c.observations, observation)
	return nil
}

func TestSubscriptionObservationsKeepCapacityConfigurationAndModelSeparate(t *testing.T) {
	for _, tc := range []struct {
		name   string
		status int
		body   string
		kind   string
	}{
		{"capacity", 429, `{"error":{"message":"rate limited"}}`, "capacity"},
		{"configuration", 401, `{"error":{"message":"invalid credential"}}`, "configuration"},
		{"model", 500, `{"message":"no healthy model"}`, "model"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			client := &observedAdaptorChannelClient{}
			server := NewHTTPServer(nil, nil, nil, nil, relaybiz.NewRelayUsecase(adaptorFailoverIdentity{}, client, nil, nil))
			server.SetOAuthHTTPClient(&http.Client{Transport: roundTripFunc(func(*http.Request) (*http.Response, error) {
				return newStatusResponse(tc.status, tc.body), nil
			})})
			plan := stickyCodexPlan(42, 0)
			plan.Account.UpstreamConfigurationRevision = "v2"
			server.executeAndMeter(context.Background(), plan, "gpt-5", make(http.Header), []byte(`{"model":"gpt-5","input":"hello"}`), relayadaptor.FormatOpenAIResponses, "")
			require.Len(t, client.observations, 1)
			require.Equal(t, tc.kind, client.observations[0].FailureKind)
			require.Equal(t, provider.HealthNeutral, client.observations[0].Outcome)
			require.Equal(t, relaybiz.UpstreamSourceSubscription, client.observations[0].SourceKind)
			require.Empty(t, client.health, "capacity/configuration/model failures must not also feed availability")
		})
	}
}

func TestSubscriptionStreamHealthWaitsForTerminal(t *testing.T) {
	for _, complete := range []bool{true, false} {
		t.Run(map[bool]string{true: "completed", false: "truncated"}[complete], func(t *testing.T) {
			client := &observedAdaptorChannelClient{}
			server := NewHTTPServer(nil, nil, nil, nil, relaybiz.NewRelayUsecase(adaptorFailoverIdentity{}, client, nil, nil))
			streamBody := "event: response.output_text.delta\ndata: {\"type\":\"response.output_text.delta\",\"delta\":\"partial\"}\n\n"
			if complete {
				streamBody += "event: response.completed\ndata: {\"type\":\"response.completed\",\"response\":{\"id\":\"resp-complete\",\"status\":\"completed\"}}\n\n"
			}
			server.SetOAuthHTTPClient(&http.Client{Transport: roundTripFunc(func(*http.Request) (*http.Response, error) {
				return &http.Response{StatusCode: http.StatusOK, Header: http.Header{"Content-Type": {"text/event-stream"}}, Body: io.NopCloser(strings.NewReader(streamBody))}, nil
			})})
			plan := stickyCodexPlan(42, 0)
			plan.Account.UpstreamConfigurationRevision = "v2"
			plan.Account.HealthProbeID = "probe-42"
			result := server.executeAndMeter(context.Background(), plan, "gpt-5", make(http.Header), []byte(`{"model":"gpt-5","input":"hello","stream":true}`), relayadaptor.FormatOpenAIResponses, "")
			require.Empty(t, client.observations, "stream headers cannot record success")
			require.Empty(t, client.health)
			require.Empty(t, client.modelHealth)
			result.write(httptest.NewRecorder())
			if complete {
				require.Len(t, client.observations, 1)
				require.Equal(t, provider.HealthSuccess, client.observations[0].Outcome)
				require.Equal(t, relaybiz.UpstreamSourceSubscription, client.observations[0].SourceKind)
				require.EqualValues(t, 42, client.observations[0].SourceID)
				require.Equal(t, "probe-42", client.observations[0].ProbeID)
			} else {
				for _, observation := range client.observations {
					require.NotEqual(t, provider.HealthSuccess, observation.Outcome)
				}
			}
		})
	}
}

func TestWebSocketConfigurationAndCapacityDoNotPoisonAvailability(t *testing.T) {
	for _, tc := range []struct {
		name   string
		status int
	}{
		{"local configuration", 0},
		{"upstream rate limit", http.StatusTooManyRequests},
		{"upstream credential", http.StatusUnauthorized},
	} {
		t.Run(tc.name, func(t *testing.T) {
			upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { http.Error(w, "rejected", tc.status) }))
			defer upstream.Close()
			upstreamURL := upstream.URL
			if tc.status == 0 {
				upstreamURL = "ftp://invalid.example"
			}
			_, channels, billing, conn, done := newWSAdmissionGateway(t, upstreamURL)
			writeWSAdmissionTurn(t, conn)
			ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
			defer cancel()
			_, _, err := conn.Read(ctx)
			require.Error(t, err)
			awaitWSAdmissionDone(t, done)
			require.Equal(t, 1, billing.releases)
			channels.mu.Lock()
			defer channels.mu.Unlock()
			require.Empty(t, channels.unhealthy)
			require.Zero(t, channels.channelHealthFailures)
		})
	}
}

func TestWebSocketTerminalObservationsKeepFailureKindsSeparate(t *testing.T) {
	for _, tc := range []struct {
		code    string
		kind    string
		outcome provider.HealthOutcome
	}{
		{"rate_limit_exceeded", "capacity", provider.HealthNeutral},
		{"overloaded", "capacity", provider.HealthNeutral},
		{"invalid_api_key", "configuration", provider.HealthNeutral},
		{"model_not_found", "model", provider.HealthNeutral},
		{"server_error", "", provider.HealthFailure},
	} {
		t.Run(tc.code, func(t *testing.T) {
			client := &observedMatrixChannelClient{}
			uc := relaybiz.NewRelayUsecase(nil, client, nil, nil)
			err := openAIWSTerminalHealthError(openAIWSTurnResult{terminalEventType: "response.failed", errorCode: tc.code})
			uc.RecordUpstreamResult(context.Background(), &relaybiz.Channel{ID: 7, UpstreamConfigurationRevision: "v2", HealthProbeID: "probe-7"}, err, 10, time.Now())
			require.Len(t, client.observations, 1)
			require.Equal(t, tc.kind, client.observations[0].FailureKind)
			require.Equal(t, tc.outcome, client.observations[0].Outcome)
		})
	}
}

func TestWebSocketLaterRecoveryProbeRequiresNewConnection(t *testing.T) {
	client := &pinnedExecutionClient{}
	billing := &rawBillingClient{}
	server := NewHTTPServer(rawIdentityClient{}, nil, billing, nil, relaybiz.NewRelayUsecase(nil, client, nil, nil))
	channel := &relaybiz.Channel{ID: 7, UpstreamConfigurationRevision: "v2", HealthProbeID: "connection-probe"}
	plan := &relaybiz.RelayPlan{Auth: &relaybiz.AuthSnapshot{UserID: 42, TokenID: 7, Group: "default"}, Channel: channel}
	turns := server.newRoutingWSTurns("user-token", "gpt-5", "gpt-5", plan, channel, nil)
	frame := []byte(`{"type":"response.create","model":"gpt-5","input":"hello"}`)
	_, err := turns.beforeWrite(context.Background(), coderws.MessageText, frame)
	require.NoError(t, err)
	turns.takeExecution()
	_, err = turns.beforeWrite(context.Background(), coderws.MessageText, frame)
	require.ErrorIs(t, err, errWSRoutingAdmission)
	require.Contains(t, err.Error(), "fresh websocket connection")
	require.Empty(t, billing.reserveRequests, "rejected recovery turn must not reserve quota")
	require.Len(t, client.observations, 1, "unused recovery lease must be retired neutrally")
	require.Equal(t, provider.HealthNeutral, client.observations[0].Outcome)
	require.Equal(t, "pinned-probe", client.observations[0].ProbeID)
	require.Equal(t, "connection-probe", channel.HealthProbeID, "turn-local lease must not mutate connection projection")
}

func TestOneAPIProxyClaimsConfigurationAndDoesNotRecoverFromDiscovery(t *testing.T) {
	t.Setenv("PROVIDER_DISABLE_SSRF_CHECK", "true")
	for _, tc := range []struct {
		name string
		path string
		body string
		want provider.HealthOutcome
	}{
		{"inference", "chat/completions", `{"model":"gpt-5","messages":[]}`, provider.HealthSuccess},
		{"model discovery", "models", `{}`, provider.HealthNeutral},
	} {
		t.Run(tc.name, func(t *testing.T) {
			upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				require.Equal(t, "Bearer fresh-key", r.Header.Get("Authorization"))
				w.Header().Set("Content-Type", "application/json")
				_, _ = io.WriteString(w, `{"id":"response","choices":[],"data":[]}`)
			}))
			defer upstream.Close()
			control := &pinnedExecutionClient{}
			information := pinnedSourceInformationClient{rawChannelClient{baseURL: upstream.URL + "/v1", key: "fresh-key", getModels: "gpt-5"}}
			server := NewHTTPServer(rawIdentityClient{}, information, &rawBillingClient{}, provider.NewProviderFactory(time.Second), relaybiz.NewRelayUsecase(nil, control, nil, nil))
			req := httptest.NewRequest(http.MethodPost, "/v1/oneapi/proxy/11/"+tc.path, strings.NewReader(tc.body))
			req.Header.Set("Authorization", "Bearer user-token")
			recorder := httptest.NewRecorder()
			server.handleOneAPIProxy(recorder, req)
			require.Equal(t, http.StatusOK, recorder.Code)
			require.Len(t, control.claims, 1)
			require.Equal(t, "current-v2", control.claims[0].ConfigurationRevision)
			require.Len(t, control.observations, 1)
			require.Equal(t, tc.want, control.observations[0].Outcome)
		})
	}
}

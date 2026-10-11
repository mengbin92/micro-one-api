package server

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	khttp "github.com/go-kratos/kratos/v3/transport/http"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc"
	channelv1 "micro-one-api/api/channel/v1"
	relayprovider "micro-one-api/domain/upstream/provider"
	relaybiz "micro-one-api/internal/biz"
	relaydata "micro-one-api/internal/data"
)

type rejectingRawSlotClient struct {
	rawModelHealthClient
	slotRequests   []*channelv1.RecordChannelSlotRequest
	healthFailures int
}

func (c *rejectingRawSlotClient) RecordChannelSlot(_ context.Context, req *channelv1.RecordChannelSlotRequest, _ ...grpc.CallOption) (*channelv1.RecordChannelSlotResponse, error) {
	c.slotRequests = append(c.slotRequests, req)
	if req.Acquired {
		return nil, errors.New("channel at slot limit")
	}
	return &channelv1.RecordChannelSlotResponse{Success: true}, nil
}

func (c *rejectingRawSlotClient) RecordChannelHealth(_ context.Context, req *channelv1.RecordChannelHealthRequest, _ ...grpc.CallOption) (*channelv1.RecordChannelHealthResponse, error) {
	if !req.Success {
		c.healthFailures++
	}
	return &channelv1.RecordChannelHealthResponse{Success: true}, nil
}

func TestHTTPChannelSlotRejectionReleasesQuotaBeforeForward(t *testing.T) {
	for _, tc := range []struct {
		name, method, path, body string
		stored                   bool
	}{
		{name: "chat", method: http.MethodPost, path: "/v1/chat/completions", body: `{"model":"gpt-4o-mini","messages":[{"role":"user","content":"hello"}]}`},
		{name: "chat stream", method: http.MethodPost, path: "/v1/chat/completions", body: `{"model":"gpt-4o-mini","messages":[{"role":"user","content":"hello"}],"stream":true}`},
		{name: "raw", method: http.MethodPost, path: "/v1/moderations", body: `{"model":"gpt-4o-mini","input":"hello"}`},
		{name: "responses", method: http.MethodPost, path: "/v1/responses", body: `{"model":"gpt-4o-mini","input":"hello"}`},
		{name: "responses stream", method: http.MethodPost, path: "/v1/responses", body: `{"model":"gpt-4o-mini","input":"hello","stream":true}`},
		{name: "stored responses", method: http.MethodGet, path: "/v1/responses/resp-stored", stored: true},
		{name: "oneapi", method: http.MethodPost, path: "/v1/oneapi/proxy/11/chat/completions", body: `{"model":"gpt-4o-mini","messages":[]}`},
		{name: "anthropic", method: http.MethodPost, path: "/v1/messages", body: `{"model":"gpt-4o-mini","max_tokens":1,"messages":[{"role":"user","content":"hello"}]}`},
		{name: "anthropic stream", method: http.MethodPost, path: "/v1/messages", body: `{"model":"gpt-4o-mini","max_tokens":1,"messages":[{"role":"user","content":"hello"}],"stream":true}`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var forwarded atomic.Bool
			upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { forwarded.Store(true) }))
			defer upstream.Close()
			identity := rawIdentityClient{}
			channels := &rejectingRawSlotClient{rawModelHealthClient: rawModelHealthClient{rawChannelClient: rawChannelClient{baseURL: upstream.URL, getModels: "gpt-4o-mini"}}}
			billing := &rawBillingClient{}
			uc := relaybiz.NewRelayUsecase(relaydata.NewIdentityAdapter(identity), relaydata.NewChannelAdapter(channels), nil, &relaybiz.RetryPolicy{MaxAttempts: 1})
			s := NewHTTPServer(identity, channels, billing, relayprovider.NewProviderFactory(time.Second), uc)
			r := httptest.NewRequest(tc.method, tc.path, strings.NewReader(tc.body))
			r.Header.Set("Authorization", "Bearer user-token")
			r.Header.Set("Content-Type", "application/json")
			w := httptest.NewRecorder()
			if tc.stored {
				s.forwardResponsesToStoredRoute(w, r, "/responses/resp-stored", nil, "user-token", responseRoute{Model: "gpt-4o-mini", Channel: relaybiz.Channel{ID: 11, Type: relayprovider.ChannelTypeOpenAI, BaseURL: upstream.URL}, UserID: 42}, false)
			} else {
				routes := khttp.NewServer()
				s.RegisterRoutes(routes)
				routes.ServeHTTP(w, r)
			}
			require.Equal(t, http.StatusServiceUnavailable, w.Code, w.Body.String())
			require.False(t, forwarded.Load(), "slot rejection must stop before upstream execution")
			require.Len(t, billing.reserveRequests, 1)
			require.Equal(t, 1, billing.releases)
			require.Zero(t, billing.commits)
			require.Zero(t, channels.healthFailures, "local rejection must not poison channel health")
		})
	}
}

type rejectingMatrixSlotClient struct{ matrixChannelClient }

func (*rejectingMatrixSlotClient) RecordChannelSlot(_ context.Context, _ int64, _ string, acquired bool) error {
	if acquired {
		return errors.New("channel at slot limit")
	}
	return nil
}

func TestOrchestratorChannelSlotRejectionReleasesQuotaBeforeForward(t *testing.T) {
	for _, stream := range []bool{false, true} {
		t.Run(map[bool]string{false: "nonstream", true: "stream"}[stream], func(t *testing.T) {
			channels := &rejectingMatrixSlotClient{matrixChannelClient: matrixChannelClient{channel: &relaybiz.Channel{ID: 11, Type: relayprovider.ChannelTypeOpenAI}}}
			hooks, forward := &matrixLifecycleHooks{}, &matrixForwarder{}
			o := newRelayOrchestrator(matrixUsecase(channels, nil), relayprovider.NewProviderFactory(time.Second), hooks, nil)
			o.forwardPort = forward
			streamForward := &orchestratorFailoverStreamForwarder{}
			o.streamPort = streamForward
			result, err := o.Execute(context.Background(), &RelayRequest{Token: "token", Model: "gpt-4o-mini", Endpoint: EndpointChatCompletions, IsStream: stream, Body: strings.NewReader(`{"model":"gpt-4o-mini"}`)})
			require.Error(t, err)
			require.Equal(t, http.StatusServiceUnavailable, result.StatusCode)
			require.Equal(t, 1, hooks.reserve)
			require.Equal(t, 1, hooks.release)
			require.Zero(t, hooks.commit)
			require.Zero(t, forward.calls)
			require.Empty(t, streamForward.calls)
			require.Empty(t, channels.healthOK, "local rejection must not report an upstream outcome")
		})
	}
}

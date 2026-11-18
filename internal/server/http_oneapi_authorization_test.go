package server

import (
	"context"
	"errors"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
	"google.golang.org/grpc"
	channelv1 "micro-one-api/api/channel/v1"
)

type proxyAuthorizationClient struct {
	rawChannelClient
	channelReads int
}

func (c *proxyAuthorizationClient) GetChannel(context.Context, *channelv1.GetChannelRequest, ...grpc.CallOption) (*channelv1.GetChannelReply, error) {
	c.channelReads++
	return nil, errors.New("credential access was outside the authorized boundary")
}

func TestOneAPIProxyRejectsBeforeLoadingChannel(t *testing.T) {
	for _, tc := range []struct {
		name, method, target, body string
		models                     []string
		denied                     bool
		routeError                 error
		status                     int
	}{
		{"model permission", "POST", "chat/completions", `{"model":"fixture-model"}`, []string{"other-model"}, false, nil, 403},
		{"source permission", "POST", "chat/completions", `{"model":"fixture-model"}`, nil, true, nil, 404},
		{"authority unavailable", "POST", "chat/completions", `{"model":"fixture-model"}`, nil, false, errors.New("authority offline"), 503},
		{"missing model", "POST", "chat/completions", `{}`, nil, false, nil, 400},
		{"non-inference path", "POST", "models", `{}`, nil, false, nil, 501},
		{"non-inference method", "GET", "chat/completions", `{}`, nil, false, nil, 405},
	} {
		t.Run(tc.name, func(t *testing.T) {
			channel := &proxyAuthorizationClient{rawChannelClient: rawChannelClient{denyRoute: tc.denied, routeError: tc.routeError}}
			billing := &rawBillingClient{}
			server := NewHTTPServer(rawIdentityClient{allowedModels: tc.models}, channel, billing, nil, nil)
			req := httptest.NewRequest(tc.method, "/v1/oneapi/proxy/11/"+tc.target, strings.NewReader(tc.body))
			req.Header.Set("Authorization", "Bearer test-token")
			req.Header.Set("Content-Type", "application/json")
			rec := httptest.NewRecorder()

			server.handleOneAPIProxy(rec, req)

			require.Zero(t, channel.channelReads)
			require.Empty(t, billing.reserveRequests)
			require.Equal(t, tc.status, rec.Code, rec.Body.String())
		})
	}
}

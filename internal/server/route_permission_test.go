package server

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	relaybiz "micro-one-api/internal/biz"
)

func TestStoredResponseRouteRechecksCurrentAuthority(t *testing.T) {
	for _, channel := range []rawChannelClient{{denyRoute: true}, {routeError: errors.New("offline")}} {
		srv := &HTTPServer{identityClient: rawIdentityClient{}, channelClient: channel, responseRoutes: map[string]responseRouteEntry{"resp_cached": {route: responseRoute{Model: "managed", Channel: relaybiz.Channel{ID: 11}, UserID: 42}, expiresAt: time.Now().Add(time.Hour)}}}
		_, ok := srv.lookupResponseRouteWithSticky(context.Background(), "token", "managed", "resp_cached")
		require.False(t, ok)
	}
}

func TestStoredResponseForwardStopsBeforeUpstreamOnRevocation(t *testing.T) {
	upstreamCalls := 0
	upstream := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { upstreamCalls++ }))
	defer upstream.Close()
	srv := &HTTPServer{identityClient: rawIdentityClient{}, channelClient: rawChannelClient{denyRoute: true, baseURL: upstream.URL}}
	writer := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodGet, "/v1/responses/resp_cached", nil)
	request.Header.Set("Authorization", "Bearer token")
	srv.forwardResponsesToStoredRoute(writer, request, "/v1/responses/resp_cached", nil, "token", responseRoute{Model: "managed", Channel: relaybiz.Channel{ID: 11, BaseURL: upstream.URL}, UserID: 42}, false)
	require.Equal(t, http.StatusNotFound, writer.Code)
	require.Zero(t, upstreamCalls)
}

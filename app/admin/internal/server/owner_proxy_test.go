package server

import (
	"context"
	"net/http/httptest"
	"net/url"
	"testing"

	"github.com/stretchr/testify/require"
	"micro-one-api/app/admin/internal/service"
)

func TestOwnerProxyUsesIndependentVerifiedCredentials(t *testing.T) {
	t.Setenv("SERVICE_IDENTITY_TOKEN", "admin-private")
	t.Setenv("SERVICE_TOKEN", "legacy-shared")
	target, err := url.Parse("http://127.0.0.1:8008")
	require.NoError(t, err)
	proxy := newOwnerHTTPProxy(target)
	for _, raw := range []string{"", "verified-session"} {
		request := httptest.NewRequest("GET", "/v1/notifications", nil)
		request.Header.Set("Authorization", "Bearer browser-supplied")
		request.Header.Set("x-operator-authorization", "Bearer spoofed-operator")
		if raw != "" {
			request = request.WithContext(service.WithOperatorCredential(context.Background(), raw))
		}
		proxy.Director(request)
		require.Equal(t, "Bearer admin-private", request.Header.Get("Authorization"))
		if raw == "" {
			require.Empty(t, request.Header.Get("x-operator-authorization"))
		} else {
			require.Equal(t, []string{"Bearer verified-session"}, request.Header.Values("x-operator-authorization"))
		}
	}
}

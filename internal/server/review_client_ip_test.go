package server

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/require"
	"google.golang.org/grpc"
	identityv1 "micro-one-api/api/identity/v1"
	xhttp "micro-one-api/platform/http"
)

type ipIdentityClient struct {
	rawIdentityClient
	clientIP string
}

func (c *ipIdentityClient) GetAuthSnapshot(ctx context.Context, r *identityv1.GetAuthSnapshotRequest, opts ...grpc.CallOption) (*identityv1.GetAuthSnapshotReply, error) {
	c.clientIP = r.ClientIp
	return c.rawIdentityClient.GetAuthSnapshot(ctx, r, opts...)
}

func TestRelayPassesTrustedClientIPToIdentity(t *testing.T) {
	for _, trusted := range []bool{false, true} {
		t.Run(map[bool]string{true: "trusted", false: "untrusted"}[trusted], func(t *testing.T) {
			t.Setenv("TRUSTED_PROXY_CIDRS", "")
			if trusted {
				t.Setenv("TRUSTED_PROXY_CIDRS", "10.0.0.0/8")
			}
			client := &ipIdentityClient{}
			s := &HTTPServer{identityClient: client}
			r := httptest.NewRequest(http.MethodPost, "/v1/messages", nil)
			r.RemoteAddr = "10.0.0.1:1234"
			r.Header.Set("X-Forwarded-For", "203.0.113.1")
			ctx := xhttp.WithClientIP(r.Context(), relayClientIP(r))
			_, err := s.getAuthSnapshot(ctx, "token")
			require.NoError(t, err)
			want := "10.0.0.1"
			if trusted {
				want = "203.0.113.1"
			}
			require.Equal(t, want, client.clientIP)
		})
	}
}

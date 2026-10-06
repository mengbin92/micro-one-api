package xgrpc

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"
	"micro-one-api/platform/security/serviceidentity"
)

func TestServiceCallerMethodAllowlist(t *testing.T) {
	v, err := serviceidentity.NewVerifier(map[string]string{"relay": "relay-private"}, "legacy")
	require.NoError(t, err)
	interceptor := ServiceIdentityUnaryInterceptor(v)
	for _, tt := range []struct {
		token, method string
		code          codes.Code
		dedicated     bool
	}{
		{"relay-private", "/api.billing.v1.BillingService/CommitQuota", codes.OK, true},
		{"relay-private", "/api.identity.v1.IdentityService/DeleteUser", codes.PermissionDenied, false},
		{"legacy", "/api.billing.v1.BillingService/CommitQuota", codes.OK, false},
		{"legacy", "/api.billing.v1.BillingService/Unknown", codes.PermissionDenied, false},
		{"fake", "/api.billing.v1.BillingService/CommitQuota", codes.Unauthenticated, false},
	} {
		t.Run(tt.token+tt.method, func(t *testing.T) {
			ctx := metadata.NewIncomingContext(context.Background(), metadata.Pairs("authorization", "Bearer "+tt.token, "x-service-name", "admin"))
			called := false
			_, err := interceptor(ctx, nil, &grpc.UnaryServerInfo{FullMethod: tt.method}, func(ctx context.Context, _ any) (any, error) {
				called = true
				require.Equal(t, tt.dedicated, serviceidentity.FromContext(ctx).Dedicated)
				return nil, nil
			})
			require.Equal(t, tt.code, status.Code(err))
			require.Equal(t, tt.code == codes.OK, called)
		})
	}
}

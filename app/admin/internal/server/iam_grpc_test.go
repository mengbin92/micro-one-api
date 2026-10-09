package server

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"
	"micro-one-api/app/admin/internal/service"
	"micro-one-api/domain/authorization"
)

func TestAdminGRPCAlwaysMarksExternalAndForwardsOneOperator(t *testing.T) {
	interceptor := IAMOperatorUnaryInterceptor()
	for _, input := range []struct{ key, reason string }{
		{"x-authorization-reason", "reviewed change"},
		{"x-authorization-reason-bin", "上游缺货"},
	} {
		for _, credential := range []string{"", "Bearer live-session"} {
			md := metadata.Pairs(input.key, input.reason)
			if credential != "" {
				md.Set("x-operator-authorization", credential)
			}
			_, err := interceptor(metadata.NewIncomingContext(context.Background(), md), nil, &grpc.UnaryServerInfo{FullMethod: "/api.admin.v1.AdminService/GetSystemOptions"}, func(ctx context.Context, _ any) (any, error) {
				require.True(t, authorization.External(ctx), "a service token cannot select the internal UC path")
				require.Equal(t, input.reason, authorization.WriteReason(ctx))
				expected := ""
				if credential != "" {
					expected = "live-session"
				}
				require.Equal(t, expected, authorization.Credential(ctx))
				require.Equal(t, expected, service.OperatorCredential(ctx))
				return nil, nil
			})
			require.NoError(t, err)
		}
	}
}

func TestAdminGRPCRejectsAmbiguousOperatorOrReason(t *testing.T) {
	for _, md := range []metadata.MD{
		metadata.Pairs("x-operator-authorization", "Bearer first", "x-operator-authorization", "Bearer second"),
		metadata.Pairs("x-authorization-reason", "first", "x-authorization-reason", "second"),
		metadata.Pairs("x-authorization-reason-bin", "first", "x-authorization-reason-bin", "second"),
		metadata.Pairs("x-authorization-reason", "first", "x-authorization-reason-bin", "second"),
	} {
		called := false
		_, err := IAMOperatorUnaryInterceptor()(metadata.NewIncomingContext(context.Background(), md), nil, &grpc.UnaryServerInfo{FullMethod: "/api.admin.v1.AdminService/UpdateSystemOptions"}, func(context.Context, any) (any, error) { called = true; return nil, nil })
		require.Error(t, err)
		require.Contains(t, []codes.Code{codes.Unauthenticated, codes.InvalidArgument}, status.Code(err))
		require.False(t, called)
	}
}

func TestAdminGRPCMissingAuthorityCannotUseLocalConfigurationPath(t *testing.T) {
	svc := service.NewAdminService(nil, nil, nil, nil)
	_, err := IAMOperatorUnaryInterceptor()(context.Background(), nil, &grpc.UnaryServerInfo{FullMethod: "/api.admin.v1.AdminService/GetSystemOptions"}, func(ctx context.Context, _ any) (any, error) {
		out, err := svc.ResourceAuthorizer().Query(ctx, "admin.system_options", "system.option.read", "")
		return out, err
	})
	require.Equal(t, codes.Unavailable, status.Code(err))
}

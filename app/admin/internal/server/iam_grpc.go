package server

import (
	"context"
	"slices"
	"strings"

	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"
	"micro-one-api/app/admin/internal/service"
	"micro-one-api/domain/authorization"
)

// Install after service-token verification on every real admin gRPC constructor.
// Only a transport-verified credential is forwarded; request actor hints are
// absent, and identity independently verifies the user JWT and IAM authority.
func IAMOperatorUnaryInterceptor() grpc.UnaryServerInterceptor {
	return func(ctx context.Context, req any, info *grpc.UnaryServerInfo, handler grpc.UnaryHandler) (any, error) {
		md, _ := metadata.FromIncomingContext(ctx)
		values := md.Get("x-operator-authorization")
		reasons := slices.Concat(md.Get("x-authorization-reason-bin"), md.Get("x-authorization-reason"))
		if len(reasons) > 1 {
			return nil, status.Error(codes.InvalidArgument, "ambiguous write reason")
		}
		ctx = authorization.WithCredential(authorization.WithExternal(ctx), "")
		if len(reasons) == 1 {
			ctx = authorization.WithWriteReason(ctx, reasons[0])
		}
		if strings.HasPrefix(info.FullMethod, "/api.admin.v1.IAMAdminService/") || len(values) > 0 {
			if len(values) != 1 || !strings.HasPrefix(values[0], "Bearer ") {
				return nil, status.Error(codes.Unauthenticated, "user operator credential required")
			}
			credential := strings.TrimPrefix(values[0], "Bearer ")
			if credential == "" {
				return nil, status.Error(codes.Unauthenticated, "user operator credential required")
			}
			ctx = service.WithOperatorCredential(ctx, credential)
		}
		return handler(ctx, req)
	}
}

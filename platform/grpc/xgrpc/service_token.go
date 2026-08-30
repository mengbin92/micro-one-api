package xgrpc

import (
	"context"
	"crypto/subtle"
	"strings"

	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"
)

// ValidateServiceToken requires authorization: Bearer <SERVICE_TOKEN> and
// fails closed when the server-side token is not configured.
func ValidateServiceToken(ctx context.Context, serviceToken string) error {
	if serviceToken == "" {
		return status.Error(codes.PermissionDenied, "service token not configured")
	}
	md, ok := metadata.FromIncomingContext(ctx)
	if !ok {
		return status.Error(codes.Unauthenticated, "missing metadata")
	}
	values := md.Get("authorization")
	if len(values) == 0 || !strings.HasPrefix(values[0], "Bearer ") {
		return status.Error(codes.Unauthenticated, "missing or invalid authorization header")
	}
	token := strings.TrimPrefix(values[0], "Bearer ")
	if subtle.ConstantTimeCompare([]byte(token), []byte(serviceToken)) != 1 {
		return status.Error(codes.Unauthenticated, "invalid service token")
	}
	return nil
}

// ServiceTokenUnaryInterceptor authenticates internal unary RPCs with the
// shared service token.
func ServiceTokenUnaryInterceptor(serviceToken string) grpc.UnaryServerInterceptor {
	return func(ctx context.Context, req interface{}, _ *grpc.UnaryServerInfo, handler grpc.UnaryHandler) (interface{}, error) {
		if err := ValidateServiceToken(ctx, serviceToken); err != nil {
			return nil, err
		}
		return handler(ctx, req)
	}
}

// ServiceTokenStreamInterceptor authenticates internal streaming RPCs with
// the shared service token.
func ServiceTokenStreamInterceptor(serviceToken string) grpc.StreamServerInterceptor {
	return func(srv interface{}, ss grpc.ServerStream, _ *grpc.StreamServerInfo, handler grpc.StreamHandler) error {
		if err := ValidateServiceToken(ss.Context(), serviceToken); err != nil {
			return err
		}
		return handler(srv, ss)
	}
}

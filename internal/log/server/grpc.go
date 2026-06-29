package server

import (
	"context"
	"crypto/subtle"
	apptimeout "micro-one-api/internal/pkg/timeout"
	"os"
	"strings"

	logv1 "micro-one-api/api/log/v1"
	"micro-one-api/internal/log/service"

	kgrpc "github.com/go-kratos/kratos/v2/transport/grpc"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"
)

// NewGRPCServer wires gRPC transport for log-service.
func NewGRPCServer(addr string, svc *service.LogService) *kgrpc.Server {
	opts := []kgrpc.ServerOption{
		kgrpc.Address(addr),
		kgrpc.Timeout(apptimeout.GetGRPCTimeout()),
	}
	if os.Getenv("LOG_GRPC_AUTH") == "true" {
		serviceToken := os.Getenv("SERVICE_TOKEN")
		opts = append(opts,
			kgrpc.UnaryInterceptor(serviceTokenUnaryInterceptor(serviceToken)),
			kgrpc.StreamInterceptor(serviceTokenStreamInterceptor(serviceToken)),
		)
	}
	srv := kgrpc.NewServer(opts...)
	logv1.RegisterLogServiceServer(srv, svc)
	return srv
}

func serviceTokenUnaryInterceptor(serviceToken string) grpc.UnaryServerInterceptor {
	return func(ctx context.Context, req interface{}, info *grpc.UnaryServerInfo, handler grpc.UnaryHandler) (interface{}, error) {
		if err := validateServiceToken(ctx, serviceToken); err != nil {
			return nil, err
		}
		return handler(ctx, req)
	}
}

func serviceTokenStreamInterceptor(serviceToken string) grpc.StreamServerInterceptor {
	return func(srv interface{}, ss grpc.ServerStream, info *grpc.StreamServerInfo, handler grpc.StreamHandler) error {
		if err := validateServiceToken(ss.Context(), serviceToken); err != nil {
			return err
		}
		return handler(srv, ss)
	}
}

func validateServiceToken(ctx context.Context, serviceToken string) error {
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

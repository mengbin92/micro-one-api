package server

import (
	"os"

	adminv1 "micro-one-api/api/admin/v1"
	"micro-one-api/app/admin/internal/service"
	"micro-one-api/platform/grpc/xgrpc"

	"google.golang.org/grpc"
)

// NewGRPCServer creates a gRPC server and registers the AdminService.
func NewGRPCServer(svc *service.AdminService) *grpc.Server {
	serviceToken := os.Getenv("SERVICE_TOKEN")
	srv := grpc.NewServer(
		grpc.UnaryInterceptor(xgrpc.ServiceTokenUnaryInterceptor(serviceToken)),
		grpc.StreamInterceptor(xgrpc.ServiceTokenStreamInterceptor(serviceToken)),
	)
	adminv1.RegisterAdminServiceServer(srv, svc)
	return srv
}

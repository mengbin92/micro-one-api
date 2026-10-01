package server

import (
	"micro-one-api/platform/authz"
	"os"

	configv1 "micro-one-api/api/config/v1"
	"micro-one-api/app/config/internal/service"
	apptimeout "micro-one-api/pkg/timeout"
	"micro-one-api/platform/grpc/xgrpc"

	kgrpc "github.com/go-kratos/kratos/v3/transport/grpc"
)

// NewGRPCServer wires gRPC transport for config-service.
func NewGRPCServer(addr string, svc *service.ConfigService) *kgrpc.Server {
	srv := kgrpc.NewServer(
		kgrpc.Address(addr),
		kgrpc.Timeout(apptimeout.GetGRPCTimeout()),
		kgrpc.UnaryInterceptor(xgrpc.ServiceTokenUnaryInterceptor(os.Getenv("SERVICE_TOKEN")), authz.OperatorUnaryInterceptor(), authz.CoverageUnaryInterceptor(svc.OwnerAuthorizationClient(), "system.options.read", configReadyMethods)),
		kgrpc.StreamInterceptor(xgrpc.ServiceTokenStreamInterceptor(os.Getenv("SERVICE_TOKEN"))),
	)
	configv1.RegisterConfigServiceServer(srv, svc)
	return srv
}

var configReadyMethods = []string{
	"/api.config.v1.ConfigService/GetConfig",
	"/api.config.v1.ConfigService/SetConfig",
	"/api.config.v1.ConfigService/ListConfigs",
	"/api.config.v1.ConfigService/DeleteConfig",
}

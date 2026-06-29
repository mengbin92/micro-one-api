package server

import (
	configv1 "micro-one-api/api/config/v1"
	"micro-one-api/internal/config/service"
	apptimeout "micro-one-api/internal/pkg/timeout"

	kgrpc "github.com/go-kratos/kratos/v2/transport/grpc"
)

// NewGRPCServer wires gRPC transport for config-service.
func NewGRPCServer(addr string, svc *service.ConfigService) *kgrpc.Server {
	srv := kgrpc.NewServer(
		kgrpc.Address(addr),
		kgrpc.Timeout(apptimeout.GetGRPCTimeout()),
	)
	configv1.RegisterConfigServiceServer(srv, svc)
	return srv
}

package server

import (
	configv1 "micro-one-api/api/config/v1"
	"micro-one-api/internal/config/service"

	kgrpc "github.com/go-kratos/kratos/v2/transport/grpc"
)

// NewGRPCServer wires gRPC transport for config-service.
func NewGRPCServer(addr string, svc *service.ConfigService) *kgrpc.Server {
	srv := kgrpc.NewServer(
		kgrpc.Address(addr),
	)
	configv1.RegisterConfigServiceServer(srv, svc)
	return srv
}

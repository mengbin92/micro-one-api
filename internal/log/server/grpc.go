package server

import (
	logv1 "micro-one-api/api/log/v1"
	"micro-one-api/internal/log/service"

	kgrpc "github.com/go-kratos/kratos/v2/transport/grpc"
)

// NewGRPCServer wires gRPC transport for log-service.
func NewGRPCServer(addr string, svc *service.LogService) *kgrpc.Server {
	srv := kgrpc.NewServer(
		kgrpc.Address(addr),
	)
	logv1.RegisterLogServiceServer(srv, svc)
	return srv
}

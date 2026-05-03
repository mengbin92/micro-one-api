package server

import (
	monitorv1 "micro-one-api/api/monitor/v1"
	"micro-one-api/internal/monitor/service"

	kgrpc "github.com/go-kratos/kratos/v2/transport/grpc"
)

// NewGRPCServer wires gRPC transport for monitor-worker.
func NewGRPCServer(addr string, svc *service.MonitorService) *kgrpc.Server {
	srv := kgrpc.NewServer(
		kgrpc.Address(addr),
	)
	monitorv1.RegisterMonitorServiceServer(srv, svc)
	return srv
}

package server

import (
	monitorv1 "micro-one-api/api/monitor/v1"
	"micro-one-api/app/monitor/internal/service"
	apptimeout "micro-one-api/pkg/timeout"

	kgrpc "github.com/go-kratos/kratos/v3/transport/grpc"
)

// NewGRPCServer wires gRPC transport for monitor-worker.
func NewGRPCServer(addr string, svc *service.MonitorService) *kgrpc.Server {
	srv := kgrpc.NewServer(
		kgrpc.Address(addr),
		kgrpc.Timeout(apptimeout.GetGRPCTimeout()),
	)
	monitorv1.RegisterMonitorServiceServer(srv, svc)
	return srv
}

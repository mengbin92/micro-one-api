package server

import (
	"micro-one-api/platform/authz"
	"os"

	monitorv1 "micro-one-api/api/monitor/v1"
	"micro-one-api/app/monitor/internal/service"
	apptimeout "micro-one-api/pkg/timeout"
	"micro-one-api/platform/grpc/xgrpc"

	kgrpc "github.com/go-kratos/kratos/v3/transport/grpc"
)

// NewGRPCServer wires gRPC transport for monitor-worker.
func NewGRPCServer(addr string, svc *service.MonitorService) *kgrpc.Server {
	srv := kgrpc.NewServer(
		kgrpc.Address(addr),
		kgrpc.Timeout(apptimeout.GetGRPCTimeout()),
		kgrpc.UnaryInterceptor(xgrpc.ServiceTokenUnaryInterceptor(os.Getenv("SERVICE_TOKEN")), authz.OperatorUnaryInterceptor(), authz.CoverageUnaryInterceptor(svc.OwnerAuthorizationClient(), "monitor.health.service", monitorReadyMethods)),
		kgrpc.StreamInterceptor(xgrpc.ServiceTokenStreamInterceptor(os.Getenv("SERVICE_TOKEN"))),
	)
	monitorv1.RegisterMonitorServiceServer(srv, svc)
	return srv
}

var monitorReadyMethods = []string{
	"/api.monitor.v1.MonitorService/ListHealthChecks",
	"/api.monitor.v1.MonitorService/GetLatestHealth",
	"/api.monitor.v1.MonitorService/CreateAlertRule",
	"/api.monitor.v1.MonitorService/ListAlertRules",
	"/api.monitor.v1.MonitorService/GetAlertRule",
	"/api.monitor.v1.MonitorService/UpdateAlertRule",
	"/api.monitor.v1.MonitorService/DeleteAlertRule",
}

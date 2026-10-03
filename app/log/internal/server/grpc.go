package server

import (
	"micro-one-api/platform/authz"
	"os"

	logv1 "micro-one-api/api/log/v1"
	"micro-one-api/app/log/internal/service"
	apptimeout "micro-one-api/pkg/timeout"
	"micro-one-api/platform/grpc/xgrpc"

	kgrpc "github.com/go-kratos/kratos/v3/transport/grpc"
)

// NewGRPCServer wires gRPC transport for log-service.
func NewGRPCServer(addr string, svc *service.LogService) *kgrpc.Server {
	serviceToken := os.Getenv("SERVICE_TOKEN")
	srv := kgrpc.NewServer(
		kgrpc.Address(addr),
		kgrpc.Timeout(apptimeout.GetGRPCTimeout()),
		kgrpc.UnaryInterceptor(xgrpc.ServiceTokenUnaryInterceptor(serviceToken), authz.OperatorUnaryInterceptor(), authz.CoverageUnaryInterceptor(svc.OwnerAuthorizationClient(), "log.requests.list", logReadyMethods)),
		kgrpc.StreamInterceptor(xgrpc.ServiceTokenStreamInterceptor(serviceToken)),
	)
	logv1.RegisterLogServiceServer(srv, svc)
	return srv
}

var logReadyMethods = []string{
	"/api.log.v1.LogService/ExportLogs",
	"/api.log.v1.LogService/PurgeLogs",
	"/api.log.v1.LogService/GetLog",
	"/api.log.v1.LogService/ListLogs",
}

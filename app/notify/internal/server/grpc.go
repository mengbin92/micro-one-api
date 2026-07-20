package server

import (
	notifyv1 "micro-one-api/api/notify/v1"
	"micro-one-api/app/notify/internal/service"
	apptimeout "micro-one-api/pkg/timeout"

	kgrpc "github.com/go-kratos/kratos/v3/transport/grpc"
)

// NewGRPCServer wires gRPC transport for notify-worker.
func NewGRPCServer(addr string, svc *service.NotifyService) *kgrpc.Server {
	srv := kgrpc.NewServer(
		kgrpc.Address(addr),
		kgrpc.Timeout(apptimeout.GetGRPCTimeout()),
	)
	notifyv1.RegisterNotifyServiceServer(srv, svc)
	return srv
}

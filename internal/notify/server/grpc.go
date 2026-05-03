package server

import (
	notifyv1 "micro-one-api/api/notify/v1"
	"micro-one-api/internal/notify/service"

	kgrpc "github.com/go-kratos/kratos/v2/transport/grpc"
)

// NewGRPCServer wires gRPC transport for notify-worker.
func NewGRPCServer(addr string, svc *service.NotifyService) *kgrpc.Server {
	srv := kgrpc.NewServer(
		kgrpc.Address(addr),
	)
	notifyv1.RegisterNotifyServiceServer(srv, svc)
	return srv
}

package server

import (
	channelv1 "micro-one-api/api/channel/v1"
	"micro-one-api/app/channel/internal/service"
	apptimeout "micro-one-api/pkg/timeout"

	kgrpc "github.com/go-kratos/kratos/v3/transport/grpc"
)

// NewGRPCServer wires gRPC transport for channel-service.
func NewGRPCServer(addr string, svc *service.ChannelService) *kgrpc.Server {
	srv := kgrpc.NewServer(
		kgrpc.Address(addr),
		kgrpc.Timeout(apptimeout.GetGRPCTimeout()),
	)
	channelv1.RegisterChannelServiceServer(srv, svc)
	return srv
}

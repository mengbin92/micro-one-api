package server

import (
	channelv1 "micro-one-api/api/channel/v1"
	"micro-one-api/internal/channel/service"

	kgrpc "github.com/go-kratos/kratos/v2/transport/grpc"
)

// NewGRPCServer wires gRPC transport for channel-service.
func NewGRPCServer(addr string, svc *service.ChannelService) *kgrpc.Server {
	srv := kgrpc.NewServer(
		kgrpc.Address(addr),
	)
	channelv1.RegisterChannelServiceServer(srv, svc)
	return srv
}

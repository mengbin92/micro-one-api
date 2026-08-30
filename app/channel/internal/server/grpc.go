package server

import (
	"os"

	channelv1 "micro-one-api/api/channel/v1"
	"micro-one-api/app/channel/internal/service"
	apptimeout "micro-one-api/pkg/timeout"
	"micro-one-api/platform/grpc/xgrpc"

	kgrpc "github.com/go-kratos/kratos/v3/transport/grpc"
)

// NewGRPCServer wires gRPC transport for channel-service.
func NewGRPCServer(addr string, svc *service.ChannelService) *kgrpc.Server {
	srv := kgrpc.NewServer(
		kgrpc.Address(addr),
		kgrpc.Timeout(apptimeout.GetGRPCTimeout()),
		kgrpc.UnaryInterceptor(xgrpc.ServiceTokenUnaryInterceptor(os.Getenv("SERVICE_TOKEN"))),
		kgrpc.StreamInterceptor(xgrpc.ServiceTokenStreamInterceptor(os.Getenv("SERVICE_TOKEN"))),
	)
	channelv1.RegisterChannelServiceServer(srv, svc)
	return srv
}

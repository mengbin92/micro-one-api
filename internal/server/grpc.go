package server

import (
	relayv1 "micro-one-api/api/relay/v1"
	apptimeout "micro-one-api/pkg/timeout"
	"micro-one-api/internal/service"

	kgrpc "github.com/go-kratos/kratos/v2/transport/grpc"
	"google.golang.org/grpc"
)

// NewGRPCServer wires gRPC transport for relay-gateway.
func NewGRPCServer(addr string, svc *service.RelayGrpcService, opts ...grpc.ServerOption) *kgrpc.Server {
	serverOpts := []kgrpc.ServerOption{
		kgrpc.Address(addr),
		kgrpc.Timeout(apptimeout.GetGRPCTimeout()),
	}
	if len(opts) > 0 {
		serverOpts = append(serverOpts, kgrpc.Options(opts...))
	}
	srv := kgrpc.NewServer(serverOpts...)
	relayv1.RegisterRelayServiceServer(srv, svc)
	return srv
}

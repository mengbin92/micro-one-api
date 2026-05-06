package server

import (
	relayv1 "micro-one-api/api/relay/v1"
	"micro-one-api/internal/relay/service"

	kgrpc "github.com/go-kratos/kratos/v2/transport/grpc"
)

// NewGRPCServer wires gRPC transport for relay-gateway.
func NewGRPCServer(addr string, svc *service.RelayGrpcService) *kgrpc.Server {
	srv := kgrpc.NewServer(
		kgrpc.Address(addr),
	)
	relayv1.RegisterRelayServiceServer(srv, svc)
	return srv
}

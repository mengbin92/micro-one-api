package server

import (
	relayv1 "micro-one-api/api/relay/v1"
	apptimeout "micro-one-api/internal/pkg/timeout"
	"micro-one-api/internal/relay/service"

	kgrpc "github.com/go-kratos/kratos/v2/transport/grpc"
)

// NewGRPCServer wires gRPC transport for relay-gateway.
func NewGRPCServer(addr string, svc *service.RelayGrpcService) *kgrpc.Server {
	srv := kgrpc.NewServer(
		kgrpc.Address(addr),
		kgrpc.Timeout(apptimeout.GetGRPCTimeout()),
	)
	relayv1.RegisterRelayServiceServer(srv, svc)
	return srv
}

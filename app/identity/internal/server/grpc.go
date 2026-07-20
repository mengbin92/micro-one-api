package server

import (
	identityv1 "micro-one-api/api/identity/v1"
	"micro-one-api/app/identity/internal/service"
	apptimeout "micro-one-api/pkg/timeout"

	kgrpc "github.com/go-kratos/kratos/v3/transport/grpc"
)

// NewGRPCServer wires gRPC transport for identity-service.
func NewGRPCServer(addr string, svc *service.IdentityService) *kgrpc.Server {
	srv := kgrpc.NewServer(
		kgrpc.Address(addr),
		kgrpc.Timeout(apptimeout.GetGRPCTimeout()),
	)
	identityv1.RegisterIdentityServiceServer(srv, svc)
	return srv
}

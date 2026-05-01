package server

import (
	identityv1 "micro-one-api/api/identity/v1"
	"micro-one-api/internal/identity/service"

	kgrpc "github.com/go-kratos/kratos/v2/transport/grpc"
)

// NewGRPCServer wires gRPC transport for identity-service.
func NewGRPCServer(addr string, svc *service.IdentityService) *kgrpc.Server {
	srv := kgrpc.NewServer(
		kgrpc.Address(addr),
	)
	identityv1.RegisterIdentityServiceServer(srv, svc)
	return srv
}

package server

import (
	billingv1 "micro-one-api/api/billing/v1"
	"micro-one-api/internal/billing/service"
	apptimeout "micro-one-api/internal/pkg/timeout"

	kgrpc "github.com/go-kratos/kratos/v2/transport/grpc"
)

// NewGRPCServer wires gRPC transport for billing-service.
func NewGRPCServer(addr string, svc *service.BillingService) *kgrpc.Server {
	srv := kgrpc.NewServer(
		kgrpc.Address(addr),
		kgrpc.Timeout(apptimeout.GetGRPCTimeout()),
	)
	billingv1.RegisterBillingServiceServer(srv, svc)
	return srv
}

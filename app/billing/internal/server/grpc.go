package server

import (
	billingv1 "micro-one-api/api/billing/v1"
	"micro-one-api/app/billing/internal/service"
	apptimeout "micro-one-api/pkg/timeout"

	kgrpc "github.com/go-kratos/kratos/v3/transport/grpc"
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

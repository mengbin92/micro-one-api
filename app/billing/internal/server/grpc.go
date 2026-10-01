package server

import (
	"os"

	billingv1 "micro-one-api/api/billing/v1"
	"micro-one-api/app/billing/internal/service"
	apptimeout "micro-one-api/pkg/timeout"
	"micro-one-api/platform/grpc/xgrpc"

	kgrpc "github.com/go-kratos/kratos/v3/transport/grpc"
	"google.golang.org/grpc"
)

// NewGRPCServer applies the same dedicated caller/full-method authentication
// used by the other resource owners, including the legacy shared principal.
// Resource authorization and self/system boundaries remain owner responsibilities.
func NewGRPCServer(addr string, svc *service.BillingService) *kgrpc.Server {
	serviceToken := os.Getenv("SERVICE_TOKEN")
	srv := kgrpc.NewServer(
		kgrpc.Address(addr),
		kgrpc.Timeout(apptimeout.GetGRPCTimeout()),
		// Metrics outermost: server-side handling latency (incl. auth) is the
		// discriminating signal when client-side dependency latency moves
		// (O5 attribution: relay-side contention vs downstream slowdown).
		kgrpc.UnaryInterceptor(
			xgrpc.MetricsUnaryServerInterceptor("billing-service"),
			serviceTokenUnaryInterceptor(serviceToken),
		),
		kgrpc.StreamInterceptor(serviceTokenStreamInterceptor(serviceToken)),
	)
	billingv1.RegisterBillingServiceServer(srv, svc)
	return srv
}

func serviceTokenUnaryInterceptor(serviceToken string) grpc.UnaryServerInterceptor {
	return xgrpc.ServiceTokenUnaryInterceptor(serviceToken)
}

func serviceTokenStreamInterceptor(serviceToken string) grpc.StreamServerInterceptor {
	return xgrpc.ServiceTokenStreamInterceptor(serviceToken)
}

package xgrpc

import (
	"context"
	"time"

	"micro-one-api/platform/metrics"

	"google.golang.org/grpc"
	"google.golang.org/grpc/status"
)

// UnaryClientMetricsInterceptor observes the gRPC call latency towards a
// dependency (service/method/status) into metrics.ServiceDependencyLatency.
//
// v0.18 P2 C5: ServiceDependencyLatency was registered in platform/metrics
// but never instrumented — no client interceptor called Observe — so the
// BASELINE PromQL query (micro_one_api_dependency_grpc_latency_seconds)
// always returned empty and the table stayed "N/A — not scraped". Wire this
// interceptor on each grpc.NewClient dial to start collecting the baseline.
func UnaryClientMetricsInterceptor(serviceName string) grpc.UnaryClientInterceptor {
	return func(ctx context.Context, method string, req, reply any, cc *grpc.ClientConn, invoker grpc.UnaryInvoker, opts ...grpc.CallOption) error {
		start := time.Now()
		err := invoker(ctx, method, req, reply, cc, opts...)
		if metrics.ServiceDependencyLatency != nil {
			metrics.ServiceDependencyLatency.WithLabelValues(
				serviceName, method, status.Code(err).String(),
			).Observe(time.Since(start).Seconds())
		}
		return err
	}
}
